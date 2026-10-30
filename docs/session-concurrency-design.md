# Session Concurrency Design — one session, one turn

> Status: **implemented.** The gate, the cross-client 409 and the frontend's
> busy-state handling are all in the code — §5 points at where each piece
> lives. §2 still describes the code as it was **before** the change, so it
> reads as history.
>
> ★ **Successor.** [run-lifecycle-design.md](run-lifecycle-design.md) proposes
> replacing the gate's mutex with a lease and a fence, and demoting the SSE
> connection to a subscriber. It is a proposal — nothing in it is in the tree
> yet. The rule this document fixes ("one session, one turn") stays.
>
> Scope: the `/v2` run path (`POST /v2/run`), the session state projection
> (`GET /v2/sessions/{id}` and `GET /v2/sessions/{id}/messages`), and the
> frontend's ability to know that a session is busy.

## 1. The problem

Two browser tabs (or two windows, or one tab plus `curl`) open the same
session. The first one sends a message; the second one sends another. What
happens is not "two turns run side by side" — it is **the second message
kills the first turn**.

> ★ **The line numbers in this section are the ones the code had
>   _before_ this change.** They are kept so the "before" state stays
>   readable; §5 points at where the code lives now.

The frontend guard looks like it prevents this, and it does — inside one tab:

```tsx
// web/src/components/composer.tsx:47
const busy = status === "running"
```

`status` comes from the zustand store, so it is **per-tab memory**. A new
tab starts with `emptyConversation()` (`web/src/lib/types.ts:125`), which is
`status: "idle"`. Nothing tells the new tab that the session is busy, so its
composer is enabled and its send button works.

### 1.1 The three gaps

| # | Gap | Where |
|---|---|---|
| 1 | The busy guard lives in per-tab memory, not in the server | `web/src/components/composer.tsx:47` |
| 2 | The server already computes "is this session running" but the frontend throws that answer away | `api/v2_sessions.go:441` → `web/src/lib/store/actions.ts:85` |
| 3 | The server has no admission gate: a second turn **preempts** the first one | `agentkit/run/runtime.go:239` |

**Gap 2 in detail.** `stateOf` already answers the question:

```go
// api/v2_sessions.go:441
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	if sess.HasPending() {
		return stateWaiting
	}
	if status, ok := s.rt.StatusOf(ctx, sess.ID()); ok && status.Phase == run.PhaseRunning {
		return stateRunning
	}
	return stateIdle
}
```

`v2Messages` puts it on the wire as `view.State` (`api/v2_sessions.go:288`).
The frontend receives it — and then puts it in `meta.state`
(`web/src/lib/store/actions.ts:85`) while the composer reads `convo.status`.
**Two state lines, one of them dead.** `seedFromHistory` only ever sets
`awaiting-approval` (`web/src/lib/store/history.ts:107`); it never sets
`running`, so a fresh tab hydrates as idle no matter what the server said.

**Gap 3 in detail.** `Runtime.Submit` does not refuse a second turn — it
takes over:

```go
// agentkit/run/runtime.go:239
if prev != nil && prev != turn {
	// Release the outgoing turn: its Wait must fail with ErrSuperseded and
	// its OnAgentEvents handshake must not block on a Complete that will
	// never arrive.
	prev.fail(ErrSuperseded)
}
```

The superseded tab's stream gets `RUN_ERROR{code:"turn_superseded"}`
(`api/v2_run.go:184`). The new tab runs. To the user this reads as "the app
lost my message".

### 1.2 The collateral damage

`Submit` does two destructive things **before** it starts the new turn
(`agentkit/run/runtime.go:112-118`):

```go
rt.discardCheckpoint(ctx, sess)      // deletes the running turn's checkpoint
if pending, perr := sess.PendingApproval(); ... {
	sess.ClearPendingApproval()       // drops the running turn's pending approval
}
```

So the second tab does not merely cancel the first tab's turn — it deletes
the checkpoint that turn would have resumed from, and clears the approval
the first tab is showing. When the first tab then answers its approval, it
gets `no_checkpoint` (`api/v2_types.go:121`). The user's decision is
silently discarded.

### 1.3 The upstream position

Upstream (`heron-bk`) documents preemption as a deliberate product
semantic, not an accident:

> ★ 「停止」与「抢占」是两件事：
> - **停止**（`stop()`）= 终止这一轮，用户接着说下一句。
> - **抢占**（在回答途中发新消息）= 旧轮次在安全点让位，新消息接管。
>
> — `heron-bk/docs/frontend-design.md:817`

Two things follow from it:

1. Preemption is a **same-client** gesture: the user interrupts *their own*
   answer by typing the next thing.
2. The upstream frontend already prevents the cross-client case the same
   way this one does — `heron-bk/web/src/components/composer.tsx:69` has the
   identical `const busy = status === "running"`. **The upstream guard has
   the same hole.** Both repos inherit it.

This design **drops preemption** (see [§4](#4-decisions)). That is a
deliberate narrowing of the upstream semantic, taken because with no client
identity there is no way to tell "the user interrupting themselves" from
"another tab cutting in", and the cost of guessing wrong (silently
destroying another surface's turn, checkpoint and approval) is much higher
than the cost of refusing.

## 2. What happens today

```
tab A                          tab B
──────                         ───────
send "hello"
POST /v2/run
  Submit → startTurn
  loop running, SSE streaming

                               open session (fresh tab)
                               GET /v2/sessions/{id}/messages
                                 → view.State = "running"
                                 → stored in meta.state
                                 → convo.status = "idle"   ← ignored

                               send "world"   (enabled: status is idle)
                               POST /v2/run
                                 Submit → discardCheckpoint()   ← deletes A's checkpoint
                                            ClearPendingApproval()← drops A's approval
                                            startTurn → prev.fail(ErrSuperseded)
                               200, SSE streaming

  SSE: RUN_ERROR{code:"turn_superseded"}
  status → "error"
  if it had been waiting for approval:
    answering it → 409 no_checkpoint
```

The transcript keeps both user messages (both `Append`s succeed), so the
history looks plausible — which is why this reads as "the app lost my
message" rather than "two clients raced".

### 2.1 The lock's granularity

**The unit of exclusion is the session id.** One session, one live turn —
two *different* sessions run concurrently without interfering, and two
clients on the *same* session exclude each other.

This is not a new decision: it is the granularity the runtime already
uses. `Runtime` keys its per-session state by session id:

```go
// agentkit/run/runtime.go:91
states map[string]*sessionState
```

and every turn-level fact — the live turn, the loop, the start time, the
last error — hangs off `sessionState` for that id
(`agentkit/run/bridge.go:29`). The gate in §5.1 reads exactly that state.
**The exclusion is per session id, not per session object and not per
process.**

Two consequences follow, and they are different in kind:

| Scope | What is excluded | Mechanism |
|---|---|---|
| **Within one process** | two turns on the same session id | `Runtime.states[sessionID]` — in-memory, authoritative |
| **Across processes** | two turns on the same session id | **nothing** — see below |

### 2.2 The gate's two halves have different durability

`ensureIdle` (§5.1) checks two facts, and they are **not** equally
persistent:

| Check | Source | Survives a restart? |
|---|---|---|
| a pending approval exists | `sess.PendingApproval()` — read from the session file | **yes** |
| a live turn is executing | the runtime's in-memory `sessionState` (the `turn`+`loop` pair, §5.1.1 Hole 2) | **no** |

The pending approval is durable state (`storage/filestore/session.go:43`,
persisted to the session's `state.json`). The live turn is **volatile
execution state** — by design: `sessionState` is "deliberately not the
session: sessions are durable data, this is volatile execution state"
(`agentkit/run/bridge.go:27-28`).

So the honest statement of the rule is:

> **Within one server process, a session id has at most one live turn.
> Across processes, only the pending-approval half is enforced.**

### 2.3 Why one process is the operative assumption

openheron is a **single-operator, single-process, no-auth** runtime
(`heron-bk/docs/api-v2-design.md:229` — "heron 是单机、单操作者、无鉴权的
运行时"). Two processes pointed at the same config root is not a supported
deployment: they would already race on the session files themselves
(`storage/filestore` writes the transcript with plain `O_APPEND` and keeps
sessions in an in-process cache, `storage/filestore/manager.go:105`).

So the in-memory half of the gate is the right scope for this codebase.
Recording it explicitly matters, though: **the exclusion is a property of
one process's runtime, not a distributed lock.** If openheron ever grows a
second server process against the same state directory, this gate becomes
best-effort for the "live turn" half, and the fix would have to move into
the storage layer (a lock file per session id) — not into the runtime.

### 2.4 What the lock is *not*

- **Not per agent.** Two sessions running the same agent are unrelated;
  `heron-bk/docs/agent-design.md:91` is explicit that agent instances are safely
  reusable concurrently.
- **Not per user or per client.** There is no client identity (D7), so
  there is nothing to lock *on* except the session id.
- **Not global.** A global lock would serialize unrelated sessions — the
  runtime already runs them concurrently (`Runtime.states` is a map, not a
  single slot), and nothing here changes that.

### 2.5 The two axes of concurrency — this design touches only one

openheron has **two independent kinds of concurrency**, and conflating them
is the easy mistake here.

| Axis | What runs concurrently | Where it lives | Affected by this design? |
|---|---|---|---|
| **A · turn-level** | two *sessions* running at the same time | one `Runtime.states` entry per session id | **no** — untouched |
| **B · agent-level** | several *agents* inside one turn | inside one turn's loop | **no** — untouched |
| **C · cross-client** | two *clients* on one session | one session id, one turn slot | **yes** — this is the target |

**Axis B is the parallel-delegation feature, and it is unaffected.**
Upstream is explicit that it comes from the framework, not from openheron:

> **① 并行是框架给的，不是自己造的**
> eino 已经能并行跑多个 `task` 调用。heron **不引入** supervisor / workflow /
> 自造调度器
>
> — `heron-bk/docs/multi-agent-design.md:50`

and that the two features are orthogonal:

> ★ 两者**正交**：切到 `pm` 之后，pm 依然可以并行分派给它的 3 个工程师。
>
> — `heron-bk/docs/multi-agent-design.md:18`

**Why axis B is unaffected, mechanically.** A sub-agent is a **tool** of its
parent (`agentkit/build/assemble.go:97` puts the children in `cfg.SubAgents`),
and the framework runs sibling tool calls concurrently. Every one of those
children runs **inside the parent's turn**:

- They share the parent's loop, so they share its `sessionState` entry.
- They never call `Runtime.Submit` — `Builder.Build` ignores its session-id
  argument entirely (`agentkit/build/builder.go:69`, the parameter is `_ string`).
- The runtime's turn bookkeeping is **one turn per session id**, and the
  parallel children are *not turns*: they are tool calls inside one turn.

So the gate in §5.1 cannot see them, and must not: a turn that fans out to
three engineers is still **one** turn on **one** session id. Refusing it
would break the parallel-delegation feature outright.

**What the gate sees is the user's interaction unit**, which is exactly what
§3 states: *one user turn per session at a time*. The user's mental model
is "I ask, it answers, I ask again" — a **serial** interaction with a
session, even though the work inside one turn may fan out to many agents
in parallel.

The distinction that matters:

- **Inside one turn**: any amount of agent-level parallelism is fine.
- **Across turns on one session**: only one turn at a time — that is what
  this design enforces.

A second `/v2/run` on a busy session is a second **turn**, not a second
agent. If it were the latter, the gate would be wrong.

## 3. The rule

> **A session has at most one live turn. A second `/v2/run` on a busy
> session is refused with 409 and changes nothing.**

Three parts, each with a specific owner:

### 3.1 The server is the authority

The guard moves from per-tab memory into `agentkit/run`. `Runtime.Submit`
and `Runtime.Resume` refuse when the session already has a live turn.

This is the only part that is not negotiable: **a client-side guard is a
courtesy, a server-side guard is a rule.** Any client that is not the
openheron web UI — `curl`, another AG-UI client, a future mobile app —
gets the same protection.

### 3.2 The state projection is the frontend's source of truth

The composer stops reading its own memory for the busy decision and starts
reading the server's answer. `GET /v2/sessions/{id}/messages` already
carries `state`; the frontend must use it.

### 3.3 "Busy" means busy — including "waiting for approval"

A session that is waiting for an approval is **not** free. The context
belongs to the pending decision: the checkpoint is what a resume would
continue from, and the pending approval is what the user is being asked
about. A new query would have to discard both.

`ensureAgentIdle` (`api/v2_sessions.go:221`) already applies exactly this
rule to agent switching, and `v2Compact` (`api/v2_compact.go:45`) applies it
to compaction. **The run path is the only one that does not.** Making it
consistent is the point of this design.

## 4. Decisions

| # | Decision | Why |
|---|---|---|
| D1 | The server refuses the second turn (409), never preempts | A preemption cannot distinguish "the user interrupting themselves" from "another tab cutting in". Refusing is safe in both cases; preempting is destructive in one. |
| D2 | Refusal changes **nothing** — no checkpoint deletion, no approval clearing | The current damage (§1.2) is worse than the conflict itself. Refusal must be inert. |
| D3 | "Busy" includes "waiting for approval" | The context belongs to the pending decision. Matches `ensureAgentIdle` and `v2Compact`. |
| D4 | The frontend reads the server's `state` | One source of truth. The server already computes it; the frontend drops it on the floor. |
| D5 | A busy session gets a **stop-and-take-over** action | "Busy" is not a dead end. The user can stop the other turn and then send. |
| D6 | The status projection stays `idle`/`running`/`waiting_approval` | `state` is a **session-level** fact. "running" means "this session has a live turn" — regardless of which client owns it. Adding a "busy elsewhere" state would need a second axis and a second wire field. |
| D7 | No client identity, no ownership tracking | See D1. The rule is "one turn per session", not "one turn per client". |

### 4.1 Why not "the second turn waits"?

Queueing was considered and rejected: it makes the second tab's send
silently queue behind a turn the user cannot see, and the queue has no
owner — when does it drain? A refusal is visible and actionable.

### 4.2 Why not keep preemption but require an explicit flag?

`POST /v2/run {"preempt": true}` would preserve the upstream gesture
without the ambiguity. It was rejected for this round because it needs a
**client identity** to be safe (see D1) — without one, `preempt: true`
from a second tab still destroys the first tab's turn. With an identity,
it becomes a much larger change (identity issuance, storage, expiry,
"who is this client" across restarts). D5's stop-and-take-over covers the
real need — "I want to interrupt that turn" — without any of that.

If preemption is wanted later, it should be re-introduced as its own
design, with identity, rather than smuggled back in as a flag.

## 5. The design

### 5.1 Server: the admission gate

**New sentinel** in `agentkit/run/errors.go`:

```go
// ErrSessionBusy is returned when a session already has a live turn.
//
// ★ It is a conflict, not a failure: the caller may retry once the
//   session goes idle. The HTTP layer maps it to 409.
ErrSessionBusy = errors.New("session is busy")
```

**The gate** in `agentkit/run/runtime.go`, in `Submit` and `Resume`:

```go
// Submit admits a query into the session.
//
// ★ A session has at most one live turn. A second Submit is refused with
//   ErrSessionBusy rather than preempting the first one: preemption
//   cannot tell "the user interrupting themselves" from "another tab
//   cutting in", and the second case destroys a turn the user cannot
//   see — along with its checkpoint and its pending approval.
func (rt *Runtime) Submit(ctx context.Context, sessionID, query string) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, errors.New("query must not be empty")
	}

	if err := rt.ensureIdle(ctx, sess); err != nil {
		return nil, err
	}

	// From here on the session is ours: the checkpoint and the pending
	// approval belong to a turn that is over, and discarding them is
	// what makes the next turn start from a clean slate.
	rt.discardCheckpoint(ctx, sess)
	if pending, perr := sess.PendingApproval(); perr == nil && pending != nil && len(pending.Interrupts) > 0 {
		if err := sess.ClearPendingApproval(); err != nil {
			rt.logf("session=%s failed to clear the stale pending approval: %v", sessionID, err)
		}
	}

	return rt.startTurn(ctx, sess, loopItem{Query: query}, contract.TurnQuery)
}
```

**`ensureIdle` is the gate.** It lives in `agentkit/run` because that is
where the runtime state lives; the API layer must not re-implement it
(AGENTS.md: "不要在 API 层复制业务装配逻辑").

```go
// ensureIdle refuses the request when the session already owes the user
// something.
//
// ★ Both cases mean the context belongs to something else right now:
//   - a live turn: it is executing, or it is waiting for an approval;
//   - a pending approval: the checkpoint it would resume from is the
//     session's context, and answering it is the only way forward.
//
// ★ The check is a read. It does not touch the checkpoint and it does not
//   clear the pending approval — a refused request must change nothing
//   (design decision D2). Those two side effects live in Submit, after
//   the gate.
//
// ★ It asks about the loop, not the turn pointer — see §5.1.1 Hole 2.
func (rt *Runtime) ensureIdle(ctx context.Context, sess storage.Session) error {
	if p, err := sess.PendingApproval(); err == nil && p != nil && len(p.Interrupts) > 0 {
		return fmt.Errorf("%w: an approval is pending on this session", ErrSessionBusy)
	}
	return rt.ensureNoLiveTurn(ctx, sess)
}
```

and the narrower one, for a resume:

```go
// ensureNoLiveTurn refuses the request when a turn is executing.
//
// ★ It asks about the LOOP, not the turn pointer. st.turn is cleared when
//   the turn finishes archiving (markTurnDone), so it lags: a turn that
//   has already produced its answer still reads as "running" for a
//   moment, and refusing on that would be a false positive. The loop is
//   the thing that is actually executing.
func (rt *Runtime) ensureNoLiveTurn(ctx context.Context, sess storage.Session) error {
	st, ok := rt.lookupState(sess.ID())
	if !ok {
		return nil
	}
	st.mu.Lock()
	turn, loop := st.turn, st.loop
	st.mu.Unlock()
	if turn != nil && loop != nil {
		return fmt.Errorf("%w: a turn is already running on this session", ErrSessionBusy)
	}
	return nil
}
```

> `ensureIdle` reads the same two facts `ensureAgentIdle` reads
> (`api/v2_sessions.go:221`) — a pending approval and the runtime phase.
> The API layer's copy stays where it is (agent switching is an API-level
> concern); the run path's copy lives next to the runtime state that it
> guards. When §7.3 lands, the two merge.

**`PhaseWaitingApproval` matters.** `StatusOf` derives it from the session's
own pending record (`agentkit/run/runtime.go:727`), so a session whose turn
just stopped to ask for approval still reports busy — even though no turn
is executing. That is exactly D3.

**The turn bookkeeping no longer preempts.** `claimTurn` takes the busy
test and the takeover under one lock, and the displaced turn is released
rather than failed:

```go
// agentkit/run/runtime.go — claimTurn
st.mu.Lock()
prev := st.turn
busy := prev != nil && st.loop != nil && conflictTurns(kind, prev.Kind)
if !busy {
	st.turn = turn
	st.loop = loop
	// ...
}
st.mu.Unlock()
```

With the gate in place, `prev` can only be a turn that is **already
finished** (its `Complete` landed but its state was not yet cleared). It
must be released, not failed — `runClaimedTurn` does that:

```go
// agentkit/run/runtime.go — runClaimedTurn
if prev != nil && prev != turn {
	// The outgoing turn has already completed — the claim would have
	// refused a live one. Release its handshake so the OnAgentEvents
	// side is not left waiting for a Complete that will never arrive.
	prev.fail(ErrSuperseded)
}
```

The call itself stays: it releases the handshake. Only its *meaning*
changes — from "the newer turn takes over" to "the previous turn is
already done". `ErrSuperseded` remains the signal; the HTTP layer's
mapping for it is untouched.


> **The race is closed by the claim, not by the check.** The check above
> is the friendly error; the authoritative one is `claimTurn`, which takes
> the busy test and the takeover under **one** lock. Two simultaneous
> requests cannot both pass it — the loser gets `ErrSessionBusy`, not a
> half-started turn.

### 5.1.2 Four timing defects found while implementing it

The three holes above are design mistakes. Implementing them surfaced four
more, all of them about **timing** — and each one changed the design.

#### Timing 1 — `Complete` returned before the archive landed

The HTTP response goes out when `transport.Stream` returns, and
`turn.Complete` is called right after. But `Complete` only **signals** the
archive: the runtime picks the outcome up asynchronously and writes the
messages to the transcript. So the client was told "your turn is done"
while the server had not written the turn's messages yet.

Two consequences, both bad:

- The **gate** saw a session that was still busy. A user who sent the
  next message right after the answer arrived was refused — a false
  positive, on the most ordinary sequence there is.
- The **next turn's input** is built from the transcript
  (`makeGenInput` reads `sess.Messages(true)`), so a turn that started
  before the previous one was archived would read an incomplete history.

**Fix: `Complete` returns only after the archive has landed.** The turn
carries an `archived` channel; the archive closes it, and `Complete` waits
on it. The HTTP response therefore leaves the server after the history is
complete, and the gate's "busy" and the client's "done" cannot disagree.

The wait is bounded (30s): a bug in the archive must not hang a request.

#### Timing 2 — the pending approval was written before the checkpoint

`TestApprovalChainEndToEnd` caught this: it waits for the pending
approval to appear and then asserts the checkpoint file exists. The
approval was there; the checkpoint was not.

The order was always wrong. The framework writes the checkpoint when the
loop **stops** — and the loop stops *after* the turn's callback returns.
The approval was written *inside* that callback. So there was always a
window where the user saw the approval card but answering it failed with
`no_checkpoint` — the "orphan approval" upstream documents
(`heron-bk/docs/checkpoint-store.md:303`).

**Fix: the approval is written when the loop exits, not when the callback
returns.** The callback *holds* it; `loopExited` writes it, after the
framework has written the checkpoint. The approval now never precedes the
thing it needs.

This is not only a fix for the test: it removes the orphan window
altogether.

#### Timing 3 — a resume must not block another resume

The first implementation refused a second `Resume` while the first
resume turn was still settling. That broke three existing tests
(`TestResumeConsumesTheApproval`, `TestMultiplePendingApprovalsAreAnsweredAsAGroup`,
`TestDecisionsAreAppendOnly`), and they were right to break: answering a
second group of approvals while the first answer is still settling is
ordinary use.

The distinction that resolves it: **a resume continues the interrupted
turn it answers** — it is the same round of the conversation, not a new
one (`contract.TurnKind` says so). A **query** is a new round, and that is
exactly what must not overlap.

So the gate asks about the **kind** of the live turn:

| live turn | new query | new resume |
|---|---|---|
| query | **refuse** — the cross-client case | allowed — it answers that query's own approval |
| resume | **refuse** — a new round | allowed — the same round continues |

`conflictTurns` in `agentkit/run/runtime.go` is that rule.

#### Timing 4 — a turn that never starts held the session forever

`Turn.Wait` returns `ErrStartTimeout` when the turn does not become
streamable in time — the agent could not be built, so the loop never
started. Nothing called `fail()` on that path, so the turn was never
released and the `archived` channel was never closed.

With the gate, that is a **locked session**: the slot stays taken, and
every later run is refused. The user could not even start a new turn to
recover.

**Fix: the start timeout fails the turn.** `Wait` calls `fail()` when it
times out, which releases the handshake and the archived channel. A turn
that can neither run nor finish must not hold the session.

#### What all four had in common

Each was a case where **two facts about the same turn were read at
different moments**: "the agent is done" and "the archive has landed", "the
approval is written" and "the checkpoint is written", "a turn is running"
and "a round has started", "the turn gave up" and "the slot is free".

The fix in every case was to make the second fact **wait** for the first
rather than to guess at it. A gate that reads a lagging pointer is a gate
with a hole in it — no matter how carefully the check is written.

### 5.2 Server: the HTTP mapping

`ErrSessionBusy` becomes a 409 with its own code: **`session_busy`**.

The existing code is `agent_busy` (`api/v2_types.go:48`). It is renamed
rather than reused, because the old name says the wrong thing:

| | `agent_busy` today | `session_busy` after |
|---|---|---|
| what it actually guards | the **session**, not an agent | the session |
| its own error message | `"session is busy; finish or stop the run first"` (`api/v2_sessions.go:25`) | unchanged |
| its three call sites | agent switch, compaction, and (new) the run path | unchanged |

Every one of those three is a **session-level** refusal. The message text
already said "session is busy" — the code name was the odd one out. And
with parallel delegation in play (see §2.5), `agent_busy` is actively
misleading: several agents *can* be busy at once, legitimately, inside one
turn. A client that reads `agent_busy` as "the agent is working" would draw
exactly the wrong conclusion.

```go
// api/v2_types.go
const (
	// ...
	// codeSessionBusy is the session-level conflict: a turn is running,
	// or an approval is waiting, so this request must not start.
	//
	// ★ It used to be called agent_busy. That name was wrong: the three
	//   call sites (agent switch, compaction, run) all guard the session,
	//   and parallel delegation means several agents can legitimately be
	//   busy at once.
	codeSessionBusy = "session_busy"
)
```

```go
// api/v2_types.go — v2FailErr
case errors.Is(err, run.ErrSessionBusy):
	v2Fail(c, consts.StatusConflict, codeSessionBusy, err.Error(), true)
```

`retryable: true` — the conflict clears on its own once the running turn
finishes, so retrying later is meaningful. This is the same treatment
`errAgentBusy` already gets (`api/v2_types.go:113`).

> **The rename touches three call sites and one test file.** The Go
> identifier `codeAgentBusy` and the wire value `agent_busy` both change:
>
> | Where | What |
> |---|---|
> | `api/v2_types.go:48` | the constant: `codeAgentBusy` → `codeSessionBusy`, `"agent_busy"` → `"session_busy"` |
> | `api/v2_types.go:113` | the `errAgentBusy` branch (agent switch) |
> | `api/v2_sessions.go:223`, `api/v2_sessions.go:227` | the `errAgentBusy` wraps (agent switch) |
> | `api/v2_compact.go:46` | the compaction refusal |
> | `api/v2_agent_switch_test.go:120`, `api/v2_agent_switch_test.go:285` | the two assertions |
> | `web/test/fake-server.ts:1355` | the fake backend's 409 |
>
> The Go sentinel `errAgentBusy` (`api/v2_sessions.go:25`) is **not**
> renamed — it is an internal value, not a wire code, and its message
> already reads "session is busy". Only the **wire** code changes.
>
> The message text carries the distinction between the two causes ("an
> approval is pending" vs "a turn is already running on this session"), so
> the client can tell them apart without a second code.

### 5.3 Server: `state` must mean what the frontend needs

`stateOf` already answers it, but it has an ordering subtlety worth
stating, because §5.4 depends on it:

```go
// api/v2_sessions.go:441
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	if sess.HasPending() {
		return stateWaiting
	}
	if status, ok := s.rt.StatusOf(ctx, sess.ID()); ok && status.Phase == run.PhaseRunning {
		return stateRunning
	}
	return stateIdle
}
```

- `waiting_approval` wins over `running`: a session waiting for approval is
  busier than one merely running, and the UI shows the approval card.
- `running` is **session-level**: it means "this session has a live turn",
  not "this client owns a turn".

`StatusOf` reports `PhaseWaitingApproval` from the session's own pending
record (`agentkit/run/runtime.go:727`) — so the pending case is covered by
the same lookup, and `stateOf` does not need a second source.

**One gap to close.** `stateOf` reads `HasPending()` and `StatusOf()`
separately, and `StatusOf` reads the pending record again. That is two
reads of the same fact, and they can disagree (the pending approval is
cleared between the two reads). The fix is to make `stateOf` read the
phase **once**:

```go
// after
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	// ★ One read, one answer: the phase already accounts for the pending
	//   approval (StatusOf derives PhaseWaitingApproval from the session's
	//   own pending record), so reading both separately could see them
	//   disagree.
	status, ok := s.rt.StatusOf(ctx, sess.ID())
	if !ok {
		return stateIdle
	}
	switch status.Phase {
	case run.PhaseWaitingApproval:
		return stateWaiting
	case run.PhaseRunning:
		return stateRunning
	default:
		return stateIdle
	}
}
```

> **The gate and `stateOf` do not read the same thing, on purpose.**
> `stateOf` goes through `StatusOf`, which derives `PhaseRunning` from the
> **run ledger** — the scan of the session's runs
> ([run-lifecycle-design.md](run-lifecycle-design.md) §4.1). The gate reads
> the `turn`+`loop` pair directly. The two are released in the same breath:
> the terminal row lands in the archive path (`agentkit/run/loop.go:130`),
> and the gate is released a moment later by `markTurnDone`
> (`agentkit/run/loop.go:167`). So for a moment after a turn's answer has
> landed, the **UI** may already say "idle" while the **gate** has not yet
> admitted the next query.
>
> That asymmetry is the correct way round:
>
> | | lags | consequence of being wrong |
> |---|---|---|
> | `stateOf` (presentation) | may say "idle" a moment too early | the composer enables; the next message is refused with 409 and the bubble rolls back — recoverable, and the truth arrives a moment later |
> | the gate (admission) | must never refuse a free session | a false refusal locks the user out of a session they can use |
>
> Making them read the same thing would give them the same lag — and the
> gate's lag would become a false refusal. So they stay separate, and the
> gate is the stricter one.

### 5.4 Frontend: one state line

**Problem.** `convo.status` is the store's own event-reducer state
(`web/src/lib/events.ts:292`); `meta.state` is the server's answer
(`web/src/lib/store/actions.ts:85`). The composer reads the first, the
sidebar dot reads... also the first. The second is dead.

**Fix: one line, and it is the server's.**

`convo.status` stays the single status field. The server's `state` feeds
it at hydration time, and the event stream keeps it current afterwards.

**Hydration** (`web/src/lib/store/actions.ts:80-90`):

```ts
const view = await api.getMessages(id)
set((s) => ({
	sessions: {
		...s.sessions,
		[id]: {
			meta: { ...(s.sessions[id]?.meta ?? stubMeta(id)), state: view.state },
			convo: seedFromHistory(view.messages, view.pendingApprovals, view.usage, view.state),
			loaded: true,
		},
	},
}))
```

★ **A hydrated "running" is always somebody else's turn.** The surface
that just opened the session cannot own a turn it did not start — a
surface that owns one keeps its own event stream and never re-hydrates
(`openSession` returns early for a loaded session). So the hydration
raises the **busy** flag too, and the composer says whose turn it is.

**`seedFromHistory` learns the server's state** (`web/src/lib/store/history.ts:30`):

```ts
export function seedFromHistory(
	raw: AgMessage[],
	pending?: PendingApproval[],
	usage?: Usage,
	state?: ThreadMeta["state"],
): ConversationState {
	// ...
	const convo = emptyConversation()
	convo.messages = messages
	if (usage) convo.usage = usage
	if (usage?.context) convo.context = usage.context

	// ★ The server's state is the truth about whether this session is
	//   busy. Without this a second tab hydrates as idle and its send
	//   button works — the tab has no memory of the turn another tab
	//   started.
	//
	// ★ A hydrated "running" is ALWAYS somebody else's turn — see above.
	if (state === "running") {
		convo.status = "running"
		convo.busy = true
	}
	if (pending && pending.length > 0) {
		convo.status = "awaiting-approval"
		convo.interrupts = pending.map(/* ... */)
	}
	return convo
}
```

The pending branch keeps its precedence: an approval card is more specific
than "running".

**The composer** (`web/src/components/composer.tsx:47`) does not change:

```ts
const busy = status === "running"
```

It is now reading the right thing. The guard is unchanged; its **input**
is fixed. That is the whole point of the fix — the composer's logic was
never wrong, it was reading per-tab memory.

**One new state: `busy-elsewhere`?** No — D6. `status: "running"` means
"this session has a live turn". The composer shows a stop button for it.
Whether the turn is *this* tab's or another tab's does not change what the
user may do: stop it, or wait.

But there is a UX question: if tab B shows a stop button for a turn tab A
owns, and tab B's user clicks it — **tab B just killed tab A's turn.** Is
that acceptable?

It is the same as today (tab B's send killed it), but *explicit* rather
than accidental. And it is the only way for tab B to proceed. The
alternative — "you cannot stop a turn you did not start" — needs client
identity, which D7 rejects.

So: the stop button stays, and it means "stop this session's turn", whoever
owns it. The button's tooltip should say so.

### 5.5 Frontend: the 409 is a first-class outcome

The send path must handle a refused send **without corrupting the local
view**. Today, a failed send leaves the optimistically-appended user
message in the transcript (`appendLocalUserMessage`, `web/src/lib/store/actions.ts:524`)
and sets `status: "error"` (`loadError`, `web/src/lib/store/actions.ts:442`).
The message never reached the server, but it stays on screen as if it had.

**Server first.** The refusal arrives as a non-2xx response before the
stream starts, so it is a plain JSON error body — `runStream` already
handles it (`web/src/lib/api.ts:319-323`):

```ts
if (!res.ok) {
	const body = (await res.json().catch(() => null)) as ApiErrorBody | null
	handlers.onError?.(body?.error ?? `HTTP ${res.status}`)
	return
}
```

But the **code** is dropped — only `body.error` (the prose) survives. The
frontend needs `code` to tell "busy" from "failed". Fix:

```ts
// web/src/lib/api.ts — runStream
if (!res.ok) {
	const body = (await res.json().catch(() => null)) as ApiErrorBody | null
	handlers.onError?.(body?.error ?? `HTTP ${res.status}`, body?.code)
	return
}
```

```ts
// web/src/lib/api.ts — RunHandlers
export interface RunHandlers {
	onEvent: (ev: AgEvent) => void
	onError: (message: string, code?: string) => void
	onDone: () => void
}
```

**The store** (`web/src/lib/store/actions.ts:250-269`) distinguishes busy
from failed, and rolls the optimistic message back when the send was
refused:

```ts
run = runStream(
	{ threadId, query: trimmed },
	{
		onEvent: (ev) => handleEvent(set, get, localKey, ev as AgEvent),
		onError: (message, code) => {
			run = null
			setCurrentRunKey(null)
			if (code === "session_busy") {
				// ★ The session is busy elsewhere. The message never
				//   reached the server, so the optimistic bubble must go
				//   back — otherwise the transcript claims a message that
				//   was never sent.
				get().rollbackLocalMessage(localKey)
				get().setBusyElsewhere(localKey, message)
				return
			}
			get().loadError(localKey, message)
		},
		onDone: () => { /* ... */ },
	},
)
```

**The composer** shows the busy-elsewhere state. This is the
stop-and-take-over surface (D5): the user is told the session is busy and
offered the stop button.

### 5.6 Frontend: stop-and-take-over

The flow the user should get:

1. Tab B tries to send → 409 `session_busy`.
2. Tab B says: "This session is busy — another window is running it." with
   a **Stop** button.
3. Clicking Stop calls `POST /v2/sessions/{id}/abort`
   (`api/v2_sessions.go:386`) — the existing endpoint, already the real
   stop (`heron-bk/docs/frontend-design.md:801`).
4. The session goes idle → tab B's composer is enabled → the user sends
   again.

The existing `stop()` (`web/src/lib/store/actions.ts:318`) already does
exactly this: abort the local stream, `POST /abort`, report failure
loudly. **It needs no change** — it just needs to be reachable in the
busy-elsewhere state, which today it is not (the composer shows the send
button, not the stop button).

So the frontend change in §5.5 is what unlocks it: `status` becomes
`"running"` when the server says busy, and the composer renders the stop
button for `busy` (`web/src/components/composer.tsx:588`).

The `session_busy` refusal then only needs to say **why** it is busy, so the
user understands that the stop button will act on someone else's turn. The
server's message text carries that ("a turn is already running on this
session").

### 5.7 The full flow after the change

```
tab A                          tab B
──────                         ───────
send "hello"
POST /v2/run → 200, SSE streaming

                               open session
                               GET /v2/sessions/{id}/messages
                                 → view.State = "running"
                                 → seedFromHistory sets status = "running"
                               composer: STOP button, send disabled

                               (user clicks stop)
                               POST /v2/sessions/{id}/abort
                                 → tab A's SSE: RUN_ERROR (stopped)
                                 → session goes idle

                               send "world" → 200, SSE streaming
```

And the racing case:

```
tab A                          tab B
──────                         ───────
send "hello" → running
                               send "world"
                               POST /v2/run
                                 ensureIdle → ErrSessionBusy
                                 → 409 {"code":"session_busy",
                                        "error":"session is busy: a turn is
                                                 already running on this session"}
                               optimistic bubble rolled back
                               composer shows stop + "busy elsewhere"

  tab A's turn: UNTOUCHED
  checkpoint: UNTOUCHED
  pending approval: UNTOUCHED
```

## 6. What changes where

| Area | File | Change |
|---|---|---|
| sentinel | `agentkit/run/errors.go` | add `ErrSessionBusy` |
| gate | `agentkit/run/runtime.go` | add `ensureIdle` and `ensureNoLiveQuery`; call them in `Submit` and `Resume`; add `claimTurn` (the atomic test-and-take) and `conflictTurns` (query vs resume); move the destructive `discardCheckpoint` / `ClearPendingApproval` after the gate; `Abort` clears the void approval |
| ordering | `agentkit/run/turn.go` | `Complete` waits for the archive; the `archived` channel |
| ordering | `agentkit/run/loop.go` | the archive holds the pending approval instead of writing it |
| ordering | `agentkit/run/bridge.go` | the loop's exit writes the held approval — after the checkpoint |
| HTTP | `api/v2_types.go` | map `run.ErrSessionBusy` → 409 `session_busy`; rename `agent_busy` → `session_busy` |
| HTTP | `api/v2_sessions.go` | `stateOf` reads the phase once |
| frontend | `web/src/lib/api.ts` | `onError` carries the error `code` |
| frontend | `web/src/lib/store/history.ts` | `seedFromHistory` takes the server's `state` |
| frontend | `web/src/lib/store/actions.ts` | pass `view.state`; handle `session_busy`; roll back the optimistic message (`markBusy`) |
| frontend | `web/src/lib/store/selectors.ts` | `selectBusyElsewhere` |
| frontend | `web/src/lib/types.ts` | `ConversationState.busy` |
| frontend | `web/src/components/composer-meta.tsx` | the busy line |
| frontend | `web/src/components/composer.tsx` | pass the busy line through |
| tests | `agentkit/run/gate_test.go` | the gate: refusal, inertness, the resume exception |
| tests | `agentkit/run/runtime_test.go` | the preemption test becomes a refusal test |
| tests | `api/v2_busy_test.go` | 409 + `session_busy`; the running turn survives |
| tests | `web/test/session-busy.test.ts` | hydration sets `running` |
| tests | `web/test/store-busy.test.ts` | a 409 rolls the bubble back and marks the session busy |
| tests | `web/test/e2e/session-busy.spec.ts` | a second surface sees the session as busy |
| tests | `web/test/fake-server.ts` | the `busySession` scenario |
| docs | `docs/architecture.md` + `_zh` | the admission rule (§5.3) |
| docs | `docs/user-manual.md` + `_zh` | what a user sees when a session is busy (§6.7) |
| docs | `docs/developer-manual.md` + `_zh` | how the gate works, the three orderings (§3.8) |

### 6.1 `Resume` needs the gate too

`Runtime.Resume` (`agentkit/run/runtime.go:451`) answers an approval. It
must **not** be gated on "no pending approval" — a pending approval is
exactly what it is there to answer. But it must be gated on "no live
turn", using the same `ensureNoLiveTurn` as everything else (§5.1.1 Hole 2
for why it asks about the loop, not the turn pointer):

```go
func (rt *Runtime) Resume(ctx context.Context, sessionID string, decisions []ApprovalDecision) (*Turn, error) {
	// ...
	// ★ The pending approval is what this call is for — do not refuse on
	//   it. But a live turn means somebody else's turn is running, and
	//   this one would take it over.
	//
	// ★ And it goes FIRST: TakePendingApproval consumes the record and
	//   writes the disk, so a refusal after that point would eat the
	//   user's decision (§5.1.1 Hole 1).
	if err := rt.ensureNoLiveTurn(ctx, sess); err != nil {
		return nil, err
	}
	// ...
}
```

So the gate is **two methods**, not one:

- `ensureIdle` — for a new query: refuses on a live turn **and** on a
  pending approval.
- `ensureNoLiveTurn` — for a resume: refuses only on a live turn.

`ensureIdle` is `ensureNoLiveTurn` plus the pending check. That keeps each
one to a single clear rule.

### 6.2 `Submit`'s destructive steps move after the gate

Today (`agentkit/run/runtime.go:112-118`) the checkpoint is discarded and
the pending approval cleared **before** `startTurn`. Those are only correct
once the session is known to be ours. The gate moves them:

```
now:   Get → discardCheckpoint → ClearPendingApproval → startTurn
after: Get → ensureIdle (refuse here, touching nothing) → discardCheckpoint
                                                        → ClearPendingApproval
                                                        → startTurn
```

This is D2 — the refusal must be inert — and it is what stops the second
tab from destroying the first tab's checkpoint and approval (§1.2).

### 6.3 The `turn_superseded` code stays

`turn_superseded` is still produced: two clients racing past the gate
(§5.1's race note) and the `startTurn` handshake release both use it. The
code and its mapping (`api/v2_run.go:184`) are unchanged. Only the
*ordinary* path stops producing it.

## 7. Testing

### 7.1 Go — the gate

`agentkit/run/gate_test.go`:

| Test | Asserts |
|---|---|
| `TestSubmitRefusalIsInert` | after the refusal: the pending approval still exists, the transcript is unchanged, the running turn is not released |
| `TestSubmitRefusesWhileAnApprovalIsPending` | a pending approval blocks a new query |
| `TestResumeIsAllowedWhileAnApprovalIsPending` | a resume still works — the gate does not block the answer |
| `TestRefusedResumeKeepsTheApproval` | a refused resume leaves the pending approval intact — the user's decision is not eaten (§5.1.1 Hole 1) |
| `TestSubmitIsNotRefusedWhileTheTurnIsArchiving` | the finishing window: a turn whose answer has landed is **not** refused — no false positive |
| `TestAStartTimeoutReleasesTheSession` | a turn that never starts does not hold the session forever (§5.1.2 Timing 4) |
| `TestAbortClearsTheVoidApproval` | aborting a turn that was waiting for approval clears that approval — stop-then-take-over actually takes over (§5.1.1 Hole 3) |

`agentkit/run/runtime_test.go`:

| Test | Asserts |
|---|---|
| `TestASecondSubmitIsRefusedWhileATurnRuns` | a second `Submit` on a live session returns `ErrSessionBusy`, the running turn survives, and the refused query is not persisted |

The refusal-is-inert test is the important one: it is the regression test
for §1.2.

### 7.2 Go — the HTTP mapping

`api/v2_busy_test.go`:

| Test | Asserts |
|---|---|
| `TestV2RunOnBusySessionIsConflict` | 409 + `code == "session_busy"`, and the conflict is retryable |
| `TestV2RunOnBusySessionDoesNotDisturbTheRun` | the session still reports `"state":"running"` |

### 7.3 Frontend — unit

`web/test/session-busy.test.ts` — the hydration:

| Test | Asserts |
|---|---|
| the server's running state hydrates as running | `seedFromHistory(..., "running")` → `status === "running"` |
| a hydrated running turn is marked busy | the same call → `busy === true` — it belongs to another surface |
| an idle session is not busy | `seedFromHistory(..., "idle")` → `busy` is not set |
| a pending approval wins over the running state | `seedFromHistory(..., "running", [approval])` → `status === "awaiting-approval"` |

`web/test/store-busy.test.ts` — the refusal:

| Test | Asserts |
|---|---|
| the optimistic bubble is rolled back | after a refused send, the transcript has no user bubble |
| the session is marked busy, not failed | `status === "running"`, `error.code === "session_busy"` |
| a plain failure is still an error | a non-409 failure sets `status: "error"` and leaves `busy` unset |

### 7.4 Frontend — E2E

`web/test/e2e/session-busy.spec.ts`:

| Spec | Asserts |
|---|---|
| a second surface sees the session as busy | the busy line is there, the stop button is offered, and the send button is not |
| a raced request is refused and leaves no trace | the refused message is not in the transcript, and the surface says why |
| a busy session can be taken over | stopping the turn clears the busy line |

The E2E needs the fake backend (`web/test/fake-server.ts`) to hold one
session's state across two surfaces — it keeps `state` per thread, and the
`busySession` scenario keeps a turn open so the session stays busy.

> The upstream rule applies: E2E uses the fake backend, never the real one
> (AGENTS.md, "E2E 用假后端，不要改成真后端"). The real-backend half is
> covered by `make smoke`.

## 8. Open questions

1. ~~**The `ensureIdle` / `startTurn` race.**~~ **Resolved.** The gate is
   now a compare-and-swap: `claimTurn` takes the busy test and the
   takeover under one lock, so two simultaneous requests cannot both
   pass. The loser gets `ErrSessionBusy` — a 409, not a half-started
   turn. The check (`ensureIdle`) stayed as the friendly error that
   explains *which* conflict it is.

2. **`state` in the session list.** `GET /v2/sessions` already returns
   `state` per row (`api/v2_types.go:151`), but the sidebar only renders
   the pending-approval dot (`web/src/components/sidebar.tsx:125`). Should
   the sidebar show a "running" dot per row, so a user with many sessions
   can see which one is busy? **Recommendation: yes, but as a separate
   change** — it needs a refresh trigger, and the sidebar does not poll.

3. **Does the busy state survive a page reload in the same tab?** **Yes,
   and it is now the recovery path.** A reload hydrates `state: "running"`
   and shows the busy line and the stop button: the user can stop the
   orphaned turn and take over. Before this change the reload showed an
   idle composer — the turn was still running on the server, and the
   interface said nothing about it (AGENTS.md's "假停止" note). **No extra
   work.**

## 9. Rollout

The change is one coherent unit — the gate without the frontend fix leaves
the second tab's 409 as a raw error message, and the frontend fix without
the gate reads a `state` that the server computes but nothing enforces.

Suggested order within the change:

1. `agentkit/run`: `ErrSessionBusy`, `ensureIdle`, `ensureNoLiveTurn`, the
   gate in `Submit`/`Resume`, the destructive steps moved after the gate,
   and the compare-and-swap from §8.1.
2. `api`: the 409 mapping, `stateOf` reading the phase once.
3. Frontend: `onError` carrying the code, `seedFromHistory` taking the
   state, hydration passing it, the 409 handling with the rollback.
4. Tests: Go first (the gate is the contract), then the frontend unit
   tests, then the E2E specs.
5. Docs: architecture (the rule), user manual (what the user sees),
   developer manual (the gate and how to debug it) — English and Chinese
   together, identifiers matching one-for-one.

**Upstream note.** `heron-bk/docs/frontend-design.md:817` documents
preemption as a product semantic, and this design removes it. The upstream
repository is a **separate codebase** (`module heron`, its own git history)
that this repository does not import — the two share no code, only a
lineage. So nothing here requires a change there, and this design makes
none. The upstream documents will describe the old behaviour until someone
carries this design across; that is a separate, deliberate porting step,
not a prerequisite.

The same applies to the `agent_busy` → `session_busy` rename (§5.2): it
touches **this** repository's wire contract only. Upstream's own
`api/v2_types.go` keeps `codeAgentBusy`, and its error tables keep
listing `agent_busy` — unchanged, because upstream is not being modified
by this work.

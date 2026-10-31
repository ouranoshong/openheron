# Run Lifecycle Design — a turn outlives its connection

> openheron binds one round of conversation to one HTTP connection: the
> connection is what drains the event stream, and the archive is what the
> connection hands back. This document breaks that binding — the run becomes
> a durable object with its own identity, ownership and truth, and the SSE
> connection becomes one optional view of it.
>
> ★ **Section 1 describes the code as it is.** Every claim there points at a
> line in the tree. **The proposal is marked per section**, because it is now
> half built:
>
> | Section | Status |
> |---|---|
> | §1 What "one turn" means today | **as it is** |
> | §2 The target model | proposal — the shape §3 and §4 already have |
> | §3 D1 — a run is a row of facts | **implemented** |
> | §4 D2 — notification is a hint, the scan is the truth | the server side is **implemented**, the client side is a proposal |
> | §5 D3 — lease and fence | **implemented** — the arbiter, the progress-driven renewal and the read-side fence |
> | §6 D4 — the driver is pluggable | the interfaces and the file driver are **implemented**, the memory arbiter is not there yet |
> | §7 The event pipeline | **implemented** — the derivation moved to `agentkit/emit`, the run owns its consumption |
> | §8 The API surface | the scan routes are **implemented**, attach is a proposal |
> | §9 The frontend | proposal |
>
> A design document that blurs "what is" into "what would be" is how
> documentation drift starts.

> 中文版：[run-lifecycle-design_zh.md](run-lifecycle-design_zh.md)
>
> 关联文档：[session-concurrency-design.md](session-concurrency-design.md)
> 描述了「一个会话一轮」的准入闸门——本文是它的**继任方案**，不是它的补充。

## 1. What "one turn" means today

### 1.1 The custody chain

A user message travels this path today. Every arrow is a real function call in
the tree:

```
POST /v2/run
  │
  ├─ api/v2_run.go:192   startRun → run.Runtime.Submit
  │    └─ agentkit/run/runtime.go:177   run.Runtime.Submit
  │         └─ agentkit/run/runtime.go:227   claimTurn        (the gate: a mutex)
  │              └─ agentkit/run/runtime.go:279   openRunRow     ← the row is written
  │              └─ agentkit/run/runtime.go:336   runClaimedTurn
  │                   └─ agentkit/run/bridge.go:99   startLoop → a goroutine
  │                        └─ agentkit/run/loop.go:21   newLoop → adk.TurnLoop
  │
  ├─ agentkit/run/loop.go:110  makeOnAgentEvents  (inside the loop goroutine)
  │    └─ agentkit/run/loop.go:119  emit.Derive(events, …)   ← the run reads its own events
  │         └─ agentkit/emit/emit.go:150  src.Next()       ← the ONLY consumer
  │         └─ agentkit/emit/emit.go:…    publishes contract.RunEvent to the subscribers
  │    └─ agentkit/run/loop.go:134  rt.archive(sess, turn, out)
  │         └─ agentkit/run/loop.go:…    sess.Append(msg)   ← the transcript
  │    └─ agentkit/run/loop.go:166  rt.finishRun(turn, out)  ← the terminal row
  │
  └─ api/v2_run.go:157  turn.Subscribe()      (the connection subscribes)
       api/v2_run.go:160  transport.Render(stream, sess.ID(), turn.RunID, sub.Events())
            └─ transport/transport.go:60   Render: contract.RunEvent → AG-UI frames
       api/v2_run.go:165  turn.WaitArchived()   (the response waits for the archive)
```

★ **The persistence path no longer runs through the rendering path.** The run
reads the agent's events itself (`agentkit/run/loop.go:119`), derives what
they mean, and archives the result. The connection subscribes to what the run
publishes (`api/v2_run.go:157`) and renders it — it is a view of the run, not
its output pipeline. A connection that dies no longer stops the archive.

★ The ledger's row is written by the run's own goroutine as well:
`openRunRow` writes it before the loop starts, and the terminal row is
written by `rt.finishRun`. The row is a fact about the run; the transcript is
a fact about the conversation.

### 1.2 Five consequences

★ Two of them are already partly addressed by §3 — the row exists, so the run
has a trace and an identity. The other three are untouched. The consequences
are listed with their status so that the reader knows which part of the
proposal is still load-bearing.

**C1 — A run has no durable identity.** — *partly addressed by §3*
`Turn.RunID` (`agentkit/run/turn.go:31`) is now assigned when the turn is
claimed (`agentkit/run/runtime.go:214`), and the API hands its own id down
(`api/v2_run.go:218`) so that the id in the SSE frames and the id in the
ledger's rows are the same one. `Runtime.StatusOf` reads it back from the
scan, so `GET /v2/sessions/{threadId}` returns a real `runId`
(`api/v2_sessions.go:94`) instead of an empty string. **What is still
missing:** the client never sends one (`web/src/lib/store/actions.ts:250-254`),
so "re-attach to a run you know" does not exist yet — that is §8.

**C2 — A crash loses the run silently.** — *partly addressed by §3*
The run's row is written before the loop starts
(`agentkit/run/runtime.go:259`), so a crash leaves a trace: the row is still
open, and the startup pass closes it as `interrupted`
(`storage/filestore/manager.go:245`). The session no longer reads as "never
ran" — it reads as "a run was interrupted". **What is still missing:** the
messages are still lost with the crash, because the transcript is written by
the renderer (§7). The row records that the run happened; it does not record
what it said.

**C3 — A dead handler wedges the session.**
`agentkit/run/runtime.go:326` detaches the run from the request:
`context.WithCancel(context.WithoutCancel(ctx))`. The stored `turn.cancel` is
called from exactly one place — `agentkit/run/bridge.go:129`, *after* the loop
exits. But the loop cannot exit while `agentkit/run/loop.go:120` is blocked on
`turn.done`, and `turn.done` is only fed by `Complete` (the handler) or `fail`
(`agentkit/run/turn.go:194`). So a handler that dies without calling
`Complete` — a panic, a killed worker — leaves the run goroutine blocked
forever and the session permanently busy. `Abort` is the only escape
(`agentkit/run/runtime.go:565`), and nothing calls it automatically.
★ The ledger makes the wedge **visible** — the scan shows a run in state
`running` that nobody owns — but it does not lift it: the gate still refuses
the session. Lifting it is §5's lease and §7's pipeline.
(An ordinary client disconnect is *not* this case: the handler keeps running
and the drain continues, because `transport/transport.go:611-623` only stops
*emitting* after a write error — the accumulation at lines 167/174/182 goes on.)

**C4 — A live run is invisible to a second surface.**
`turn.events` is a single-consumer `*adk.AsyncIterator`
(`agentkit/run/turn.go:33`). Whoever drains it first owns it. A second browser
cannot attach to a running turn, and `GET /v2/sessions/{threadId}/messages`
(`api/v2_sessions.go:269`) shows only what is already archived — so a fresh
page shows a conversation that stops mid-thought with no indication that
something is still running.

**C5 — Ownership is first-come-first-served, with no notion of liveness.**
`claimTurn` (`agentkit/run/runtime.go:207-241`) is a mutex: whoever gets the
lock first owns the session until it finishes. The run now carries an epoch
(`agentkit/run/runtime.go:259`), so the ledger can order two owners — but
there is no lease, no expiry, and no way to distinguish "the owner is making
progress" from "the owner is stalled". The in-memory identity checks that
exist today — `markTurnDone` (`agentkit/run/bridge.go:51`) and the
loop-identity check in `loopExited` (`agentkit/run/bridge.go:115-124`) — are
hand-rolled fences over pointers. They work inside one process and nowhere
else.

### 1.3 The gate from the previous round

[session-concurrency-design.md](session-concurrency-design.md) established the
rule: one session, one turn; a second query is refused with 409
`session_busy` (`api/v2_types.go:48`, `api/v2_types.go:110-116`); preemption is
gone. That rule stays. What changes here is **what the gate is made of** — a
mutex over a pointer becomes a lease with a fence, and the busy state stops
being an in-memory projection and becomes a scanned fact.

## 2. The target model

### 2.1 The run is the unit; the connection is a view

```
                     POST /v2/run  (runId = the client's run id)
                              │
                              ▼
                 agentkit/run:  Runtime.Submit
                     │
                     ├─ ledger.NextEpoch(session)  →  epoch n
                     ├─ arbiter.Claim(session, runId, epoch n)  →  Lease{ExpiresAt}
                     ├─ ledger.Append(Run{state: starting, epoch: n})
                     ├─ start the run's own goroutine ─────────────────────┐
                     │      eino events                                   │
                     │        ├─ agentkit/emit: derive                    │
                     │        │     ├─ archive artifacts → transcript      │
                     │        │     └─ contract.RunEvent → subscribers     │
                     │        └─ ledger.Append(Run{state: …}) on each      │
                     │           transition                               │
                     └─ return a Run handle (not bound to the request) ────┘
                              │
   GET /v2/sessions/{id}/runs ── scan ──▶  the truth (ledger + arbiter)
                              │
   POST /v2/run (same runId) ── attach ─▶  subscribe to the run's events
```

### 2.2 The data flow

The control flow above says who calls whom. This one says **where the data
goes** — and the shape it draws is the proposal's whole point: three sinks,
one owner, one source of truth.

```
  ① events — the model's stream, readable exactly once
  ┌────────────────────────────────────────────┐
  │ run's own goroutine   (agentkit/emit)      │
  └────────────────────────────────────────────┘

                        │
            ┌─────────────┴─────────────┐
            ▼                          ▼
    ② archive artifacts                 ③ contract.RunEvent
            │                           │
            ▼                           ▼
  ┌────────────────────┐      ┌──────────────────────────┐
  │ transcript         │      │ subscribers              │
  │ session.jsonl      │      │ bounded channel, drop    │
  └────────────────────┘      └──────────────────────────┘
            │  ⑤ terminal row               │  ④ render
            │     AFTER the                 ▼
            │     archive             ┌──────────────────────────┐
            ▼                         │ SSE connection           │
  ┌────────────────────────┐          │ (a view only)            │
  │ ledger                 │          └──────────────────────────┘
  │ runs.jsonl             │
  │ the run's own row      │
  └────────────────────────┘
            │  ⑥ scan = the truth
            ▼
  ┌────────────────────────────────────────────┐
  │ GET /v2/sessions/{id}/runs                 │
  │ activeRunId · state · epoch                │
  │ ← the truth, not the stream                │
  └────────────────────────────────────────────┘
                        │  ⑦ only a scan clears busy
                        ▼
              ┌────────────────────┐
              │ frontend           │
              │ (can attach)       │
              └────────────────────┘
```

★ **Three sinks, one owner.** The run's own goroutine is the only thing that
reads the model's stream, and it feeds three sinks: the **transcript** (what
happened), the **subscribers** (whoever is watching) and the **ledger** (what
is true right now). The connection is one window onto the third — not the
thing that owns the output.

★ **The ledger takes two writes.** The run's own row at every transition, and
the **terminal row** (5) — written *after* the archive lands, so "the run is
over" and "the messages are on disk" become the same statement (§3.4). That
one ordering is what makes the busy flag honest without a timeout.

★ **(6) and (7) are the other half of the design.** The truth is a scan of the
ledger, and the client may only clear its busy flag on the word of that scan
(§4.2). The stream is a hint; the ledger is the record.

### 2.3 Four rules

**R1 — A run is a durable object.** It has an identity (the run id), a
lifecycle (a state machine), and a ledger row. It exists whether or not
anybody is watching it, and it survives a restart as a trace.

**R2 — The connection is a subscriber, not the owner.** It may attach, detach,
and re-attach. Its death does not stop the run and does not change what gets
archived.

**R3 — Notification is a hint; the scan is the truth.** The SSE stream may be
late, incomplete, duplicated or lost. Any question about "what is happening"
is answered by scanning the ledger, never by trusting a push.

**R4 — Ownership is a lease, and every durable write carries a fence.** The
lease says who may run; the fence says who may record. Both are arbitrated by
a pluggable driver, so the protocol survives the move from one process to
several.

## 3. D1 — A run is a row of facts

★ **This section is implemented.** `storage/runledger.go` holds the contract,
`storage/filestore/runledger.go` holds the file driver, and
`agentkit/run/runrow.go` writes the rows.

### 3.1 The row

A run's record is a **lifecycle**, not a payload. The transcript already owns
the content and the numbers; the row owns the timing and the ownership.

```go
// storage/runledger.go
package storage

// RunState is where a run is in its lifecycle.
type RunState uint8

const (
    RunStarting        RunState = iota // claimed, not yet executing
    RunRunning                          // executing
    RunWaitingApproval                  // ended by raising an interrupt
    RunCompleted                        // finished normally
    RunFailed                           // finished with an error
    RunAborted                          // stopped by the user
    RunInterrupted                      // the owner vanished mid-run (reconciled)
)

// Run is one row of the run ledger.
//
// ★ It carries lifecycle facts only. Usage, message counts and the transcript
// itself belong to the session's messages — keeping them here would give the
// numbers two homes, and the dashboard would eventually disagree with itself.
type Run struct {
    RunID     string
    SessionID string
    Kind      contract.TurnKind // query or resume
    State     RunState
    Epoch     uint64            // the fence this run holds
    Owner     string            // the instance that claimed it
    // ParentRunID is the run a resume continues. Empty for a query.
    ParentRunID string
    StartedAt   time.Time
    UpdatedAt   time.Time
    EndedAt     time.Time
    ErrorCode    string
    ErrorMessage string
}
```

★ The states are written on the wire as names, not numbers
(`storage/runledger.go` — `RunState.MarshalJSON`). A ledger that outlives the
binary has to stay readable when the constants are renumbered.

### 3.2 The ledger

```go
// storage/runledger.go
//
// ★ Append writes one COMPLETE snapshot of a run, not a delta. The reader
//   takes the last row per run id, and the highest epoch among the open runs
//   is the session's current owner. Both rules are order-tolerant, which is
//   what makes a fenced-out owner harmless: it cannot write itself back into
//   the lead.
type RunLedger interface {
    // Append writes one complete snapshot of a run.
    Append(ctx context.Context, r Run) error
    // List returns one row per run, newest row per run id wins.
    List(ctx context.Context) ([]Run, error)
    // NextEpoch allocates the fence epoch for a new run of this session.
    NextEpoch(ctx context.Context) (uint64, error)
    // Reconcile closes the runs that were still open when the process stopped.
    Reconcile(ctx context.Context) (int, error)
}
```

★ **Why a snapshot per line and not a delta.** A delta log makes every reader
carry the fold. A snapshot log makes every line readable on its own, and makes
a torn last line droppable (the run falls back to its previous state, which is
a state it was really in). The cost is repeated fields — irrelevant at this
size.

★ **Why "highest epoch among open runs wins" and not "last row wins".** The
last row wins *within* one run (`storage.FoldRuns`). *Across* runs, the epoch
decides (`storage.ActiveRun`): a zombie owner from epoch 3 that wakes up after
epoch 4 took over writes a row, but the reader still sees epoch 4 as the
owner. This is the fence doing its work on the read side, where it is cheap
and needs no lock.

★ **The epoch is allocated by the ledger, not by the arbiter.** The epoch has
to be monotonic across restarts, so it cannot live in a volatile structure —
and the ledger is the durable thing that outlives the process. §5's lease
carries the epoch the ledger handed out; the lease itself is about liveness,
the epoch is about order. Two homes for one number would be two truths.

### 3.3 Where the row lives

Next to the session's other files, in the session directory
(`storage/filestore/session.go` defines the current names):

```
<root>/<sessionId>/session.jsonl     the transcript (append-only)
<root>/<sessionId>/state.json        the session state (rewritten atomically)
<root>/<sessionId>/runs.jsonl        the run ledger (append-only)
```

★ **`runs.jsonl` is not `state.json`.** `state.json` is rewritten whole on
every change; a growing list of runs inside it would mean rewriting the whole
history of the session on every transition. An append-only ledger matches the
transcript's shape, reuses the same append machinery, and makes the ledger's
own write the durability barrier.

★ **The ledger is bounded.** A ledger that grows forever is a leak. The file
driver trims to the last N rows (N = 200, `storage/filestore/runledger.go` —
`maxRunLedgerRows`) with the same atomic rewrite `state.json` uses, keeping
the file append-only in the common case and bounded in the worst.

### 3.4 The row is the archive barrier

★ **The terminal row is written after the archive lands.** That single
ordering gives the busy flag its honesty for free:

```
run goroutine:  … archive → transcript  →  ledger.Append(state: completed)
scan:           sees "completed" ⟹ the messages are on disk
```

★ The implementation is `rt.finishRun` (`agentkit/run/loop.go:145`), called
from the archive site (`agentkit/run/loop.go:130`). One exception: a run that
ended by raising an interrupt waits for the loop's exit, because that is where
the approval and the checkpoint are written
(`agentkit/run/bridge.go:152`) — a scan must not be able to call the run over
before the question the user has to answer is on disk.

★ **The terminal state is the run's own truth, not the absence of an error.**
The error the consumer saw is carried into the outcome
(`api/v2_run.go` — `Outcome.Err`), so a run whose model timed out reads
`failed` and a run the user stopped reads `aborted`. Without that, the ledger
would be the one place that lies: it would call a turn that died mid-flight
`completed`. A cancelled turn is also **not** the session's last error — the
user stopping a run is not a failure of the session.

Today the same guarantee costs a 30-second wait channel
(`agentkit/run/turn.go:156-165`, `agentkit/run/turn.go:173`), because the
archive is downstream of the connection. With the row in place, the wait
disappears: *the row is the barrier*, and there is nothing to time out.

## 4. D2 — Notification is a hint, the scan is the truth

### 4.1 The server side

★ **This half is implemented.** The SSE stream keeps its shape — it is still
AG-UI frames over one HTTP response. What changes is its authority:

- The run's goroutine writes the terminal row **whether or not anybody is
  subscribed**. The stream's end is not the run's end.
- `GET /v2/sessions/{threadId}/runs` scans the ledger and answers "what
  happened, what is happening" (`api/v2_sessions.go:423`).
- `GET /v2/sessions/{threadId}` reports `state` from the scan, not from the
  volatile map: `stateOf` (`api/v2_sessions.go:506`) asks `StatusOf`
  (`agentkit/run/runtime.go:711`), and `StatusOf` asks the ledger
  (`agentkit/run/runtime.go:749` — `activeRun`).

★ **The volatile map became a cache.** `sessionState`
(`agentkit/run/bridge.go:30-39`) stays — it holds the live loop and the live
turn, which are genuinely in-memory things — but it is no longer the *truth*.
`StatusOf` mixes a volatile source (the map's timestamps) with durable ones
(the pending approval from the session file, the active run from the ledger);
the durable side now decides the phase.

### 4.2 The client side

★ **The busy flag becomes a scanned fact, not a stream-derived inference.**
Today the client infers "the session is free" from the stream ending. Under
the new contract:

| Today | Proposed |
|---|---|
| stream ends → `onDone` → assume idle (`web/src/lib/store/actions.ts:271-275`) | stream ends → **re-scan** → decide |
| `stop()` sets `busy: false` optimistically (`web/src/lib/store/actions.ts:339`) | `stop()` → abort → **re-scan** → decide |
| 409 `session_busy` → `markBusy` (`web/src/lib/store/actions.ts:464-485`) | unchanged — but the message is backed by a scanned fact |
| page load → `seedFromHistory` reads `state` (`web/src/lib/store/history.ts:100-112`) | unchanged — now a scan of the ledger |

The rule is one sentence: **the client may only clear `busy` on the word of a
scan.** A stream that ends without a scan leaves the flag as it was.

★ Why this matters more than it looks: the current client can be wrong in the
**dangerous direction**. If the server is still finalizing a run and the client
assumes "idle", the next message is sent into a session the server considers
busy — and the previous round had to paper over exactly that with the archive
wait. With the scan as the only way to clear the flag, the class of bug
disappears instead of being timed around.

### 4.3 What the scan answers

★ **Implemented**, and this is the wire shape (`api/v2_types.go` — `runsView`):

```
GET /v2/sessions/{threadId}/runs
{
  "items": [
    {
      "runId": "…", "state": "completed", "kind": "query",
      "epoch": 7, "startedAt": "…", "endedAt": "…"
    }
  ],
  "activeRunId": "…"      // the run that currently owns the session, if any
}
```

★ `error` appears only when the run failed, and `activeRunId` only when there
is an active run — an idle session scans as `{"items": []}`. A field that is
always there and always empty is noise a client has to learn to ignore.

`activeRunId` is the one field the UI needs most: it is what makes a run
**attachable** (§8.1) and what replaces the empty `runId` that
`GET /v2/sessions/{threadId}` used to return (`api/v2_sessions.go:94` — it
returns the active run's id now).

## 5. D3 — Lease and fence instead of first-come-first-served

★ **Implemented.** The epoch was already real (§3.2); now the lease is too.

### 5.1 Why the mutex is not enough

The mutex in `claimTurn` (`agentkit/run/runtime.go:207-241`) answers "who got
here first". It does not answer "who is still alive". Three failures follow:

1. **A stalled owner.** The owner holds the lock but is not progressing — a
   hung model call, a GC pause, a bug in the loop. Nobody else can take over;
   the session is stuck, and the only way out is `Abort`.
2. **A zombie owner.** The owner was paused (VM freeze, laptop sleep, a
   debugger breakpoint) and wakes up later. Its `loopExited`
   (`agentkit/run/bridge.go:115-143`) will clear state it no longer owns —
   today that is caught by a pointer-identity check, which is a fence that
   happens to be free.
3. **A second process.** The moment two openheron processes can see the same
   session directory, the mutex protects nothing: it is a lock in one
   address space guarding a resource in another.

★ **A stall comes in two kinds, and they do not have the same remedy.** A
**process-level** stall — a long GC pause, a frozen VM, a suspended laptop —
stops everything, renewal included, so the lease simply expires. A
**run-level** stall — a hung model call, a bug in the loop, a write that never
returns — stops only the run's own work, and a renewal that ticks on a timer
cannot see it: the ticker does not care what the run is doing.

★ So the fence fixes 2, the arbiter fixes 3, and 1 needs **progress-driven
renewal** (§5.2) on top of the lease. A timer-driven lease would make the
process-level half work and leave the run-level half exactly as stuck as it
is today.

### 5.2 The lease

```go
// storage/lease.go
package storage

// Lease is a time-bounded claim on a session.
//
// ★ Epoch is the fence: it is monotonically increasing per session, and every
//   durable write the holder makes carries it. A holder whose epoch is behind
//   the session's current epoch has been fenced out and must not write.
type Lease struct {
    RunID     string
    SessionID string
    Epoch     uint64
    Owner     string
    ExpiresAt time.Time
}

// LeaseArbiter decides who owns a session.
//
// ★ The arbiter does NOT allocate the epoch. The epoch has to be monotonic
//   across restarts, so it cannot live in a volatile structure — the ledger
//   allocates it (RunLedger.NextEpoch) and the lease carries the number it
//   was given. The arbiter's job is liveness: who holds the claim, and
//   until when. Keeping the two apart is what lets the ledger's read rule
//   stay simple: "highest epoch wins" needs no knowledge of liveness, and
//   "the lease expired" needs no knowledge of write order.
type LeaseArbiter interface {
    // Claim acquires the session's lease. It fails with ErrLeaseHeld when
    // another run holds a live lease, and succeeds when the lease is expired
    // or is already ours.
    Claim(ctx context.Context, sessionID, runID, owner string, epoch uint64, ttl time.Duration) (Lease, error)
    // Renew extends a lease the caller still holds.
    Renew(ctx context.Context, l Lease, ttl time.Duration) (Lease, error)
    // Release gives the lease back.
    Release(ctx context.Context, l Lease) error
    // Fence reports the session's current epoch.
    Fence(ctx context.Context, sessionID string) (uint64, error)
    // Held reports the session's current lease, if any.
    Held(ctx context.Context, sessionID string) (Lease, bool)
}
```

★ **The lease is what makes the gate say "stalled" instead of "busy".** The
admission gate asks whether the session is busy; the answer used to come from
the turn slot — "a turn is live" — and a stalled owner keeps its slot for ever.
Now the gate asks the arbiter as well: an owner whose lease ran out is not
holding the session any more, and the run may take it over
(`agentkit/run/runtime.go` — `leaseExpired`). The epoch says which of the two
is the owner from now on.

★ **The arbiter is the in-memory one** (`storage/memarbit`). It arbitrates
inside one process, and nothing survives a restart — the ledger's reconciler
closes the runs a crash left open (§3.3), and the next run allocates a fresh
epoch.

★ **The run renews — on progress, not on a timer.** The lease is renewed when
the run **reports progress**: the run publishes its events, and every publish
is a progress report. The TTL (10 minutes by default,
`agentkit/run/lease.go` — `DefaultLeaseTTL`) is the **budget**, not the
schedule: a run that has reported nothing for TTL loses the lease, however
alive it is. The renewal is throttled to a quarter of the TTL: a streaming
model reports progress on every token, and a renewal per token would be a
lock per token for nothing.

★ **The TTL is a configuration item** (`server.lease_ttl`, 10m by default).
It has to be **longer than the provider's own timeout** — the provider's
timeout ends a call that is hung, and the lease only has to outlast a call
that is slow — but the two live in different files (`llm/<id>.yaml` per
endpoint), so the relation is the operator's to keep, not the code's to
enforce. `server/` is not hot-reloaded: a change needs a restart.

★ **Why not a timer.** A timer-driven renewal keeps the lease alive for a run
stuck in a hung model call, because the renewal goroutine does not care what
the run is doing — it keeps saying "still here". That is precisely the
failure §5.1 calls a stalled owner, and a timer cannot see it.
Progress-driven renewal can: no progress, no renewal, the lease expires, and
the session becomes available to somebody else.

★ **A slow model is not a stalled run — and the TTL has to know the
difference.** A model that takes four minutes to answer reports no progress
to renew on, so a 30-second TTL would fence it out while it is working
perfectly. The remedy is a **TTL longer than the provider's own timeout**,
not a timer that lies: the provider's timeout
(`initcfg/templates/llm/_generic.yaml:10`, 5m by default) ends a call that
is hung, and the TTL only has to outlast a call that is **slow but alive**.
Both are configuration.

★ **The lease covers a run, not a session's idle state.** A run that ends by
raising an interrupt releases its lease — the run is over. What keeps the
session from accepting a new query is the **pending approval**, which is a
durable session-level fact rather than a lease
(`agentkit/run/runtime.go:348-353`). Two locks, two scopes: the lease says
"this run owns the session right now", the approval says "this session owes
the user an answer".

★ **Losing the lease fences the run out.** If a renewal fails because
somebody else holds the lease, the run cancels its own context and stops. It
does not keep writing "running" rows for a session it no longer owns — the
fence makes those writes harmless, but stopping is better.

### 5.3 The fence

The fence is a per-session, monotonically increasing integer. It answers one
question: **is this write from the current owner?**

It is enforced in two places, and nowhere else:

1. **On read** (§3.2): among the open runs, the one with the highest epoch is
   the owner. A stale row cannot take the lead.
2. **On write** (optional but recommended): `ledger.Append` refuses a row
   whose epoch is below the session's current epoch.

★ **The fence's job is ordering, not durability.** A crashed process's rows
are still on disk; what tells you they are stale is the *lease*, not the
epoch. Keeping the two separate is what makes the ledger's read rule simple:
"highest epoch wins" needs no knowledge of liveness, and "the lease expired"
needs no knowledge of write order.

### 5.4 Lease and fence do different jobs

| | Lease | Fence |
|---|---|---|
| Question | Who may run right now? | Who may record this fact? |
| Lifetime | Time-bounded, renewed on progress | Monotonic, never reused |
| Failure mode it prevents | A stalled owner holds the session forever | A zombie owner corrupts the record |
| Enforced by | The arbiter (renewal, expiry) | The epoch comparison (read and write) |

★ **What the lease detects is "the owner stopped reporting", not "the owner
stopped thinking".** With progress-driven renewal the two kinds of stall
separate cleanly: a process that froze reports nothing and loses the lease; a
model that is slow but alive also reports nothing — and is protected by a TTL
longer than the provider's timeout, not by the lease. The lease answers "may
I take this session over", so it has to be able to say yes without a human.

★ The current code has the fence and not the lease: the epoch is real
(`storage.RunLedger.NextEpoch` allocates it, `storage.ActiveRun` compares it),
but ownership is still decided by a mutex. The pointer-identity checks in
`markTurnDone` (`agentkit/run/bridge.go:51`) and `loopExited`
(`agentkit/run/bridge.go:119-124`) are fences over addresses — they work
inside one process and nowhere else. The lease replaces them.

## 6. D4 — The driver is pluggable

### 6.1 The interfaces live in `storage`

`storage` (L1 in [architecture.md](architecture.md)'s layering table) is
already the package that owns session and pending-approval interfaces
(`storage/storage.go`). The run ledger and the lease arbiter belong
there too, for the same reason: `agentkit/run` (L2) must depend on interfaces
only, and the API layer (L3) must not assemble business logic
(AGENTS.md's iron rule).

★ **The ledger half is implemented.** The lease half is not.

```go
// storage — interfaces and value types
type RunLedger interface { … }      // storage/runledger.go   ← implemented
type LeaseArbiter interface { … }   // storage/lease.go       ← not there yet
type Run struct { … }               // storage/runledger.go   ← implemented
type Lease struct { … }             // storage/lease.go       ← not there yet

var ErrLeaseHeld  = errors.New("lease held by another run")
var ErrFenceStale = errors.New("the run's fence is stale")   ← storage/runledger.go
```

★ `ErrFenceStale` is already defined, next to the ledger's contract, because
the fence is already a fact in the ledger: the epoch is allocated there and
compared there. The error is what the *optional* write-side check will
return once the arbiter exists.

### 6.2 The drivers, in the order they arrive

| Order | Driver | Ledger | Arbiter | What it buys |
|---|---|---|---|---|
| 1 | `storage/memarbit` | in-memory | in-memory | The protocol, exercised, single process. Nothing survives a restart. |
| 2 | `storage/filestore` | `runs.jsonl` | in-memory | Durability: a crash leaves a trace, and the reconciler can close it. |
| 3 | a database driver | a table | a table, with the epoch allocated by `UPDATE … SET epoch = epoch + 1` | Several processes over one session store. |

★ **The file driver's ledger is implemented** (`storage/filestore/runledger.go`),
so the middle row is half true today: the ledger is the file, the arbiter does
not exist yet. The epoch comes from the ledger, not from the arbiter — §3.2
explains why, and the database row would have to hand it out the same way.

★ **The honest reading of "memory first, database later".** This repository has
no database at all — the whole store is files (`storage/filestore`). Introducing
a DB driver is not a "later" tweak; it is a new dependency and a new
deployment story. The middle step — **files** — is nearly free here: the
transcript is already an append-only JSONL file, and `persist` already does
atomic rewrites and file locks.

★ So the practical order is **memory → file → database**, and the memory driver
is not a throwaway: it is the one that proves the *protocol* (lease, fence,
reconcile, subscribe) without any IO. The file driver then adds durability
with one new file per session.

### 6.3 What the memory driver costs

If the ledger is only in memory, then:

- A crash loses the run record → C2 is not fixed. The reconciler has nothing
  to reconcile.
- A restart cannot tell "was running" from "never ran" → the sidebar dot lies
  again, exactly as it did before the previous round.

★ That is why the file driver is not optional in the proposal. "Memory first"
is the right order for the *protocol* and the wrong one for the *ledger*. The
proposal keeps them apart: the **ledger** ships durable (file), the
**arbiter** ships in memory and is the thing that becomes a database row
later.

## 7. The event pipeline: one consumer becomes publish/subscribe

★ **This section is a proposal.** Nothing of it is in the tree.

### 7.1 Why the old pipeline forced the coupling

The transport's `streamEvents` did two jobs at once:

- it **rendered** AG-UI frames (`e.emit…`), and
- it **derived** the archive payload — the intermediates, the interrupts, the
  compaction, the usage.

★ The derivation cannot be done twice: the streaming branch calls
`mo.MessageStream.Recv()` on the model's stream and folds the chunks into one
message. **A second reader would split the chunks.** So the renderer was
necessarily the single consumer — and since the renderer was the SSE sink, the
sink owned the run's output. A dead connection meant a turn that never
archived.

### 7.2 The split

★ **Implemented.** Two responsibilities, two packages:

| Before | Now |
|---|---|
| `transport` — derive + render, in one pass | `agentkit/emit` — derive only: eino event → archive artifacts + `contract.RunEvent` |
| `transport` — the AG-UI emitter | `transport` — render only: `contract.RunEvent` → AG-UI frames → SSE |

`agentkit/emit` is an L1 package: it depends on `contract`, `message`,
`approval`, `helpers` and `obs`, and nothing above it. `agentkit/run` calls
it; `transport` does not need to — the transport receives `contract.RunEvent`s
and renders them.

★ The wire did not change: the frames are the same, one for one. What changed
is who derives them. The transport's own contract tests
(`transport/transport_test.go`) still assert the frame sequences, and they
drive the derivation and the rendering together — the split must not be
visible on the wire.

### 7.3 The run's event vocabulary

```go
// contract/runevent.go
package contract

// RunEventKind is what happened in a run.
type RunEventKind uint8

const (
    RunEventTextStart RunEventKind = iota
    RunEventTextDelta
    RunEventTextEnd
    RunEventReasoningStart
    RunEventReasoningDelta
    RunEventReasoningEnd
    RunEventToolStart
    RunEventToolArgs
    RunEventToolEnd
    RunEventToolResult
    RunEventAnnounce    // a message that produced no visible delta
    RunEventCompaction
    RunEventContextUsage
    RunEventInterrupt
    RunEventRetry       // the provider is retrying
    RunEventError
)

// RunEvent is one thing that happened in a run.
//
// ★ It is openheron's own vocabulary, not AG-UI's. The run must not depend on
//   the wire format: the transport maps RunEvent onto AG-UI frames, and a
//   second wire format would be a second renderer, not a second run.
type RunEvent struct {
    Kind RunEventKind
    RunID string
    MsgID string
    Text  string
    Role  string
    Owner string
    ToolID string
    ToolName string
    Sub  string
    Agent string
    At   time.Time
    Usage ContextUsage
    Interrupts []Interrupt
    Pending []PendingInterrupt
    Code string
}
```

★ There is no `RunEventMessage` and no `RunEventDone`. The archived messages
are the derivation's **artifacts** — they come back with the derivation, not
through the event stream, because they are facts about the run rather than
things that happened in front of a watcher. And "done" is not an event: it is
the **closing of the subscription's channel** (§7.4).

★ Why not reuse AG-UI's event types directly: `contract` is an L0 leaf
([architecture.md](architecture.md)'s layering table) and AG-UI is a transport
concern. Putting AG-UI types in `contract` would make the domain depend on the
wire. The mapping is one-to-one in practice, which keeps `transport` thin.

### 7.4 The subscriber

★ **Implemented.** The run publishes to its subscribers; the subscriber is the
SSE connection.

```go
// agentkit/run/publish.go
type publisher struct {
    mu     sync.Mutex
    window []contract.RunEvent              // a BOUNDED window, not the turn's history
    subs   map[*subscription]struct{}
    closed bool
}
```

- A subscriber that is slow gets a **bounded** channel; when it overflows, the
  run **drops the subscriber**. A slow reader must never slow the run — that
  is the old "the sink blocks the loop" trap.
- ★ **A subscriber that is *stuck* is the case that matters, and it is not the
  same as one that is slow.** A slow reader drains its channel eventually. A
  stuck one has a socket write that never returns: the peer vanished without
  a reset, and the kernel keeps accepting bytes into a buffer nobody reads.
  The publisher must therefore **never block on a full channel** — it drops
  the subscriber instead of waiting for it, and the run carries on. Before the
  split the same situation stalled the run, because the run was the one
  writing to the socket.
- A subscriber that disappears takes nothing with it. The run keeps going and
  keeps archiving.
- ★ **The run's event log is a bounded window, not the turn's history.** A
  client that attaches mid-run gets the events from the window, so it can
  render the tail of the turn without waiting for the turn to end. The window
  holds the last N events (N = 1000, `agentkit/run/publish.go` —
  `windowSize`) and drops the oldest, so a long turn costs a **fixed** amount of
  memory rather than one that grows with the output. The run drops the window
  when it ends.

★ **The window's replay and the registration happen under one lock**, so a
subscriber sees every event exactly once: the ones from the window, then the
ones that follow.

★ **The run closes its subscription when the RUN is over — and a run that ended
by raising an interrupt is not over until the approval is on disk.** This is
not a detail: the client learns about the interrupt from the closing channel
(the renderer writes `RUN_FINISHED` when the channel closes), so a channel
that closed before the approval was persisted would invite the client to
answer an approval the server cannot find yet — and the answer would be
refused with `no_pending_interrupt`, for no reason the user can see. The
approval is written when the loop exits (the checkpoint it resumes from must
exist first), so that run's publisher closes there instead
(`agentkit/run/bridge.go`).

★ **Two jobs, two stores.** "Coming back later" is the **transcript's** job: a
client that opens a session hydrates from `GET /v2/sessions/{id}/messages` and
sees the **finished** turn (`web/src/lib/store/actions.ts:86` →
`seedFromHistory`). The log serves only "attaching while it is still running"
— a live view, never a final one.

★ **So the window is filled whether or not anyone is watching**, and there is
no branch to decide. Its size is a bound, so an unwatched turn pays a
**constant** cost for a log nobody reads — while the run's real output, the
transcript, was going to be written either way. The alternative, building the
window only once somebody attaches, would mean guessing — and it would miss
the case that matters most: a client that attaches halfway through.

★ **Persisting it is a separate decision** — see §13 Q1. In memory for now.

### 7.5 What is deliberately not built

- **No multi-subscriber protocol.** Two browsers may both attach to a run
  (that is just two subscribers), but the run does not arbitrate between them
  beyond the bounded channel.
- **No persisted event log.** See §13 Q1.
- **No change to the AG-UI wire format.** The frames are the same; only their
  producer changes. The transport's contract tests assert the sequences.

## 8. The API surface

### 8.1 Changed routes

**`POST /v2/run`** — three cases, decided by the `runId` the client sends:

| The session has | The request carries | Result |
|---|---|---|
| no live run | a `runId` (or none — the server mints one) | **create** the run; the response streams it |
| a live run with **that** id | the same `runId`, **and no question** | **attach**: subscribe to the live run, replaying its window |
| a live run with a **different** id | any `runId` | **409 `session_busy`** — unchanged from today |

★ The first row is **implemented**: the server mints the id when the client
sends none (`api/v2_run.go`), and hands it down to the runtime
(`api/v2_run.go:218`) so the id in the frames and the id in the ledger's rows
are one id.

★ The second row is **implemented** (server side). The request that asks for
it carries **no question**: a question would mean "start a new run", and
starting one here would silently run the user's message a second time. The
server looks the live run up by id (`agentkit/run/runtime.go` — `LiveRun`),
subscribes to it and renders its window — so a client that arrives late sees
the tail of the turn instead of an empty stream.

★ **A run that is over cannot be attached to.** The client has to hydrate from
the transcript; the run's own facts are in the scan. The server refuses with
**404 `run_not_live`** rather than starting a new run — the client's answer to
that refusal is "load the history", and it must be able to tell it apart from
a session it may retry.

★ The second row is the new capability: a reconnect. Today the client cannot
resume a stream it lost (`web/src/lib/store/actions.ts:250-254` sends no
`runId` at all), so a dropped connection loses the turn's output. With the
run id known, the client asks for the same run and gets the rest of it.

★ The client must therefore **keep the run id**. The scan returns it
(`activeRunId`), and the SSE frames already carry it (AG-UI's `runId`).

**`GET /v2/sessions/{threadId}`** — `state` comes from the scan. ★
**Implemented**: `stateOf` (`api/v2_sessions.go:506`) asks `StatusOf`, and
`StatusOf` reads the ledger. The `runId` that `api/v2_sessions.go:94` returns
is no longer empty — it is the active run's id, the same value the scan
reports as `activeRunId`. ★ **Not** a second field: one value, one name.

**`POST /v2/sessions/{threadId}/abort`** — aborts the **run**, not the loop.
It cancels the run's context, waits (bounded) for the terminal row, and
returns the run id it aborted. ★ The run id is **implemented**
(`api/v2_sessions.go:405`). The rest is a proposal: the current implementation
(`api/v2_sessions.go:385` → `agentkit/run/runtime.go:565`) manipulates the
loop directly; under the proposal the run's own cleanup writes the row, so
the abort is "stop this run" and the row follows.

### 8.2 New routes

★ **Implemented.**

```
GET /v2/sessions/{threadId}/runs              the scan: every run of the session
GET /v2/sessions/{threadId}/runs/{runId}      one run
```

Both read the ledger (`api/v2_sessions.go:423`). Neither ever writes.

### 8.3 The error envelope

No new codes. The existing ones carry the meaning:

| Code | Where it is produced | Meaning |
|---|---|---|
| `session_busy` | `api/v2_types.go:48` | another run owns the session |
| `run_not_live` | `api/v2_types.go` | the attach asked for a run that is not running |
| `no_pending_interrupt` | `api/v2_types.go:36` | the approval was already answered |
| `turn_superseded` | `api/v2_types.go:49` | kept for compatibility; the superseding path is gone |
| `turn_start_timeout` | `api/v2_types.go:50` | the run never became streamable |

★ `turn_superseded` stays because removing it would break clients that branch
on it — but under the proposal it is only ever produced by the start-timeout
path's sibling, never by a takeover. The concurrency design's §6.3 already noted
this; the proposal does not change it.

★ `run_not_live` is the one new code, and it exists because the attach needs
one. "The run you asked for is gone" is neither "the session is busy" nor "no
such run": the client's answer to it is "load the history", and it has to be
able to tell that apart from a session it may retry.

## 9. The frontend

★ **The re-attach is implemented.** The busy flag's own rule (§9.1) is still a
proposal: the server tells the truth, and the client does not ask it yet.

### 9.1 The busy flag becomes a scanned fact

`ConversationState.busy` (`web/src/lib/types.ts:123-127`) means "the session
has a live turn that this surface does not own". Under the proposal it is
**set and cleared only by a scan**:

- **Set**: the scan reports an `activeRunId` that this surface does not own.
- **Cleared**: the scan reports no active run.

★ `stop()` (`web/src/lib/store/actions.ts:318-351`) changes shape: it sends the
abort, then **re-scans**, instead of setting `busy: false` itself
(`web/src/lib/store/actions.ts:339`). Setting it optimistically is exactly the
"trust the notification" mistake the design removes.

### 9.2 Attach

★ **Implemented.** The store keeps the run id of the run it started (the
`RUN_STARTED` frame carries it, and the reducer keeps it). When the stream
ends **without a terminal frame**, the connection died — not the run — and the
client asks the scan whether that run is still live:

- live → re-`POST /v2/run` with the same `runId` and **no question** →
  **attach**, and the rest of the turn renders.
- not live → the run is over; the transcript is the truth; load it.

★ **A stream that ends without a terminal frame is the signal.** `RUN_FINISHED`
means the run is over and `RUN_ERROR` means it broke; a stream that ends with
neither means the connection died. That is a protocol-level fact, and it is
the only one the client needs.

★ This is the visible payoff of the whole design: **a second tab can watch a
running turn instead of being refused**, and a dropped connection can be
resumed instead of losing the output.

### 9.3 The store

The store's shape barely changes — the reducer stays a pure function, the
selectors stay reference-stable (AGENTS.md's rules). What changes is the
**source** of two values:

| Value | Today | Proposed |
|---|---|---|
| `busy` | inferred from the stream's end and from 409s | scanned |
| the run id | ✓ kept (`web/src/lib/events.ts` — the reducer) and sent on re-attach | kept, and sent on re-attach |

## 10. What changes where

★ **✓ = landed.** The rest is still a proposal.

| File | Change |
|---|---|
| `storage/runledger.go` | ✓ **new** — `Run`, `RunState`, `RunLedger`, `FoldRuns`, `ActiveRun` |
| `storage/lease.go` | ✓ **new** — `Lease`, `LeaseArbiter`, `ErrLeaseHeld` |
| `storage/memarbit/arbiter.go` | ✓ **new** — the in-memory arbiter |
| `storage/filestore/runledger.go` | ✓ **new** — the `runs.jsonl` driver |
| `storage/filestore/session.go` | ✓ the session directory gains `runs.jsonl`; the `FileSession` gains a ledger handle |
| `storage/filestore/manager.go` | ✓ the startup pass reconciles the open runs |
| `agentkit/emit/` | ✓ **new** — the derivation split out of `transport` |
| `contract/runevent.go` | ✓ **new** — `RunEvent`, `RunEventKind` |
| `agentkit/run/runtime.go` | ✓ `Submit`/`Resume` carry the run id; `claimTurn` opens the row; `StatusOf` reads the scan. **Later:** `claimTurn` becomes a lease claim |
| `agentkit/run/publish.go` | ✓ **new** — the publisher, the subscriptions and the window |
| `agentkit/run/turn.go` | ✓ `RunID` is assigned; the turn closes its own row. **Later:** `Turn` becomes a `Run` with subscribers |
| `agentkit/run/loop.go` | ✓ the terminal row is written after the archive; the run derives and archives its own events. **Later:** the lease |
| `agentkit/run/bridge.go` | ✓ the loop's exit closes the row and the publisher; `sessionState` becomes a cache |
| `agentkit/run/lease.go` | ✓ **new** — the run's lease, the progress-driven renewal |
| `agentkit/run/errors.go` | ✓ `ErrSessionBusy` stays; the lease errors map onto it |
| `transport/transport.go` | ✓ becomes a renderer: `contract.RunEvent` → AG-UI frames |
| `api/v2_run.go` | ✓ the run id is minted once and handed down; the handler subscribes instead of draining. **Later:** attach |
| `api/v2_sessions.go` | ✓ `state` comes from the scan; the two scan routes; the abort names the run it stopped |
| `api/v2_files.go` | ✓ the two new routes |
| `web/src/lib/store/actions.ts` | ✓ keep the run id; ✓ re-attach after a dropped stream; re-scan instead of assuming |
| `web/src/lib/store/history.ts` | `busy` from the scan |
| `web/src/lib/api.ts` | ✓ `runId` on the run request; ✓ the runs scan |
| `web/src/lib/events.ts` | ✓ the reducer keeps the run id |
| `web/src/lib/types.ts` | `activeRunId` on the session meta |

## 11. Testing

★ **✓ = the test exists.** The rest is the plan.

**Go — the ledger** (`storage/filestore`, `storage/filestore/runledger_test.go`):
- ✓ a run's row survives a restart of the store;
- ✓ the last row per run id wins;
- ✓ among open runs, the highest epoch is the owner, even when the lower-epoch
  run wrote later;
- ✓ a torn last line is dropped and the run falls back to its previous state;
- ✓ the epoch never repeats, not even across a restart;
- ✓ the ledger stays within its bound.

**Go — the arbiter** (`storage/memarbit/arbiter_test.go`):
- ✓ a second claim on a live lease fails with `ErrLeaseHeld`;
- ✓ an expired lease can be claimed, and the new epoch is higher;
- ✓ a renewal by a fenced-out holder fails;
- ✓ a run that **reports progress** keeps its lease;
- ✓ a run that reports **nothing** loses the lease even while it is alive — the
  run-level stall §5.1 describes;
- ✓ a stale release does not free the new holder's lease;
- ✓ the fence reports the holder's epoch.

**Go — the runtime** (`agentkit/run/lease_test.go`):
- ✓ a live owner holds the session: a second run is refused;
- ✓ **a stalled owner does not hold the session for ever** — the lease is a
  budget, and the gate asks it;
- ✓ a run that reports progress keeps its lease.

**Go — the runtime** (`agentkit/run`, `agentkit/run/runrow_test.go`,
`agentkit/run/publish_test.go`):
- ✓ a run with **no subscriber attached** still archives — the direct test of
  "a turn outlives its connection";
- ✓ a subscriber that vanishes does not stop the run;
- ✓ a subscriber whose queue overflows is **dropped** and the run keeps
  archiving — the stuck subscriber of §7.4;
- ✓ the run's event window stays **within its bound** — the cost of an unwatched
  turn is bounded;
- ✓ a late subscriber gets the window replayed, then the live events;
- ✓ the run is recorded before it is handed out;
- ✓ the terminal row lands after the archive (the transcript already holds the
  answer when the row says `completed`);
- ✓ a turn that never starts closes its own row;
- ✓ the scan names the active run, and stops naming it once the run is over;
- ✓ a resume names the run it answers;
- ✓ `StatusOf` reports the active run's id;
- the run archives its messages **with no subscriber attached** — the direct
  test of "a turn outlives its connection";
- a subscriber that vanishes does not stop the run;
- **a subscriber whose channel overflows is dropped and the run keeps
  archiving** — the stuck subscriber of §7.4, where the socket write never
  returns;
- a run whose handler dies still writes its terminal row (the C3 case);
- a run that loses its lease stops and does not write a `running` row;
- a run nobody attached to keeps its event log **within the window's size** —
  the cost of an unwatched turn is bounded (§7.4).

**Go — the reconciler** (`storage/filestore/runledger_test.go`):
- ✓ a run left `running` by a crash is closed as `interrupted` at startup,
  before the server accepts requests (the hook already exists:
  `storage/storage.go` — `SessionManager.Inspect`).

**Go — the API** (`api/v2_runs_test.go`):
- ✓ the scan lists the runs and names the active one;
- ✓ the session's own route carries the active run's id;
- ✓ one run by id, and 404 for an unknown one;
- ✓ an idle session scans as an empty list.

**Frontend — unit**:
- `busy` is cleared only by a scan, not by a stream's end;
- `stop()` re-scans;
- a re-attach keeps the run id.

**E2E** (the fake backend, `web/test/e2e/`):
- ✓ a dropped connection re-attaches and receives the rest of the turn
  (`web/test/e2e/reattach.spec.ts`);
- ✓ the re-attach carries the run's id and no question;
- a second tab attaches to a running turn and sees its output;
- a 409 still rolls back the optimistic bubble (the previous round's
  behaviour must survive the change).

## 12. Staging

**Stage 1 — the run becomes a fact.** ✓ **landed.** The ledger, the row, the
scan route, the reconciler. The SSE still drives the run, but the run's **row**
is written independently of the connection, so a crash leaves a trace. This
stage fixes C1 and C2 and gives the frontend something true to scan.

**Stage 2 — the run owns its consumption.** ✓ **landed.** The
`agentkit/emit` split, the subscribers, the run's event window, and the
**attach**. The run reads its own events, derives what they mean, and archives
the result; the connection subscribes to what the run publishes, and a client
that lost its connection comes back with the run's id and gets the rest of the
turn. This fixes C3 and C4 — the coupling itself. It is also the stage where a
**stuck subscriber stops being a scenario**: the run owns no socket, so a
wedged connection cannot reach the run at all.

**Stage 3 — the lease replaces the mutex.** ✓ **landed.** The arbiter, the
**progress-driven renewal**, the fence. The gate's behaviour is unchanged (still
"one session, one turn", still 409); only its internals move. This stage fixes
**both** kinds of stalled owner — the process-level one because the lease
expires when the process stops reporting, the run-level one because the
renewal is driven by progress — and it prepares the ground for a second
process.

★ **Stage 3 can come only after Stage 2, and the reason is the renewal.** A
progress-driven renewal needs the run to see its own progress, and before
Stage 2 the run saw nothing: the transport drained the events and handed back
the archive payload. A renewal driven by the connection's progress would
expire the lease of a run whose connection died — exactly the coupling the
lease is meant to remove.

★ **The order changed while Stage 1 was being built, and the reason is worth
writing down.** The original plan put the lease second. It cannot go second:
**progress-driven renewal needs the run to see its own progress**, and today
the run does not see the events — the transport drains them and hands back the
archive payload (`agentkit/run/loop.go:115`). A renewal driven by the
connection's progress would expire the lease of a run whose connection died,
which is exactly the coupling the lease is meant to remove. So the pipeline has
to be split before the lease can be honest.

★ The upside of the new order: **Stage 1 depends on neither.** It is a fact
about the run, written by the run's own goroutine, and it is the stage that
makes the other two possible — the lease needs a durable epoch, and the epoch
is the ledger's.

★ **Stage 2 is the expensive one** and it is the only one that truly unbinds
the turn from the connection. Stages 1 and 3 are worth doing on their own:
Stage 1 makes the run observable, Stage 3 makes its ownership bounded — a much
better place to be than today.

## 13. Open questions

**Q1 — Should the run's event log be persisted, and how big is the window?**
A third file per session (`events.jsonl`) would let a client that reconnects
hours later replay the deltas of a turn, not just its final transcript. The
transcript already gives a correct final view, so this is a nicety. Cost: a
third file, a third trim policy, a third thing to corrupt.

★ The second half of the question matters more than the first, and it has
**nothing to do with persistence**: **the size of the window** (§7.4). A log that
grows with the turn is a memory leak that only shows up under load, so the
size is a **bound** rather than a default — the same kind of bound the ledger
already has (§3.3).

**Q2 — Is a second process in scope?**
The lease and fence are designed for it, but the memory arbiter cannot enforce
anything across processes — it is a protocol, not a lock. The question is
whether the DB driver is a real requirement or a preparation.

**Q3 — Who may answer an approval?**
A pending approval is session-level (`storage/storage.go:104`), so two
surfaces can both try. `TakePendingApproval` is atomic, so the second one gets
`ErrNoPendingInterrupt` (`agentkit/run/runtime.go:482`) — correct, but the
UX needs to say "someone else already answered". Should the second surface be
told *who* answered?

**Q4 — Should the run's row carry the usage totals?**
Deliberately left out (§3.1): the transcript owns the numbers. But a run list
that shows "what did that turn cost" would have to read the transcript per
run, which is a range scan over the message log. A cached total in the row
would be a second home for a number that is currently derived — the exact
duplication AGENTS.md warns about.

**Q5 — What happens to `turn_superseded`?**
Kept for compatibility (§8.3). Once the code has shipped, the superseding path
it names no longer exists. It can be retired after a deprecation window.

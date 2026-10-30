# Run Lifecycle Design — a turn outlives its connection

> openheron today binds one round of conversation to one HTTP connection: the
> connection is what drains the event stream, and the archive is what the
> connection hands back. This document proposes breaking that binding — the
> run becomes a durable object with its own identity, ownership and truth, and
> the SSE connection becomes one optional view of it.
>
> ★ **Section 1 describes the code as it is.** Every claim there points at a
> line in the tree. **Section 2 onwards is a proposal** — nothing in it is
> implemented. The distinction is kept sharp on purpose: a design document
> that blurs "what is" into "what would be" is how documentation drift starts.

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
  ├─ api/v2_run.go:126   startRun → run.Runtime.Submit
  │    └─ agentkit/run/runtime.go:129   claimTurn        (the gate: a mutex)
  │    └─ agentkit/run/runtime.go:212   runClaimedTurn
  │         └─ agentkit/run/bridge.go:99   startLoop → a goroutine
  │              └─ agentkit/run/loop.go:21   newLoop → adk.TurnLoop
  │
  ├─ agentkit/run/loop.go:100  makeOnAgentEvents  (inside the loop goroutine)
  │    └─ agentkit/run/loop.go:112  turn.events = events     ← the iterator is handed over
  │    └─ agentkit/run/loop.go:113  turn.signalReady()
  │    └─ agentkit/run/loop.go:117  waits on turn.done        ← blocked here
  │
  └─ api/v2_run.go:137  turn.Wait(...)          (the HTTP handler blocks too)
       api/v2_run.go:150  transport.Stream(stream, turn, ...)
            └─ transport/transport.go:72   RunStream(w, …, turn.Events(), …)
                 └─ transport/transport.go:103  src.Next()   ← the ONLY consumer
                 └─ transport/transport.go:167/174/182  accumulates res.Intermediates
       api/v2_run.go:157  turn.Complete(Outcome{Intermediates: res.Intermediates, …})
            └─ agentkit/run/turn.go:157  t.done <- outcome
                 └─ agentkit/run/loop.go:122  rt.archive(sess, turn, out.out)
                      └─ agentkit/run/loop.go:157  sess.Append(msg)   ← the transcript
```

★ **The persistence path runs through the rendering path.** The transcript is
written only after the HTTP handler has drained the eino event stream and
reported back. The renderer is not a view of the run — it *is* the run's
output pipeline.

### 1.2 Five consequences

**C1 — A run has no durable identity.**
`Turn.RunID` (`agentkit/run/turn.go:23`) is declared and never assigned. The
protocol run id is minted in the API layer (`api/v2_run.go:102-105`) and lives
only in the SSE frames and in the obs context (`agentkit/obs/runid.go:7`).
`Runtime.StatusOf` never sets `SessionStatus.RunID` (`agentkit/run/runtime.go:596-626`),
so `GET /v2/sessions/{threadId}` returns an empty `runId` today
(`api/v2_sessions.go:94`). The client does not send a `runId` at all
(`web/src/lib/store/actions.ts:250-254`), even though the wire type has one
(`web/src/lib/api.ts:46`).

**C2 — A crash loses the run silently.**
The archive is reachable only from the connection's drain
(`api/v2_run.go:157` → `agentkit/run/loop.go:122`). If the process dies
mid-run, the messages are not in the transcript, no run record exists, and the
session reads as idle on the next start — indistinguishable from "never ran".
There is no trace to reconcile.

**C3 — A dead handler wedges the session.**
`agentkit/run/runtime.go:222` detaches the run from the request:
`context.WithCancel(context.WithoutCancel(ctx))`. The stored `turn.cancel` is
called from exactly one place — `agentkit/run/bridge.go:128`, *after* the loop
exits. But the loop cannot exit while `agentkit/run/loop.go:117` is blocked on
`turn.done`, and `turn.done` is only fed by `Complete` (the handler) or `fail`
(`agentkit/run/turn.go:177`). So a handler that dies without calling
`Complete` — a panic, a killed worker — leaves the run goroutine blocked
forever and the session permanently busy. `Abort` is the only escape
(`agentkit/run/runtime.go:455`), and nothing calls it automatically.
(An ordinary client disconnect is *not* this case: the handler keeps running
and the drain continues, because `transport/transport.go:611-623` only stops
*emitting* after a write error — the accumulation at lines 167/174/182 goes on.)

**C4 — A live run is invisible to a second surface.**
`turn.events` is a single-consumer `*adk.AsyncIterator`
(`agentkit/run/turn.go:25`). Whoever drains it first owns it. A second browser
cannot attach to a running turn, and `GET /v2/sessions/{threadId}/messages`
(`api/v2_sessions.go:269`) shows only what is already archived — so a fresh
page shows a conversation that stops mid-thought with no indication that
something is still running.

**C5 — Ownership is first-come-first-served, with no notion of liveness.**
`claimTurn` (`agentkit/run/runtime.go:161-183`) is a mutex: whoever gets the
lock first owns the session until it finishes. There is no lease, no epoch, no
way to distinguish "the owner is making progress" from "the owner is stalled".
The in-memory identity checks that exist today — `markTurnDone`
(`agentkit/run/bridge.go:51`) and the loop-identity check in `loopExited`
(`agentkit/run/bridge.go:119-124`) — are hand-rolled fences over pointers.
They work inside one process and nowhere else.

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
                     ├─ arbiter.Claim(session, runId)  →  Lease{Epoch: n}
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

### 3.1 The row

A run's record is a **lifecycle**, not a payload. The transcript already owns
the content and the numbers; the row owns the timing and the ownership.

```go
// storage/runledger.go (proposed)
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

### 3.2 The ledger

```go
// RunLedger is the durable record of runs.
//
// ★ Append writes one COMPLETE snapshot of a run, not a delta. The reader
//   takes the last row per run id, and the highest epoch among the open runs
//   is the session's current owner. Both rules are order-tolerant, which is
//   what makes a fenced-out owner harmless: it cannot write itself back into
//   the lead.
type RunLedger interface {
    Append(ctx context.Context, r Run) error
    // List returns one row per run, newest row per run id wins.
    List(ctx context.Context, sessionID string) ([]Run, error)
}
```

★ **Why a snapshot per line and not a delta.** A delta log makes every reader
carry the fold. A snapshot log makes every line readable on its own, and makes
a torn last line droppable (the run falls back to its previous state, which is
a state it was really in). The cost is repeated fields — irrelevant at this
size.

★ **Why "highest epoch among open runs wins" and not "last row wins".** The
last row wins *within* one run. *Across* runs, the epoch decides: a zombie
owner from epoch 3 that wakes up after epoch 4 took over writes a row, but the
reader still sees epoch 4 as the owner. This is the fence doing its work on
the read side, where it is cheap and needs no lock.

### 3.3 Where the row lives

Next to the session's other files, in the session directory
(`storage/filestore/session.go:503-507` defines the current names):

```
<root>/<sessionId>/session.jsonl     the transcript (append-only)
<root>/<sessionId>/state.json        the session state (rewritten atomically)
<root>/<sessionId>/runs.jsonl        the run ledger (append-only)  ← new
```

★ **`runs.jsonl` is not `state.json`.** `state.json` is rewritten whole on
every change (`storage/filestore/session.go:483-498`); a growing list of runs
inside it would mean rewriting the whole history of the session on every
transition. An append-only ledger matches the transcript's shape, reuses the
same append machinery, and makes the ledger's own write the durability
barrier.

★ **The ledger is bounded.** A ledger that grows forever is a leak. The file
driver trims to the last N rows (N = 200 by default) with the atomic rewrite
already in `persist`, keeping the file append-only in the common case and
bounded in the worst.

### 3.4 The row is the archive barrier

★ **The terminal row is written after the archive lands.** That single
ordering gives the busy flag its honesty for free:

```
run goroutine:  … archive → transcript  →  ledger.Append(state: completed)
scan:           sees "completed" ⟹ the messages are on disk
```

Today the same guarantee costs a 30-second wait channel
(`agentkit/run/turn.go:156-165`, `agentkit/run/turn.go:173`), because the
archive is downstream of the connection. With the row in place, the wait
disappears: *the row is the barrier*, and there is nothing to time out.

## 4. D2 — Notification is a hint, the scan is the truth

### 4.1 The server side

The SSE stream keeps its shape — it is still AG-UI frames over one HTTP
response. What changes is its authority:

- The run's goroutine writes the terminal row **whether or not anybody is
  subscribed**. The stream's end is not the run's end.
- `GET /v2/sessions/{threadId}/runs` scans the ledger and answers "what
  happened, what is happening".
- `GET /v2/sessions/{threadId}` reports `state` from the scan, not from the
  volatile map (`api/v2_sessions.go:448-461` today reads
  `Runtime.StatusOf`).

★ **The volatile map becomes a cache.** `sessionState`
(`agentkit/run/bridge.go:30-39`) stays — it holds the live loop and the live
turn, which are genuinely in-memory things — but it is no longer the *truth*.
`StatusOf` (`agentkit/run/runtime.go:596-626`) already mixes a volatile source
with a durable one (the pending approval from the session file); the design
makes that mixing explicit and puts the durable side in charge.

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

```
GET /v2/sessions/{threadId}/runs
{
  "items": [
    {
      "runId": "…", "state": "completed", "kind": "query",
      "epoch": 7, "startedAt": "…", "endedAt": "…", "error": null
    }
  ],
  "activeRunId": "…"      // the run that currently owns the session, if any
}
```

`activeRunId` is the one field the UI needs most: it is what makes a run
**attachable** (§8.1) and what replaces the empty `runId` that
`GET /v2/sessions/{threadId}` returns today (`api/v2_sessions.go:94`).

## 5. D3 — Lease and fence instead of first-come-first-served

### 5.1 Why the mutex is not enough

The mutex in `claimTurn` (`agentkit/run/runtime.go:161-183`) answers "who got
here first". It does not answer "who is still alive". Three failures follow:

1. **A stalled owner.** The owner holds the lock but is not progressing — a
   hung model call, a GC pause, a bug in the loop. Nobody else can take over;
   the session is stuck, and the only way out is `Abort`.
2. **A zombie owner.** The owner was paused (VM freeze, laptop sleep, a
   debugger breakpoint) and wakes up later. Its `loopExited`
   (`agentkit/run/bridge.go:115-140`) will clear state it no longer owns —
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
// storage/lease.go (proposed)
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
// ★ The arbiter, not the run package, allocates the epoch. That is what makes
//   the fence meaningful across processes: with a database driver the epoch
//   comes from the row, so it is global; with the memory driver it is
//   process-local, and the protocol is unchanged.
type LeaseArbiter interface {
    // Claim acquires the session's lease. It fails with ErrLeaseHeld when
    // another run holds a live lease, and succeeds when the lease is expired
    // or is already ours.
    Claim(ctx context.Context, sessionID, runID, owner string, ttl time.Duration) (Lease, error)
    // Renew extends a lease the caller still holds.
    Renew(ctx context.Context, l Lease, ttl time.Duration) (Lease, error)
    // Release gives the lease back.
    Release(ctx context.Context, l Lease) error
    // Fence reports the session's current epoch.
    Fence(ctx context.Context, sessionID string) (uint64, error)
}
```

★ **The run renews — on progress, not on a timer.** The lease is renewed when
the run **reports progress**: the loop renews after each iteration, and a
progress report is what counts. The TTL (30s by default) is the **budget**,
not the schedule: a run that has reported nothing for TTL loses the lease,
however alive it is.

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
(`agentkit/run/runtime.go:244-249`). Two locks, two scopes: the lease says
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

★ The current code has the fence in spirit and the lease not at all: the
pointer-identity checks in `markTurnDone` (`agentkit/run/bridge.go:51`) and
`loopExited` (`agentkit/run/bridge.go:119-124`) are fences over addresses.
The proposal replaces the addresses with epochs and adds the lease that the
pointer checks stand in for today.

## 6. D4 — The driver is pluggable

### 6.1 The interfaces live in `storage`

`storage` (L1 in [architecture.md](architecture.md)'s layering table) is
already the package that owns session and pending-approval interfaces
(`storage/storage.go:19-93`). The run ledger and the lease arbiter belong
there too, for the same reason: `agentkit/run` (L2) must depend on interfaces
only, and the API layer (L3) must not assemble business logic
(AGENTS.md's iron rule).

```go
// storage — interfaces and value types
type RunLedger interface { … }      // storage/runledger.go
type LeaseArbiter interface { … }   // storage/lease.go
type Run struct { … }               // storage/runledger.go
type Lease struct { … }             // storage/lease.go

var ErrLeaseHeld  = errors.New("lease held by another run")
var ErrFenceStale = errors.New("fence is stale")
```

### 6.2 The drivers, in the order they arrive

| Order | Driver | Ledger | Arbiter | What it buys |
|---|---|---|---|---|
| 1 | `storage/memarbit` | in-memory | in-memory | The protocol, exercised, single process. Nothing survives a restart. |
| 2 | `storage/filestore` | `runs.jsonl` | in-memory | Durability: a crash leaves a trace, and the reconciler can close it. |
| 3 | a database driver | a table | a table, with the epoch allocated by `UPDATE … SET epoch = epoch + 1` | Several processes over one session store. |

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

### 7.1 Why the current pipeline forces the coupling

`transport/transport.go:98-200` (`streamEvents`) does two jobs at once:

- it **renders** AG-UI frames (`e.emit…`), and
- it **derives** the archive payload — `res.Intermediates` (lines 167, 174,
  182), `res.Interrupts` (line 149), `res.Compaction` (line 142),
  `res.Usage` (lines 105, 196).

★ The derivation cannot be done twice: `renderStream`
(`transport/transport.go:266-292`) calls `mo.MessageStream.Recv()` on the
model's stream, and `streamState` (`transport/transport.go:294-430`) folds the
chunks into one message. **A second reader would split the chunks.** So the
renderer is necessarily the single consumer — and since the renderer is the
SSE sink, the sink owns the run's output.

### 7.2 The split

Two responsibilities become two packages:

| Today | Proposed |
|---|---|
| `transport/transport.go:98-430` — derive + render, in one pass | `agentkit/emit` — derive only: eino event → archive artifacts + `contract.RunEvent` |
| `transport/transport.go:531-685` — the AG-UI emitter | `transport` — render only: `contract.RunEvent` → AG-UI frames → SSE |

`agentkit/emit` is a new L1 package: it depends on `contract`, `message` and
eino, and nothing above it. `agentkit/run` calls it; `transport` does not need
to — the transport receives `contract.RunEvent`s and renders them.

### 7.3 The run's event vocabulary

```go
// contract/runevent.go (proposed)
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
    RunEventMessage    // a complete message, for the archive
    RunEventCompaction
    RunEventInterrupt
    RunEventUsage
    RunEventError
    RunEventDone
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
    ToolID string
    ToolName string
    Agent string
    At   time.Time
    Msg  *schema.Message   // set on RunEventMessage
}
```

★ Why not reuse AG-UI's event types directly: `contract` is an L0 leaf
([architecture.md](architecture.md)'s layering table) and AG-UI is a transport
concern. Putting AG-UI types in `contract` would make the domain depend on the
wire. The mapping is one-to-one in practice, which keeps `transport` thin.

### 7.4 The subscriber

The run publishes to its subscribers; the subscriber is the SSE connection.

```go
// agentkit/run (proposed)
type Run struct {
    // …
    subs   map[int]chan contract.RunEvent
    events []contract.RunEvent   // a BOUNDED window, not the turn's history
}
```

- A subscriber that is slow gets a **bounded** channel; when it overflows, the
  run **drops the subscriber** and marks the stream broken. A slow reader must
  never slow the run — that is the old "the sink blocks the loop" trap.
- ★ **A subscriber that is *stuck* is the case that matters, and it is not the
  same as one that is slow.** A slow reader drains its channel eventually. A
  stuck one has a socket write that never returns: the peer vanished without
  a reset, and the kernel keeps accepting bytes into a buffer nobody reads.
  The publisher must therefore **never block on a full channel** — it drops
  the subscriber instead of waiting for it, and the run carries on. Today the
  same situation stalls the run, because the run is the one writing to the
  socket (`transport/transport.go:611-623` is where that write happens today).
- A subscriber that disappears takes nothing with it. The run keeps going and
  keeps archiving.
- ★ **The run's event log is a bounded window, not the turn's history.** A
  client that attaches mid-run gets the events from the window, so it can
  render the tail of the turn without waiting for the turn to end. The window
  holds the last N events (N = 1000 by default) and drops the oldest, so a
  long turn costs a **fixed** amount of memory rather than one that grows with
  the output. The run drops the window when it ends.

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
  producer changes.

## 8. The API surface

### 8.1 Changed routes

**`POST /v2/run`** — three cases, decided by the `runId` the client sends:

| The session has | The request carries | Result |
|---|---|---|
| no live run | a `runId` (or none — the server mints one) | **create** the run; the response streams it |
| a live run with **that** id | the same `runId` | **attach**: subscribe to the live run, replaying its log |
| a live run with a **different** id | any `runId` | **409 `session_busy`** — unchanged from today |

★ The second row is the new capability: a reconnect. Today the client cannot
resume a stream it lost (`web/src/lib/store/actions.ts:250-254` sends no
`runId` at all), so a dropped connection loses the turn's output. With the
run id known, the client asks for the same run and gets the rest of it.

★ The client must therefore **keep the run id**. The scan returns it
(`activeRunId`), and the SSE frames already carry it (AG-UI's `runId`).

**`GET /v2/sessions/{threadId}`** — `state` comes from the scan; add
`activeRunId`. The empty `runId` that `api/v2_sessions.go:94` returns today
becomes a real value.

**`POST /v2/sessions/{threadId}/abort`** — aborts the **run**, not the loop.
It cancels the run's context, waits (bounded) for the terminal row, and
returns the run id it aborted. The current implementation
(`api/v2_sessions.go:386-409` → `agentkit/run/runtime.go:455`) manipulates the
loop directly; under the proposal the run's own cleanup writes the row, so
the abort is "stop this run" and the row follows.

### 8.2 New routes

```
GET /v2/sessions/{threadId}/runs              the scan: every run of the session
GET /v2/sessions/{threadId}/runs/{runId}      one run
```

Both read the ledger. Neither ever writes.

### 8.3 The error envelope

No new codes. The existing ones carry the meaning:

| Code | Where it is produced | Meaning |
|---|---|---|
| `session_busy` | `api/v2_types.go:48` | another run owns the session |
| `no_pending_interrupt` | `api/v2_types.go:36` | the approval was already answered |
| `turn_superseded` | `api/v2_types.go:49` | kept for compatibility; the superseding path is gone |
| `turn_start_timeout` | `api/v2_types.go:50` | the run never became streamable |

★ `turn_superseded` stays because removing it would break clients that branch
on it — but under the proposal it is only ever produced by the start-timeout
path's sibling, never by a takeover. The concurrency design's §6.3 already noted
this; the proposal does not change it.

## 9. The frontend

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

The store keeps the run id of the run it started. On a stream error or a
reconnect, the client asks the scan whether that run is still live:

- live → re-`POST /v2/run` with the same `runId` → **attach**, and the rest of
  the turn renders.
- not live → the run is over; the transcript is the truth; load it.

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
| the run id | not kept (`web/src/lib/store/actions.ts:250-254`) | kept, and sent on re-attach |

## 10. What changes where

| File | Change |
|---|---|
| `storage/runledger.go` | **new** — `Run`, `RunState`, `RunLedger` |
| `storage/lease.go` | **new** — `Lease`, `LeaseArbiter`, `ErrLeaseHeld`, `ErrFenceStale` |
| `storage/memarbit/arbiter.go` | **new** — the in-memory arbiter |
| `storage/filestore/runledger.go` | **new** — the `runs.jsonl` driver |
| `storage/filestore/session.go` | the session directory gains `runs.jsonl`; the `FileSession` gains a ledger handle |
| `agentkit/emit/` | **new** — the derivation split out of `transport` |
| `contract/runevent.go` | **new** — `RunEvent`, `RunEventKind` |
| `agentkit/run/runtime.go` | `claimTurn` becomes a lease claim; `Submit`/`Resume` carry the lease; `StatusOf` reads the scan |
| `agentkit/run/turn.go` | `Turn` becomes a `Run` with subscribers; `RunID` is assigned |
| `agentkit/run/loop.go` | `handoff` no longer waits for the consumer: the run's own goroutine consumes and archives |
| `agentkit/run/bridge.go` | `loopExited` writes the terminal row; `sessionState` becomes a cache |
| `agentkit/run/errors.go` | `ErrSessionBusy` stays; the lease errors map onto it |
| `transport/transport.go` | becomes a renderer: `contract.RunEvent` → AG-UI frames |
| `api/v2_run.go` | create / attach / refuse; the handler subscribes instead of draining |
| `api/v2_sessions.go` | `state` from the scan; `activeRunId`; abort targets the run |
| `api/v2_files.go` | the two new routes |
| `web/src/lib/store/actions.ts` | keep the run id; re-attach; re-scan instead of assuming |
| `web/src/lib/store/history.ts` | `busy` from the scan |
| `web/src/lib/api.ts` | `runId` on the run request; the runs scan |
| `web/src/lib/types.ts` | `activeRunId` on the session meta |

## 11. Testing

**Go — the ledger** (`storage/filestore`):
- a run's row survives a restart of the store;
- the last row per run id wins;
- among open runs, the highest epoch is the owner, even when the lower-epoch
  run wrote later;
- a torn last line is dropped and the run falls back to its previous state.

**Go — the arbiter** (`storage/memarbit`):
- a second claim on a live lease fails with `ErrLeaseHeld`;
- an expired lease can be claimed, and the new epoch is higher;
- a renewal by a fenced-out holder fails;
- the epoch never repeats;
- a run that **reports progress** keeps its lease;
- a run that reports **nothing** loses the lease even while it is alive — the
  run-level stall §5.1 describes.

**Go — the runtime** (`agentkit/run`):
- the run archives its messages **with no subscriber attached** — the direct
  test of "a turn outlives its connection";
- a subscriber that vanishes does not stop the run;
- **a subscriber whose channel overflows is dropped and the run keeps
  archiving** — the stuck subscriber of §7.4, where the socket write never
  returns;
- a run whose handler dies still writes its terminal row (the C3 case);
- a run that loses its lease stops and does not write a `running` row.
- a run nobody attached to keeps its event log **within the window's size** —
  the cost of an unwatched turn is bounded (§7.4).

**Go — the reconciler**:
- a run left `running` by a crash is closed as `interrupted` at startup,
  before the server accepts requests (the hook already exists:
  `storage/storage.go:29`).

**Frontend — unit**:
- `busy` is cleared only by a scan, not by a stream's end;
- `stop()` re-scans;
- a re-attach keeps the run id.

**E2E** (the fake backend, `web/test/e2e/`):
- a second tab attaches to a running turn and sees its output;
- a dropped connection re-attaches and receives the rest of the turn;
- a 409 still rolls back the optimistic bubble (the previous round's
  behaviour must survive the change).

## 12. Staging

**Stage 1 — the run becomes a fact.** The ledger, the row, the scan route, the
reconciler. The SSE still drives the run, but the run's **row** is written
independently of the connection, so a crash leaves a trace. This stage fixes
C1 and C2 and gives the frontend something true to scan.

**Stage 2 — the lease replaces the mutex.** The arbiter, the epoch, the
**progress-driven renewal**, the fence. The gate's behaviour is unchanged (still
"one session, one turn", still 409); only its internals move. This stage fixes
**both** kinds of stalled owner — the process-level one because the lease
expires when the process stops reporting, the run-level one because the
renewal is driven by progress — and it prepares the ground for a second
process.

**Stage 3 — the run owns its consumption.** The `agentkit/emit` split, the
subscribers, the run's event log, attach. This stage fixes C3 and C4 — the
coupling itself. It is also the stage where a **stuck subscriber stops being
a scenario**: from here on the run owns no socket, so a wedged connection
cannot reach the run at all. Until then the risk stands — the run still writes
to the socket itself, so a connection that wedges can still stall the archive.

★ **Stage 3 is the expensive one** and it is the only one that truly unbinds
the turn from the connection. Stages 1 and 2 are worth doing on their own: they
make the run observable and its ownership honest, and they cost a fraction of
Stage 3. A team that stops after Stage 2 has a system whose run facts are true
and whose ownership is bounded — a much better place to be than today.

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
A pending approval is session-level (`storage/storage.go:95-113`), so two
surfaces can both try. `TakePendingApproval` is atomic, so the second one gets
`ErrNoPendingInterrupt` (`agentkit/run/runtime.go:372-380`) — correct, but the
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

import type { RunsView } from "@/lib/api"

// attachTarget decides which run a surface should follow.
//
// ★ The session's run is the one the server says owns it — the scan's
//   activeRunId. A surface that is already following that run has nothing
//   to do: attaching again would replay the turn's events onto a
//   conversation that already holds them.
//
// ★ A surface that does not follow it is a second tab, a second window, or
//   a client that reloaded. It has to attach, or it would sit there
//   saying "this session is running in another window" while the window
//   it names is its own.
//
// ★ The scan is the only thing that knows. The client cannot guess which
//   run owns the session, and a guess would attach to a run that is over.
export function attachTarget(runs: RunsView, following: string | undefined): string | undefined {
  const active = runs.activeRunId
  if (!active) return undefined
  if (following === active) return undefined
  return active
}

// busyElsewhere says the session has a live turn this surface does not
// follow.
//
// ★ The run this surface follows is not busy. Busy means "a live turn that
//   belongs to somebody else" — another tab, another window, another
//   client. It is not a failure: the turn is fine, it just belongs to
//   someone else.
//
// ★ It is the scan that says so, and nothing else. A stream that ends
//   says nothing about the session: another surface's turn may still be
//   running, and the ledger is the only thing that knows.
export function busyElsewhere(runs: RunsView, following: string | undefined): boolean {
  const active = runs.activeRunId
  if (!active) return false
  return active !== following
}

// Recovery is what a surface does when a round's stream ended without the
// round's last frame.
export type Recovery =
  // watch: the round is still going — follow it.
  | { kind: "watch"; runId: string }
  // wait: the round is over, but its ledger row is still open — the barrier
  // behind the approval card is not up yet.
  | { kind: "wait"; runId: string }
  // transcript: the round is over — the transcript holds what it raised.
  | { kind: "transcript" }

// recoveryPlan says what to do after a round's stream was cut.
//
// ★ The wire cannot tell the two endings apart: a round that finished and a
//   stream that was cut both close the response, and the server writes the
//   round's last frame for the first one and deliberately does not for the
//   second (transport/transport.go). ★ A last frame says "this is everything
//   the run had to say", so an approval raised after that point would stay
//   invisible until the user reloaded the page — which is the bug this
//   plan exists to end.
//
// ★ So the plan comes from the ledger, and the ledger is the truth: a live
//   run means the round is still going, and no live run means it is over and
//   the transcript holds the approvals it raised.
//
// ★ A run that waits for an approval counts as over: it stopped to ask the
//   user a question, and the answer starts a new run (storage/runledger.go).
//
// ★ "refused" is the run the server said is no longer live — the answer to
//   an attach that came back 404. ★ It changes the plan, because the server
//   refuses on the **runtime's** turn while the ledger's row is the
//   **barrier**: the runtime releases its turn first, the approval is
//   written next, and the row closes last (agentkit/run/bridge.go). ★ So
//   there is a window in which the turn is gone and the approval is not on
//   disk yet, and reading the transcript in that window finds nothing.
export function recoveryPlan(runs: RunsView, refused?: string): Recovery {
  const active = runs.activeRunId
  // ★ A live run that is not the one the server refused: follow it. ★ It is
  //   the round that is actually going, whatever this surface was following
  //   a moment ago.
  if (active && active !== refused) return { kind: "watch", runId: active }
  // ★ The refused run's row is still open. ★ The row is the barrier, and the
  //   barrier stands behind the approval card: the approval is on disk
  //   before the row closes. ★ So an open row means the question has not
  //   landed yet — wait for the barrier, do not read a transcript that
  //   cannot hold it.
  if (refused) {
    const row = runs.items.find((r) => r.runId === refused)
    if (row && !TERMINAL_RUN_STATES.has(row.state)) {
      return { kind: "wait", runId: refused }
    }
  }
  return { kind: "transcript" }
}

// TERMINAL_RUN_STATES are the run states that are final.
//
// ★ They are the server's own set (storage/runledger.go): a run that is
//   starting or running owns the session, and every other state does not.
//   ★ "waiting_approval" is terminal — the run stopped to ask a question,
//   and the answer starts a new run.
const TERMINAL_RUN_STATES = new Set([
  "waiting_approval",
  "completed",
  "failed",
  "aborted",
  "interrupted",
])

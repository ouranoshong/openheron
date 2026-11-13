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

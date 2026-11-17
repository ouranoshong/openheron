import { describe, expect, test } from "bun:test"

import { attachTarget, busyElsewhere, recoveryPlan } from "../src/lib/attach"
import type { RunsView } from "../src/lib/api"

const RUNS = { items: [], activeRunId: "run-1" }
const T1 = "2026-10-30T10:00:00Z"

describe("the attach target", () => {
  // A session with no live turn has nothing to attach to. The scan is the
  // only thing that knows, and it says so.
  test("a session with no active run has nothing to attach to", () => {
    expect(attachTarget({ items: [] }, undefined)).toBeUndefined()
  })

  // The run that owns the session is the one to follow.
  test("the active run is the target", () => {
    expect(attachTarget(RUNS, undefined)).toBe("run-1")
  })

  // A surface that already follows the run has nothing to do: attaching
  // again would replay the turn's events onto a conversation that already
  // holds them.
  test("a surface that already follows the run has nothing to do", () => {
    expect(attachTarget(RUNS, "run-1")).toBeUndefined()
  })

  // A surface that follows a different run — a stale one — follows the one
  // the session actually has.
  test("a surface that follows another run attaches to the active one", () => {
    expect(attachTarget(RUNS, "run-0")).toBe("run-1")
  })
})

describe("the busy flag", () => {
  // Busy means "a live turn that belongs to somebody else".
  test("an active run that is not ours means busy", () => {
    expect(busyElsewhere(RUNS, undefined)).toBe(true)
  })

  test("our own run is not busy", () => {
    expect(busyElsewhere(RUNS, "run-1")).toBe(false)
  })

  test("no active run means the session is free", () => {
    expect(busyElsewhere({ items: [] }, undefined)).toBe(false)
  })
})

describe("the recovery plan", () => {
  // ★ The wire cannot tell a finished round and a cut stream apart, so the
  //   plan comes from the ledger.
  test("a live run means the round is still going — watch it", () => {
    expect(recoveryPlan(RUNS)).toEqual({ kind: "watch", runId: "run-1" })
  })

  // ★ No live run means the round is over: the transcript holds the
  //   approvals it raised.
  test("no active run means the round is over — read the transcript", () => {
    expect(recoveryPlan({ items: [] })).toEqual({ kind: "transcript" })
  })

  // ★ A run that waits for an approval is over as far as the ledger is
  //   concerned — the answer starts a new run.
  test("a run that waits for an approval is over", () => {
    const runs: RunsView = {
      items: [
        { runId: "run-1", state: "waiting_approval", kind: "query", epoch: 1, startedAt: T1 },
      ],
    }
    expect(recoveryPlan(runs)).toEqual({ kind: "transcript" })
  })
})

describe("the recovery plan after a refused attach", () => {
  // ★ The server refuses an attach on its **runtime**: the turn is gone.
  //   The ledger's row is the **barrier**, and it stands up after the
  //   approval is written (agentkit/run/bridge.go). ★ So a refused run's
  //   row can still be open, and the approval is not on disk yet.
  test("a refused run whose row is still open — wait for the barrier", () => {
    const runs: RunsView = {
      items: [{ runId: "run-1", state: "running", kind: "query", epoch: 1, startedAt: T1 }],
      activeRunId: "run-1",
    }
    expect(recoveryPlan(runs, "run-1")).toEqual({ kind: "wait", runId: "run-1" })
  })

  // ★ The barrier is up: the row is terminal and the approval is on disk.
  //   The transcript holds it now.
  test("a refused run whose row is closed — read the transcript", () => {
    const runs: RunsView = {
      items: [
        { runId: "run-1", state: "waiting_approval", kind: "query", epoch: 1, startedAt: T1 },
      ],
    }
    expect(recoveryPlan(runs, "run-1")).toEqual({ kind: "transcript" })
  })

  // ★ Another round is going: that one is the one to follow, whatever the
  //   refused run was.
  test("a live run that is not the refused one — watch it", () => {
    const runs: RunsView = {
      items: [
        { runId: "run-1", state: "running", kind: "query", epoch: 1, startedAt: T1 },
        { runId: "run-2", state: "running", kind: "resume", epoch: 2, startedAt: T1 },
      ],
      activeRunId: "run-2",
    }
    expect(recoveryPlan(runs, "run-1")).toEqual({ kind: "watch", runId: "run-2" })
  })
})

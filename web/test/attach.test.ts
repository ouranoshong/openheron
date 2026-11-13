import { describe, expect, test } from "bun:test"

import { attachTarget, busyElsewhere } from "../src/lib/attach"

const RUNS = { items: [], activeRunId: "run-1" }

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

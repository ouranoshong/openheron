import { describe, expect, test } from "bun:test"

import { seedFromHistory } from "../src/lib/store/history"
import type { AgMessage, PendingApproval } from "../src/lib/api"

const history: AgMessage[] = [
  { id: "m1", role: "user", content: "hello" },
  { id: "m2", role: "assistant", content: "hi" },
]

const approval: PendingApproval[] = [
  {
    interruptId: "int-1",
    reason: "tool_call",
    message: "write the file?",
  },
]

describe("session concurrency", () => {
  // The server's state is the truth about whether the session is busy. A
  // second tab hydrates from it — without this the tab thinks it is idle
  // and its send button works while another tab owns the turn.
  test("the server's running state hydrates as running", () => {
    const convo = seedFromHistory(history, undefined, undefined, "running")
    expect(convo.status).toBe("running")
  })

  // A hydrated "running" is a turn this surface has not joined yet. It does
  // not raise the busy flag: the flag says the live turn belongs to
  // somebody else, and this surface is about to attach to it instead — a
  // flag that went up and came down again would flash the warning at a
  // surface that is following the turn after all.
  test("a hydrated running turn does not raise the busy flag", () => {
    const convo = seedFromHistory(history, undefined, undefined, "running")
    expect(convo.busy).toBeUndefined()
  })

  test("an idle session is not busy", () => {
    const convo = seedFromHistory(history, undefined, undefined, "idle")
    expect(convo.busy).toBeUndefined()
  })

  test("an idle session hydrates as idle", () => {
    const convo = seedFromHistory(history, undefined, undefined, "idle")
    expect(convo.status).toBe("idle")
  })

  // An approval card is more specific than "running": the user must be
  // able to answer it.
  test("a pending approval wins over the running state", () => {
    const convo = seedFromHistory(history, approval, undefined, "running")
    expect(convo.status).toBe("awaiting-approval")
    expect(convo.interrupts.length).toBe(1)
  })

  // Without the server's state there is nothing to say the session is busy.
  test("no state at all hydrates as idle", () => {
    const convo = seedFromHistory(history)
    expect(convo.status).toBe("idle")
  })
})

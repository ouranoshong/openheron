import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import { useStore, type StoreState } from "../src/lib/store"

const A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const B = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

const store = new Map<string, string>()
;(globalThis as any).localStorage = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => void store.clear(),
  key: (i: number) => [...store.keys()][i] ?? null,
  get length() {
    return store.size
  },
}

// runsView is what the run ledger's scan answers.
let runsView: { items: unknown[]; activeRunId?: string } = { items: [] }
// pendings is what the transcript holds: the approvals that are open.
let pendings: unknown[] = []
// bodies records what the run requests carried.
let bodies: string[] = []
// attachMode says what the server does with an attach.
let attachMode: "ok" | "refused" = "ok"
// barrierScans counts the ledger reads of a refused round: the first one
// finds the row still open, the second finds it closed.
let barrierScans = 0
// barrierUp says whether the approval is on disk yet.
let barrierUp = false

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function sse(frames: unknown[]): Response {
  const body = frames.map((f) => `data: ${JSON.stringify(f)}\n\n`).join("")
  return new Response(body, { headers: { "Content-Type": "text/event-stream" } })
}

beforeEach(() => {
  runsView = { items: [] }
  pendings = []
  bodies = []
  attachMode = "ok"
  barrierScans = 0
  barrierUp = false
  globalThis.fetch = (async (input: any, init?: any) => {
    const url = typeof input === "string" ? input : String(input?.url ?? "")
    if (init?.body) bodies.push(String(init.body))
    if (url === "/v2/sessions") {
      return json({
        items: [
          {
            threadId: A,
            title: "Alpha",
            createdAt: "2026-10-30T10:00:00Z",
            updatedAt: "2026-10-30T10:05:00Z",
            messageCount: 0,
            state: "idle",
            hasPendingApproval: false,
            agentId: "main",
          },
        ],
      })
    }
    if (url.endsWith("/messages")) {
      // ★ A refused round's approval is on disk only once the barrier is
      //   up: the runtime released its turn first, the approval was written
      //   next, and the row closed last (agentkit/run/bridge.go).
      const open = attachMode === "refused" ? (barrierUp ? pendings : []) : pendings
      return json({
        threadId: A,
        state: open.length > 0 ? "waiting_approval" : "idle",
        messages: [],
        pendingApprovals: open,
      })
    }
    if (url.endsWith("/runs")) {
      // ★ The barrier comes up on the third read: the first one is the
      //   attach's own scan, the second one is the recovery's, and only
      //   then does the row close. ★ So the recovery has to wait.
      if (attachMode === "refused" && barrierScans++ >= 2) {
        barrierUp = true
        runsView = { items: [{ runId: "run-1", state: "waiting_approval" }] }
      }
      return json(runsView)
    }
    if (url === "/v2/run") {
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {}
      // ★ The attach the server refuses: its runtime has no turn for the run
      //   any more, and it says so before the ledger's barrier is up.
      if (attachMode === "refused" && body.runId && !body.query) {
        return json(
          {
            error: "the run is no longer live; load the session's history instead",
            code: "run_not_live",
            retryable: false,
          },
          404,
        )
      }
      // ★ The attach: the round's last frame, so the round ends normally.
      return sse([
        { type: "RUN_STARTED", threadId: A, runId: "run-1" },
        { type: "RUN_FINISHED", threadId: A, runId: "run-1", outcome: { type: "success" } },
      ])
    }
    return new Response(null, { status: 204 })
  }) as typeof fetch
  useStore.setState({
    sessions: {},
    order: [],
    activeId: null,
    pendingAgentId: null,
    loadingList: false,
    listError: undefined,
  } as Partial<StoreState>)
  localStorage.clear()
})
afterEach(() => {
  useStore.setState({ sessions: {}, order: [], activeId: null } as Partial<StoreState>)
})

// The recovery reads the ledger and the transcript: both are answers, and
// both arrive a moment later.
async function settle(): Promise<void> {
  for (let i = 0; i < 50; i++) {
    await new Promise((r) => setTimeout(r, 2))
  }
}

// waitFor waits for a condition the recovery reaches on its own schedule —
// the barrier comes up a second later, and the test does not get a vote.
async function waitFor(predicate: () => boolean, timeoutMs = 5000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (predicate()) return true
    await new Promise((r) => setTimeout(r, 25))
  }
  return predicate()
}

describe("the recovery of a cut round", () => {
  test("a round that is over is read from the transcript — the approval comes back", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    // ★ The round is over and it raised an approval: the ledger has no
    //   live run, and the transcript holds the approval.
    runsView = { items: [{ runId: "run-1", state: "waiting_approval" }] }
    pendings = [
      {
        interruptId: "int-1",
        reason: "tool_call",
        message: "write_file requires approval",
        toolName: "write_file",
      },
    ]

    await useStore.getState().recoverRound(A)
    await settle()

    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.status).toBe("awaiting-approval")
    expect(convo?.interrupts.length).toBe(1)
    expect(convo?.interrupts[0]?.id).toBe("int-1")
  })

  test("a round that is still going is watched, not read from the transcript", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    // ★ The ledger says the round is still live: the client follows it, and
    //   the transcript has nothing to say yet.
    runsView = { items: [{ runId: "run-1", state: "running" }], activeRunId: "run-1" }
    pendings = []

    await useStore.getState().recoverRound(A)
    await settle()

    // The attach carries the run the ledger named.
    expect(bodies.some((b) => b.includes("run-1"))).toBe(true)
    // ★ And the transcript was not read as the answer: the round is not
    //   over yet.
    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.interrupts.length ?? 0).toBe(0)
  })

  test("a session the user left alone is left alone too", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState({ activeId: B } as Partial<StoreState>)

    runsView = { items: [] }
    pendings = [
      { interruptId: "int-1", reason: "tool_call", message: "write_file requires approval" },
    ]

    await useStore.getState().recoverRound(A)
    await settle()

    // ★ The card belongs to a session nobody is looking at, and the
    //   transcript is read when the user comes back.
    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.interrupts.length ?? 0).toBe(0)
  })
})

describe("the recovery after a refused attach", () => {
  test("a refused attach waits for the barrier — and the approval comes back", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    // ★ The server refuses the attach: its runtime has no turn for the run.
    attachMode = "refused"
    // ★ And the ledger's barrier is not up yet: the row is still open, so
    //   the approval is not on disk.
    runsView = { items: [{ runId: "run-1", state: "running" }], activeRunId: "run-1" }
    pendings = [
      {
        interruptId: "int-1",
        reason: "tool_call",
        message: "write_file requires approval",
        toolName: "write_file",
      },
    ]

    await useStore.getState().reattach(A, "the connection dropped")
    const arrived = await waitFor(
      () => (useStore.getState().sessions[A]?.convo?.interrupts.length ?? 0) > 0,
    )

    // ★ The card arrives on its own: no reload, and the refusal did not
    //   cost the user the question.
    expect(arrived).toBe(true)
    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.status).toBe("awaiting-approval")
    expect(convo?.interrupts[0]?.id).toBe("int-1")
  })

  test("the ledger names the run to follow — not the run the conversation saw last", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    // ★ The conversation still holds the run it saw last, and that round is
    //   over. ★ The ledger names the round that is actually going.
    const entry = useStore.getState().sessions[A]!
    useStore.setState({
      sessions: {
        [A]: { ...entry, convo: { ...entry.convo!, runId: "run-old" } },
      },
    } as Partial<StoreState>)
    runsView = { items: [{ runId: "run-2", state: "running" }], activeRunId: "run-2" }

    await useStore.getState().reattach(A, "the connection dropped")
    await settle()

    // ★ The attach carries the run the ledger named.
    expect(bodies.some((b) => b.includes("run-2"))).toBe(true)
    expect(bodies.some((b) => b.includes("run-old"))).toBe(false)
  })
})

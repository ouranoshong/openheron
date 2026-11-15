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
      return json({
        threadId: A,
        state: pendings.length > 0 ? "waiting_approval" : "idle",
        messages: [],
        pendingApprovals: pendings,
      })
    }
    if (url.endsWith("/runs")) {
      return json(runsView)
    }
    if (url === "/v2/run") {
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

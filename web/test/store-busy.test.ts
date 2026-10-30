import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import { selectBusyElsewhere, useStore, type StoreState } from "../src/lib/store"

const A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

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

let busyResponse = false

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

beforeEach(() => {
  busyResponse = false
  globalThis.fetch = (async (input: any) => {
    const url = typeof input === "string" ? input : String(input?.url ?? "")
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
      return json({ threadId: A, state: "idle", messages: [] })
    }
    if (url === "/v2/run") {
      if (busyResponse) {
        return json(
          {
            error: "session is busy: a turn is already running on this session",
            code: "session_busy",
            retryable: true,
          },
          409,
        )
      }
      return json({}, 200)
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

// send() hands the request to runStream, which is asynchronous — the store
// settles a moment later.
async function settle(): Promise<void> {
  for (let i = 0; i < 50; i++) {
    await new Promise((r) => setTimeout(r, 2))
  }
}

describe("a refused run", () => {
  test("the optimistic bubble is rolled back — the message never landed", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    busyResponse = true
    await useStore.getState().send("hello")
    await settle()

    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.messages.length ?? 0).toBe(0)
  })

  test("the session is marked busy, not failed", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    busyResponse = true
    await useStore.getState().send("hello")
    await settle()

    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.status).toBe("running")
    expect(convo?.error?.code).toBe("session_busy")
    expect(selectBusyElsewhere(useStore.getState())).toBe(true)
  })

  test("a plain failure is still an error, not a busy session", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    // A non-409 failure: the message was refused for another reason.
    globalThis.fetch = (async (input: any) => {
      const url = typeof input === "string" ? input : String(input?.url ?? "")
      if (url === "/v2/run") {
        return json({ error: "boom", code: "internal", retryable: false }, 500)
      }
      return new Response(null, { status: 204 })
    }) as typeof fetch

    await useStore.getState().send("hello")
    await settle()

    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.status).toBe("error")
    expect(selectBusyElsewhere(useStore.getState())).toBe(false)
  })
})

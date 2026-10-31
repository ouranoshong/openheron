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
// runsView is what the run ledger's scan answers.
let runsView: { items: unknown[]; activeRunId?: string } = { items: [] }

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

beforeEach(() => {
  busyResponse = false
  runsView = { items: [] }
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
    if (url.endsWith("/runs")) {
      return json(runsView)
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

describe("the scan decides whether the session is busy", () => {
  test("an active run that is not ours means busy", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    runsView = { items: [{ runId: "other-run" }], activeRunId: "other-run" }

    await useStore.getState().scanBusy(A)

    expect(selectBusyElsewhere(useStore.getState())).toBe(true)
  })

  test("no active run means the session is free", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    // The surface was refused earlier, so it believes the session is busy.
    busyResponse = true
    await useStore.getState().send("hello")
    await settle()
    expect(selectBusyElsewhere(useStore.getState())).toBe(true)

    // The scan says the run is over: the flag goes, and so does the reason.
    runsView = { items: [] }
    await useStore.getState().scanBusy(A)

    const convo = useStore.getState().sessions[A]?.convo
    expect(selectBusyElsewhere(useStore.getState())).toBe(false)
    expect(convo?.error?.code).toBeUndefined()
  })

  test("our own run is not busy", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({
      sessions: {
        ...s.sessions,
        [A]: { ...s.sessions[A]!, convo: { ...s.sessions[A]!.convo!, runId: "mine" } },
      },
    }))
    runsView = { items: [{ runId: "mine" }], activeRunId: "mine" }

    await useStore.getState().scanBusy(A)

    expect(selectBusyElsewhere(useStore.getState())).toBe(false)
  })

  test("a failed scan leaves the flag alone", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    busyResponse = true
    await useStore.getState().send("hello")
    await settle()
    expect(selectBusyElsewhere(useStore.getState())).toBe(true)

    // The scan fails: guessing is what the scan exists to stop.
    globalThis.fetch = (async () => new Response(null, { status: 500 })) as unknown as typeof fetch
    await useStore.getState().scanBusy(A)

    expect(selectBusyElsewhere(useStore.getState())).toBe(true)
  })
})

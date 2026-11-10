import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import { useStore, type StoreState } from "../src/lib/store"

const A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

const store = new Map<string, string>()
;(globalThis as any).localStorage = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => store.clear(),
  key: (i: number) => [...store.keys()][i] ?? null,
  get length() {
    return store.size
  },
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

// ★ closeStream ends the run. The stream stays open on purpose: the title
//   has to survive the whole run, not just its first frame.
let closeStream: () => void = () => {}

function openRun(): Response {
  const encoder = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      closeStream = () => c.close()
      c.enqueue(encoder.encode('data: {"type":"RUN_STARTED","runId":"r-1"}\n\n'))
    },
  })
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } })
}

beforeEach(() => {
  closeStream = () => {}
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
            messageCount: 2,
            state: "idle",
            hasPendingApproval: false,
            agentId: "main",
          },
        ],
      })
    }
    if (url.endsWith("/messages")) return json({ threadId: A, state: "idle", messages: [] })
    if (url.endsWith("/runs")) return json({ items: [] })
    if (url === "/v2/run") return openRun()
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
  closeStream()
  useStore.setState({ sessions: {}, order: [], activeId: null } as Partial<StoreState>)
})

// send() hands the request to runStream, which is asynchronous — the store
// settles a moment later.
async function settle(): Promise<void> {
  for (let i = 0; i < 50; i++) {
    await new Promise((r) => setTimeout(r, 2))
  }
}

describe("send —— 乐观占位符不能改掉会话的标题", () => {
  test("★ 发送消息的过程中，标题还是会话自己的", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    expect(useStore.getState().sessions[A]?.meta.title).toBe("Alpha")

    await useStore.getState().send("hello")
    await settle()

    // ★ The run is still going: the placeholder is on screen, and the
    //   conversation must not have been renamed to "New Chat" in the
    //   meantime. The title is the conversation's own — the server's title
    //   only comes back when the list is refreshed at the end of the turn.
    expect(useStore.getState().sessions[A]?.meta.title).toBe("Alpha")
  })

  test("★ 乐观气泡照旧出现", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    await useStore.getState().send("hello")
    await settle()

    const convo = useStore.getState().sessions[A]?.convo
    expect(convo?.messages.some((m) => m.role === "user")).toBe(true)
  })

  test("★ 会话自己的 agent 归属不被占位符抹掉", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({
      sessions: {
        ...s.sessions,
        [A]: { ...s.sessions[A]!, meta: { ...s.sessions[A]!.meta, agentId: "worker" } },
      },
    }))

    await useStore.getState().send("hello")
    await settle()

    expect(useStore.getState().sessions[A]?.meta.agentId).toBe("worker")
  })
})

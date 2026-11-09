import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import { selectAttachments, useStore, type StoreState } from "../src/lib/store"

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

let busyResponse = false
// runBody is the body of the last run request.
let runBody: { query?: string; attachments?: { name: string; path: string; size?: number }[] } | null =
  null
// workspaceFiles is what the session's workspace holds.
let workspaceFiles: { name: string; path: string; size: number; mime: string }[] = []

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

beforeEach(() => {
  busyResponse = false
  runBody = null
  workspaceFiles = []
  globalThis.fetch = (async (input: any, init?: RequestInit) => {
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
    if (url.endsWith("/files")) {
      return json({ workspacePath: "/workspace", files: workspaceFiles })
    }
    if (url.endsWith("/runs")) {
      return json({ items: [] })
    }
    if (url === "/v2/run") {
      runBody = JSON.parse(String(init?.body ?? "{}"))
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
    attachments: {},
    uploading: false,
  } as Partial<StoreState>)
  localStorage.clear()
})

afterEach(() => {
  useStore.setState({ sessions: {}, order: [], activeId: null } as Partial<StoreState>)
})

// settle waits for the store to stop moving.
async function settle(): Promise<void> {
  for (let i = 0; i < 50; i++) {
    await new Promise((r) => setTimeout(r, 2))
  }
}

function pendingFile(name: string, path = name) {
  return { name, path, size: 12, mime: "text/plain" }
}

describe("发出去的那一轮", () => {
  test("★ 请求体里带着附件", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({ attachments: { ...s.attachments, [A]: [pendingFile("notes.md")] } }))

    await useStore.getState().send("look at this")
    await settle()

    expect(runBody?.attachments ?? []).toHaveLength(1)
    expect(runBody?.attachments?.[0]?.name).toBe("notes.md")
    // ★ The path is relative to the workspace: it is what the server
    //   resolves, and an absolute path is a fact about one machine.
    expect(runBody?.attachments?.[0]?.path).toBe("notes.md")
  })

  test("★ 发完待发清单就空了", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({ attachments: { ...s.attachments, [A]: [pendingFile("once.txt")] } }))

    await useStore.getState().send("look at this")
    await settle()

    // ★ Leaving the file in the list would send it again with the next
    //   question — the user asked once.
    expect(selectAttachments(useStore.getState())).toHaveLength(0)
  })

  test("★ 乐观气泡里就带着附件", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({ attachments: { ...s.attachments, [A]: [pendingFile("shot.png")] } }))

    await useStore.getState().send("what is this?")
    await settle()

    const convo = useStore.getState().sessions[A]?.convo
    const parts = convo?.messages[0]?.parts ?? []
    expect(parts.some((p) => p.kind === "attachment")).toBe(true)
  })
})

describe("被拒的一轮", () => {
  test("★ 附件回到待发清单", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({ attachments: { ...s.attachments, [A]: [pendingFile("kept.md")] } }))

    busyResponse = true
    await useStore.getState().send("look at this")
    await settle()

    // ★ The server refused the run, so the message — and its files — never
    //   arrived. The files are still the user's.
    const pending = selectAttachments(useStore.getState())
    expect(pending.map((f) => f.name)).toEqual(["kept.md"])
  })
})

describe("刷新之后", () => {
  test("★ workspace 里没人认领的文件回到待发清单", async () => {
    workspaceFiles = [pendingFile("unsent.md")]

    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    const pending = selectAttachments(useStore.getState())
    expect(pending.map((f) => f.name)).toEqual(["unsent.md"])
  })
})

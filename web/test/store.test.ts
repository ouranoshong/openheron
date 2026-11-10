import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import {
  MAIN_AGENT,
  seedFromHistory,
  selectActiveAgentId,
  useStore,
  type StoreState,
} from "../src/lib/store"
import type { AgMessage } from "../src/lib/api"

const A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const B = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

const history: Record<string, AgMessage[]> = {
  [A]: [
    { id: "a1", role: "user", content: "MARKER-ALPHA question" },
    { id: "a2", role: "assistant", content: "MARKER-ALPHA answer" },
  ],
  [B]: [{ id: "b1", role: "user", content: "MARKER-BRAVO question" }],
}

const meta = (id: string, title: string) => ({
  threadId: id,
  title,
  createdAt: "2026-10-12T10:00:00Z",
  updatedAt: "2026-10-12T10:05:00Z",
  messageCount: 2,
  state: "idle" as const,
  hasPendingApproval: false,
  agentId: "main",
})

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

let fetchCalls: string[] = []

let emptyList = false
const missing = new Set<string>()

function installFetch(): void {
  fetchCalls = []
  const real = globalThis.fetch
  globalThis.fetch = (async (input: any, init?: RequestInit) => {
    const url = typeof input === "string" ? input : String(input?.url ?? "")
    fetchCalls.push(`${init?.method ?? "GET"} ${url}`)

    if (url === "/v2/sessions") {
      return json({ items: emptyList ? [] : [meta(A, "Alpha"), meta(B, "Bravo")] })
    }
    if (url.endsWith("/messages")) {
      const id = url.split("/")[3]!
      if (missing.has(id)) {
        return json({ error: "session not found", code: "thread_not_found", retryable: false }, 404)
      }
      return json({ threadId: id, state: "idle", messages: history[id] ?? [] })
    }
    if (url.startsWith("/v2/sessions/") && init?.method === "DELETE") {
      return new Response(null, { status: 204 })
    }
    if (url.startsWith("/v2/sessions/") && url.endsWith("/agent") && init?.method === "PATCH") {
      const id = url.split("/")[3]!
      const body = JSON.parse(String(init.body ?? "{}")) as { agentId?: string }
      return json({ ...meta(id, "x"), agentId: body.agentId ?? "main" })
    }
    if (url.startsWith("/v2/sessions/") && init?.method === "PATCH") {
      return json(meta(url.split("/")[3]!, "renamed"))
    }
    if (url.startsWith("/v2/sessions/") && url.endsWith("/abort") && init?.method === "POST") {
      return json({ threadId: url.split("/")[3]!, aborted: true })
    }
    if (url.startsWith("/v2/sessions/") && url.endsWith("/files")) {
      const method = init?.method ?? "GET"
      if (method === "DELETE") {
        return json({ deleted: true, path: new URL(url, "http://x").searchParams.get("path") ?? "" })
      }
      return json({ workspacePath: "/workspace", files: [] })
    }
    return real(input, init)
  }) as typeof fetch
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function reset(): void {
  emptyList = false
  missing.clear()
  localStorage.clear()
  useStore.setState({
    sessions: {},
    order: [],
    activeId: null,
    pendingAgentId: null,
    loadingList: false,
    listError: undefined,
  } as Partial<StoreState>)
}

beforeEach(() => {
  installFetch()
  reset()
})
afterEach(() => reset())

describe("refreshList", () => {
  test("列出全部会话", async () => {
    await useStore.getState().refreshList()
    expect(useStore.getState().order).toEqual([A, B])
    expect(useStore.getState().sessions[A]!.meta.title).toBe("Alpha")
  })

  test("刷新不清空已加载的对话", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    const before = useStore.getState().sessions[A]!.convo!.messages.length

    await useStore.getState().refreshList()

    expect(useStore.getState().sessions[A]!.convo!.messages).toHaveLength(before)
    expect(useStore.getState().sessions[A]!.loaded).toBe(true)
  })

  test("列表为空时清掉记住的会话 id", async () => {
    await useStore.getState().openSession(A)
    expect(localStorage.getItem("openheron.activeThread")).toBe(A)

    emptyList = true
    await useStore.getState().refreshList()

    expect(localStorage.getItem("openheron.activeThread")).toBeNull()
    expect(useStore.getState().activeId).toBeNull()
  })

  test("列表里消失的会话也从表里移除", async () => {
    await useStore.getState().refreshList()
    useStore.setState({ order: [A, B, "ghost"], sessions: { ...useStore.getState().sessions, ghost: { meta: meta("ghost", "g"), loaded: false } } })
    await useStore.getState().refreshList()
    expect(useStore.getState().order).not.toContain("ghost")
    expect(useStore.getState().sessions.ghost).toBeUndefined()
  })
})

describe("openSession —— Phase 0 钉红的三个缺陷之一", () => {
  test("切到 B 之后只看到 B 的消息", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    expect(JSON.stringify(useStore.getState().sessions[A]!.convo)).toContain("MARKER-ALPHA")

    await useStore.getState().openSession(B)

    const b = useStore.getState().sessions[B]!.convo!
    expect(JSON.stringify(b)).toContain("MARKER-BRAVO")
    expect(JSON.stringify(b)).not.toContain("MARKER-ALPHA")
  })

  test("加载前先清空，不闪上一个会话的内容", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    void useStore.getState().openSession(B)
    expect(useStore.getState().sessions[B]!.convo!.messages).toEqual([])
    await useStore.getState().openSession(B)
    expect(useStore.getState().sessions[B]!.convo!.messages).toHaveLength(1)
  })

  test("重复打开同一个已加载的会话不发第二次请求", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    const before = fetchCalls.filter((c) => c.includes("/messages")).length
    await useStore.getState().openSession(A)
    expect(fetchCalls.filter((c) => c.includes("/messages")).length).toBe(before)
  })

  test("切换会落到 localStorage —— Phase 0 钉红的第二个缺陷", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(B)
    expect(localStorage.getItem("openheron.activeThread")).toBe(B)
  })

  test("未知会话读 404 时不崩，落成空会话", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession("no-such")
    expect(useStore.getState().sessions["no-such"]?.convo?.messages).toEqual([])
  })

  test("会话没了就清掉记住的 id，回欢迎界面", async () => {
    missing.add("gone")
    localStorage.setItem("openheron.activeThread", "gone")
    useStore.setState({ activeId: "gone" })

    await useStore.getState().openSession("gone")

    expect(localStorage.getItem("openheron.activeThread")).toBeNull()
    expect(useStore.getState().activeId).toBeNull()
    expect(useStore.getState().sessions["gone"]).toBeUndefined()
    expect(useStore.getState().sessions["gone"]?.convo?.error).toBeUndefined()
  })
})

describe("startNewThread —— Phase 0 钉红的第三个缺陷", () => {
  test("回到欢迎界面且不写 localStorage", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    useStore.getState().startNewThread()

    expect(useStore.getState().activeId).toBeNull()
    expect(localStorage.getItem("openheron.activeThread")).toBe(A)
  })

  test("不发任何会话请求", async () => {
    await useStore.getState().refreshList()
    fetchCalls = []
    useStore.getState().startNewThread()
    expect(fetchCalls).toHaveLength(0)
  })

  test("侧栏条目数不变", async () => {
    await useStore.getState().refreshList()
    const before = useStore.getState().order.length
    useStore.getState().startNewThread()
    expect(useStore.getState().order).toHaveLength(before)
  })
})

describe("renameSession", () => {
  test("乐观更新，回来的结果不需要再改", async () => {
    await useStore.getState().refreshList()
    const p = useStore.getState().renameSession(A, "renamed")
    expect(useStore.getState().sessions[A]!.meta.title).toBe("renamed")
    await p
    expect(useStore.getState().sessions[A]!.meta.title).toBe("renamed")
  })

  test("失败时回滚", async () => {
    await useStore.getState().refreshList()
    const real = globalThis.fetch
    globalThis.fetch = (async () => new Response("{}", { status: 500 })) as unknown as typeof fetch
    await expect(useStore.getState().renameSession(A, "will fail")).rejects.toThrow()
    globalThis.fetch = real
    expect(useStore.getState().sessions[A]!.meta.title).toBe("Alpha")
  })
})

describe("deleteSession", () => {
  test("删掉正在看的那个 → 回欢迎界面", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    await useStore.getState().deleteSession(A)
    expect(useStore.getState().activeId).toBeNull()
    expect(useStore.getState().order).not.toContain(A)
    expect(useStore.getState().sessions[A]).toBeUndefined()
  })

  test("删掉别的会话时保持当前激活不变", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    await useStore.getState().deleteSession(B)
    expect(useStore.getState().activeId).toBe(A)
  })
})

// ── seedFromHistory ───────────────────────────────────────────────────────

describe("seedFromHistory", () => {
  test("文本变成 complete 的 text part", () => {
    const c = seedFromHistory([{ id: "m1", role: "assistant", content: "hi" }])
    expect(c.messages[0]!.parts[0]).toEqual({ kind: "text", id: "m1:t0", text: "hi", complete: true })
  })

  test("历史里的思维链重建为 reasoning part（带用时）", () => {
    const c = seedFromHistory([
      {
        id: "m1",
        role: "assistant",
        content: "答完了",
        metadata: { reasoning: "先看看这个仓库", reasoningMs: 20258 },
      },
    ])
    const parts = c.messages[0]!.parts
    expect(parts[0]!.kind).toBe("reasoning")
    const r = parts[0]!
    if (r.kind !== "reasoning") throw new Error("expected a reasoning part")
    expect(r.text).toBe("先看看这个仓库")
    expect(r.complete).toBe(true)
    expect(r.elapsedMs).toBe(20258)
    expect(parts[1]!.kind).toBe("text")
  })

  test("没有思维链的历史消息不带 reasoning part", () => {
    const c = seedFromHistory([{ id: "m1", role: "assistant", content: "hi" }])
    expect(c.messages[0]!.parts.every((p) => p.kind !== "reasoning")).toBe(true)
  })

  test("tool_calls 变成 tool part", () => {
    const c = seedFromHistory([
      {
        id: "m2",
        role: "assistant",
        content: "",
        toolCalls: [{ id: "call_1", type: "function", function: { name: "ls", arguments: '{"path":"."}' } }],
      },
    ])
    const p = c.messages[0]!.parts[0]!
    expect(p.kind).toBe("tool")
    expect(p.kind === "tool" && p.name).toBe("ls")
    expect(p.kind === "tool" && p.fromHistory).toBe(true)
  })

  test("工具结果并进同一次调用，不单独成条", () => {
    const c = seedFromHistory([
      {
        id: "m2",
        role: "assistant",
        content: "",
        toolCalls: [{ id: "call_1", type: "function", function: { name: "ls", arguments: "{}" } }],
      },
      { id: "m3", role: "tool", content: "a b c", toolCallId: "call_1" },
    ])
    expect(c.messages).toHaveLength(1)
    expect(c.messages[0]!.parts).toHaveLength(1)
    expect(c.messages[0]!.parts[0]!.kind === "tool" && c.messages[0]!.parts[0]!.result).toBe("a b c")
  })

  // ★ The attachment list is the answer to "that image you just sent".
  //
  //	★ It comes back with the message, so the history shows the file and
  //	  not only the question that carried it.
  test("历史里的附件清单重建为 attachment part", () => {
    const c = seedFromHistory([
      {
        id: "m1",
        role: "user",
        content: "图里写了什么？",
        metadata: {
          attachments: [{ name: "shot.png", path: "shot.png", size: 2048, mime: "image/png" }],
        },
      },
    ])
    const parts = c.messages[0]!.parts
    // ★ The files come first, the text after — the same order the
    //   optimistic bubble uses.
    expect(parts[0]!.kind).toBe("attachment")
    const p = parts[0]!
    if (p.kind !== "attachment") throw new Error("expected an attachment part")
    expect(p.name).toBe("shot.png")
    expect(p.path).toBe("shot.png")
    expect(p.size).toBe(2048)
    expect(p.mime).toBe("image/png")
    expect(parts[1]!.kind).toBe("text")
  })

  test("没有附件的消息不带 attachment part", () => {
    const c = seedFromHistory([{ id: "m1", role: "user", content: "hi" }])
    expect(c.messages[0]!.parts.every((p) => p.kind !== "attachment")).toBe(true)
  })

  test("消息带出 agent 归属", () => {
    const c = seedFromHistory([
      { id: "m1", role: "assistant", content: "planning", name: "main" },
      { id: "m2", role: "assistant", content: "patching", name: "coder" },
    ])
    expect(c.messages[0]!.agent).toBe("main")
    expect(c.messages[1]!.agent).toBe("coder")
  })

  test("没有归属标记的消息不编造 agent", () => {
    const c = seedFromHistory([{ id: "m1", role: "user", content: "hi" }])
    expect(c.messages[0]!.agent).toBeUndefined()
  })

  test("usage 带出 byAgent 分账", () => {
    const c = seedFromHistory([], undefined, {
      promptTokens: 10,
      completionTokens: 20,
      totalTokens: 30,
      byAgent: { coder: { promptTokens: 4, completionTokens: 6, totalTokens: 10 } },
    })
    expect(c.usage?.totalTokens).toBe(30)
    expect(c.usage?.byAgent?.coder?.totalTokens).toBe(10)
  })

  test("交错的工具结果并回自己的那条消息", () => {
    const c = seedFromHistory([
      { id: "m1", role: "assistant", content: "main plan", name: "main" },
      {
        id: "m2",
        role: "assistant",
        content: "",
        name: "coder",
        toolCalls: [{ id: "call_a", type: "function", function: { name: "write_file", arguments: "{}" } }],
      },
      {
        id: "m3",
        role: "assistant",
        content: "",
        name: "reviewer",
        toolCalls: [{ id: "call_b", type: "function", function: { name: "read_file", arguments: "{}" } }],
      },
      { id: "m4", role: "tool", content: "A 的结果", toolCallId: "call_a" },
    ])

    const coder = c.messages.find((m) => m.agent === "coder")!
    const reviewer = c.messages.find((m) => m.agent === "reviewer")!
    expect(coder.parts[0]!.kind === "tool" && coder.parts[0]!.result).toBe("A 的结果")
    expect(reviewer.parts[0]!.kind === "tool" && reviewer.parts[0]!.result).toBeUndefined()
  })

  test("pendingApproval 落成 awaiting-approval", () => {
    const c = seedFromHistory([], [{ interruptId: "i1", reason: "tool_call", message: "need ok", toolCallId: "call_1" }])
    expect(c.status).toBe("awaiting-approval")
    expect(c.interrupts[0]!.id).toBe("i1")
    expect(c.interrupts[0]!.toolCallId).toBe("call_1")
  })

  test("多个待批全部重建", () => {
    const c = seedFromHistory([], [
      { interruptId: "i1", reason: "tool_call", toolName: "write_file" },
      { interruptId: "i2", reason: "tool_call", toolName: "execute" },
      { interruptId: "i3", reason: "tool_call", toolName: "edit_file" },
    ])
    expect(c.status).toBe("awaiting-approval")
    expect(c.interrupts.map((i) => i.id)).toEqual(["i1", "i2", "i3"])
  })

  test("pendingApproval 的请求方被带进来（subagentRunId 优先）", () => {
    const c = seedFromHistory([], [{
      interruptId: "i2",
      reason: "tool_call",
      requestedByAgent: "main",
      subagentRunId: "worker",
    }])
    expect(c.interrupts[0]!.requestedByAgent).toBe("worker")
  })

  test("只有 requestedByAgent 时也带进来", () => {
    const c = seedFromHistory([], [{
      interruptId: "i3",
      reason: "tool_call",
      requestedByAgent: "researcher",
    }])
    expect(c.interrupts[0]!.requestedByAgent).toBe("researcher")
  })

  test("没有待审批时是 idle", () => {
    expect(seedFromHistory([]).status).toBe("idle")
  })
})

describe("seedFromHistory 的 id 唯一性", () => {
  test("id 重复时自动去重，不产生重复 key", () => {
    const dup: AgMessage[] = [
      { id: "msg-same", role: "assistant", content: "" },
      { id: "msg-same", role: "assistant", content: "" },
      { id: "msg-same", role: "assistant", content: "" },
    ]
    const c = seedFromHistory(dup)
    const ids = c.messages.map((m) => m.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  test("id 缺失时也能给出唯一 key", () => {
    const c = seedFromHistory([
      { id: "", role: "user", content: "a" },
      { id: "", role: "user", content: "b" },
    ])
    const ids = c.messages.map((m) => m.id)
    expect(ids.every(Boolean)).toBe(true)
    expect(new Set(ids).size).toBe(ids.length)
  })

  test("内容不同且 id 不同时保持原样", () => {
    const c = seedFromHistory([
      { id: "m1", role: "user", content: "a" },
      { id: "m2", role: "assistant", content: "b" },
    ])
    expect(c.messages.map((m) => m.id)).toEqual(["m1", "m2"])
  })

  test("去重是确定性的：同样输入得到同样 id", () => {
    const dup: AgMessage[] = [
      { id: "x", role: "assistant", content: "" },
      { id: "x", role: "assistant", content: "" },
    ]
    expect(seedFromHistory(dup).messages.map((m) => m.id)).toEqual(
      seedFromHistory(dup).messages.map((m) => m.id),
    )
  })
})

describe("活动 agent —— meta 优先于暂存值", () => {
  test("欢迎页默认 main，选了之后读暂存值", () => {
    expect(selectActiveAgentId(useStore.getState())).toBe(MAIN_AGENT)
    useStore.setState({ pendingAgentId: "worker" })
    expect(selectActiveAgentId(useStore.getState())).toBe("worker")
  })

  test("已有会话读 threadMeta.agentId，暂存值不参与", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({
      pendingAgentId: "worker",
      sessions: {
        ...s.sessions,
        [A]: { ...s.sessions[A]!, meta: { ...s.sessions[A]!.meta, agentId: "researcher" } },
      },
    }))
    expect(selectActiveAgentId(useStore.getState())).toBe("researcher")
  })

  test("欢迎页切换只暂存，不发请求", async () => {
    fetchCalls = []
    await useStore.getState().switchAgent("worker")
    expect(useStore.getState().pendingAgentId).toBe("worker")
    expect(fetchCalls).toEqual([])
  })

  test("已有会话切换走 PATCH 并把结果写回 meta", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    await useStore.getState().switchAgent("worker")

    expect(fetchCalls).toContain(`PATCH /v2/sessions/${A}/agent`)
    expect(useStore.getState().sessions[A]!.meta.agentId).toBe("worker")
    expect(selectActiveAgentId(useStore.getState())).toBe("worker")
  })
})

describe("stop —— 中止当前轮次", () => {
  test("会 POST 服务端的中止端点", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    fetchCalls = []

    await useStore.getState().stop()

    expect(fetchCalls).toContain(`POST /v2/sessions/${A}/abort`)
  })

  test("中止后状态回到 idle", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    useStore.setState((s) => ({
      sessions: {
        ...s.sessions,
        [A]: { ...s.sessions[A]!, convo: { ...s.sessions[A]!.convo!, status: "running" } },
      },
    }))

    await useStore.getState().stop()

    expect(useStore.getState().sessions[A]!.convo!.status).toBe("idle")
  })

  test("中止失败要出声（不能界面 idle 而那一轮还在跑）", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    const real = globalThis.fetch
    globalThis.fetch = (async (input: any, init?: RequestInit) => {
      const url = typeof input === "string" ? input : String(input?.url ?? "")
      if (url.endsWith("/abort")) return new Response("boom", { status: 500 })
      return real(input, init)
    }) as typeof fetch

    try {
      await useStore.getState().stop()
    } finally {
      globalThis.fetch = real
    }

    expect(useStore.getState().sessions[A]!.convo!.status).toBe("error")
  })
})

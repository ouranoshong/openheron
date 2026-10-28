import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import {
  MAIN_AGENT,
  seedFromHistory,
  selectActiveAgentId,
  useStore,
  type StoreState,
} from "../src/lib/store"
import type { AgMessage } from "../src/lib/api"

/**
 * store 的行为测试。
 *
 * ★ 这里测的是**脚本级看不到、但用户天天遇到**的那几件事：
 *   切换会话串内容、刷新回到错的会话、发一条多一个条目。
 *   Phase 0 的 E2E 已经把这三条钉成红的；这里从状态层再钉一遍，
 *   重写 UI 时有个不依赖浏览器的回归网。
 */

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

/**
 * bun 的测试环境没有浏览器的 localStorage —— 而 active-thread.ts 直接
 * 用它，于是 import store 就会在模块求值时炸掉。装一个内存版的。
 *
 * 与 scripts/ 下的冒烟脚本用同一套做法。
 */
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

/**
 * ★ 两个可切换的开关，用来复现真实服务端会遇到的两种情况：
 *
 *   emptyList —— 服务端一个会话都没有（状态目录被清过 / 换了机器）；
 *   missing   —— 某些会话真的不存在（删过）。
 */
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

// ── 列表 ──────────────────────────────────────────────────────────────────

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

  // ★ 服务端一个会话都没有 ⇒ localStorage 里记着的那个 id 一定是悬空的。
  //   不清掉的话，每次刷新都去打那个不存在的会话 → 404 → 界面甩一条
  //   「session not found」，而用户什么都没做错。
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

// ── 切换 ──────────────────────────────────────────────────────────────────

describe("openSession —— Phase 0 钉红的三个缺陷之一", () => {
  test("切到 B 之后只看到 B 的消息", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    expect(JSON.stringify(useStore.getState().sessions[A]!.convo)).toContain("MARKER-ALPHA")

    await useStore.getState().openSession(B)

    const b = useStore.getState().sessions[B]!.convo!
    expect(JSON.stringify(b)).toContain("MARKER-BRAVO")
    // ★ 核心断言：A 的内容不许残留在 B 的会话里。
    expect(JSON.stringify(b)).not.toContain("MARKER-ALPHA")
  })

  test("加载前先清空，不闪上一个会话的内容", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)
    // 不 await：正处在「已清空、还没拿到 B」的那个瞬间
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

  // ★ 记住的会话**没了**（删过 / 状态目录被清过）：清掉记住的 id，回欢迎
  //   界面 —— 而不是把 404 原样摔给用户。他删的、或者它没的，两条路他都
  //   没法「重试」。
  test("会话没了就清掉记住的 id，回欢迎界面", async () => {
    // 模拟启动：localStorage 里记着一个已经不存在的会话。
    missing.add("gone")
    localStorage.setItem("openheron.activeThread", "gone")
    useStore.setState({ activeId: "gone" })

    await useStore.getState().openSession("gone")

    expect(localStorage.getItem("openheron.activeThread")).toBeNull()
    expect(useStore.getState().activeId).toBeNull()
    expect(useStore.getState().sessions["gone"]).toBeUndefined()
    // 不甩错误：那不是用户能处理的事。
    expect(useStore.getState().sessions["gone"]?.convo?.error).toBeUndefined()
  })
})

// ── 新建 ──────────────────────────────────────────────────────────────────

describe("startNewThread —— Phase 0 钉红的第三个缺陷", () => {
  test("回到欢迎界面且不写 localStorage", async () => {
    await useStore.getState().refreshList()
    await useStore.getState().openSession(A)

    useStore.getState().startNewThread()

    expect(useStore.getState().activeId).toBeNull()
    // ★ 不清 localStorage：刷新页面仍应回到上一个对话。
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

// ── 改名 / 删除 ───────────────────────────────────────────────────────────

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
    // ★ id 带**段序号**（m1:t0）—— 与实时流同一套方案（events.ts 的 ensurePart）。
    //   两条路不一致的话，一条消息里出现两个正文段时 id 会撞车。
    expect(c.messages[0]!.parts[0]).toEqual({ kind: "text", id: "m1:t0", text: "hi", complete: true })
  })

  test("历史里的思维链重建为 reasoning part（带用时）", () => {
    // ★ 后端把推理正文与用时放在 metadata 里（api/v2_convert.go）——
    //   事件流不落盘，不存下来就没有「刷新后想了多久」。
    const c = seedFromHistory([
      {
        id: "m1",
        role: "assistant",
        content: "答完了",
        metadata: { reasoning: "先看看这个仓库", reasoningMs: 20258 },
      },
    ])
    const parts = c.messages[0]!.parts
    // ★ 思维链在正文**之前** —— 实时流里就是这个顺序（模型先想、再答）。
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
    // 历史工具卡默认折叠；实时 reducer 不设这个标记。
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

  // 多 agent 模式：历史里的每条消息要带着「是谁产出的」。
  test("消息带出 agent 归属", () => {
    const c = seedFromHistory([
      { id: "m1", role: "assistant", content: "planning", name: "main" },
      { id: "m2", role: "assistant", content: "patching", name: "coder" },
    ])
    expect(c.messages[0]!.agent).toBe("main")
    expect(c.messages[1]!.agent).toBe("coder")
  })

  // 没有归属标记的老消息不猜 —— 空就是「不知道是谁」。
  test("没有归属标记的消息不编造 agent", () => {
    const c = seedFromHistory([{ id: "m1", role: "user", content: "hi" }])
    expect(c.messages[0]!.agent).toBeUndefined()
  })

  // 用量分账要跟着历史一起回来 —— 否则打开会话看不到「谁花的」。
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

  // ★ 并行时事件是**交错**的：A 的工具结果可能排在 B 的 assistant 消息
  //   之后。原来用「最后一条」匹配，会把 A 的结果并进 B 的消息 ——
  //   界面上 B 的工具卡显示 A 的输出，按 agent 过滤时 A 的活儿挂在了 B 名下。
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

    // m4 属于 m2（coder），不能挂到 m3（reviewer）身上。
    const coder = c.messages.find((m) => m.agent === "coder")!
    const reviewer = c.messages.find((m) => m.agent === "reviewer")!
    expect(coder.parts[0]!.kind === "tool" && coder.parts[0]!.result).toBe("A 的结果")
    expect(reviewer.parts[0]!.kind === "tool" && reviewer.parts[0]!.result).toBeUndefined()
  })

  test("pendingApproval 落成 awaiting-approval", () => {
    // ★ 关键：重启或切换回来之后，审批挂起必须还在。
    //   上一版把它寄生在某条消息的 metadata 上，换个顺序就找不到了。
    const c = seedFromHistory([], [{ interruptId: "i1", reason: "tool_call", message: "need ok", toolCallId: "call_1" }])
    expect(c.status).toBe("awaiting-approval")
    expect(c.interrupts[0]!.id).toBe("i1")
    expect(c.interrupts[0]!.toolCallId).toBe("call_1")
  })

  // ★ 一轮里可以有**多个**独立待批（模型一次请求四个都要审批的工具）。
  //   重建时少一个就答不了它 —— 而它会一直挂在界面上，看起来像「点了没反应」。
  test("多个待批全部重建", () => {
    const c = seedFromHistory([], [
      { interruptId: "i1", reason: "tool_call", toolName: "write_file" },
      { interruptId: "i2", reason: "tool_call", toolName: "execute" },
      { interruptId: "i3", reason: "tool_call", toolName: "edit_file" },
    ])
    expect(c.status).toBe("awaiting-approval")
    expect(c.interrupts.map((i) => i.id)).toEqual(["i1", "i2", "i3"])
  })

  // ★ 刷新后恢复的审批也要保留请求方，否则「谁在等」只在实时那一轮可见。
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
  /**
   * ★ React 的 key 必须唯一，不能指望上游永远给对。
   *
   * 后端已经修了一处真实缺陷（一轮里两次工具调用产出两条空内容 assistant
   * 消息，按内容取摘要时 id 相同），但客户端仍然要能扛住坏数据 ——
   * 否则服务端一次回归就是用户控制台里一片红字。
   */
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

// ── 活动 agent ───────────────────────────────────────────────────────────

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
    // ★ 两个来源同时存在时，服务端那份赢：它才是下一轮真的会用到的值。
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
  /**
   * ★ 「停止」必须**打服务端的中止端点**。
   *
   *   只断开本地那条 SSE 连接**不等于**中止：服务端那一轮会继续跑到底
   *   —— 模型继续烧 token、工具继续执行副作用（文件已写、命令已跑），
   *   而界面已经显示 idle。那是「假停止」，比没有停止按钮更糟。
   *
   *   这条从状态层钉住：请求**发出去了**。E2E 那条钉的是真浏览器里
   *   同样的事（它还额外验了界面回到可输入、没有停不下来的转圈）。
   */
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

    // ★ 状态必须是「出错」而不是「idle」—— 那一轮其实还在跑。
    expect(useStore.getState().sessions[A]!.convo!.status).toBe("error")
  })
})

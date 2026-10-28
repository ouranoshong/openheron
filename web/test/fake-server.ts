/**
 * 假后端：给 E2E 测试提供确定性的 `/v2`。
 *
 * ★ 为什么不打真openheron：
 *
 *   渲染层测试必须确定。现有 `smoke:hitl` 有约 1/5 的抖动 ——
 *   取决于模型这一次会不会调 `write_file`。用它当断言，测试失败时
 *   分不清是界面坏了还是模型没听话，于是测试会被"重跑一次"，
 *   然后慢慢变成没人相信的东西。
 *
 *   这里把 AG-UI 事件序列写死并按场景回放，于是「点发送 → 界面出现
 *   工具卡 → 出现审批卡」这条链路每次都一样。
 *
 * ★ 帧格式必须与真后端一致：
 *
 *   `sseio.Stream.Write` 按 `\n` 切行，每行包成一个 SSE 事件，
 *   hertz 写出的是 `data:{json}` —— **冒号后没有空格**。
 *   早先前端踩过这个坑：按 `data: ` 严格匹配的解析器会静默收空，
 *   表现是「流式内容一条都不出」。这里照抄真后端的写法。
 */

export interface FakeThread {
  threadId: string
  title: string
  createdAt: string
  updatedAt: string
  messages: FakeMessage[]
  state: "idle" | "running" | "waiting_approval"
  /** 会话的根 agent；缺省 = main（与真后端一致）。 */
  agentId?: string
  /**
   * 尚未答复的审批；非空时 /messages 会带 pendingApprovals。
   *
   * ★ 是**列表**：一轮里可以有多个独立待批（模型一次请求四个都要审批的
   *   工具），每个都要有自己的 id。
   */
  pendings?: PendingApproval[]
  usage?: Usage
  /** 已上传的附件（真后端落在会话的 workspace 目录里）。 */
  files: { name: string; path: string; size: number }[]
}

export interface FakeMessage {
  id: string
  role: "system" | "developer" | "user" | "assistant" | "tool"
  content: string
  /**
   * 产出这条消息的 agent 名（AG-UI 的「发送者名字」）。
   *
   * ★ 与真后端一致：后端 api/v2_convert.go 把它填成归属标记。
   */
  name?: string
  /** role=tool 时指回它回答的那个工具调用。 */
  toolCallId?: string
  toolCalls?: { id: string; type: "function"; function: { name: string; arguments: string } }[]
  toolCallResults?: unknown[]
  /**
   * AG-UI 的通用 metadata。
   *
   * ★ 与真后端一致：`createdAt` 是产生时刻，`reasoning` / `reasoningMs` 是
   *   思维链的正文与用时（后端算好存在归档消息的 Extra 里，历史端点带出来）。
   */
  metadata?: Record<string, unknown>
}

interface PendingApproval {
  interruptId: string
  reason: string
  message?: string
  toolCallId?: string
  toolName?: string
  /** 与真后端 pendingView.arguments 一致：结构化参数。 */
  arguments?: unknown
  /** 子 agent 名；根 agent 的审批不带（与 agtransport 的归属逻辑一致）。 */
  subagentRunId?: string
}

interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens: number
  cachedTokens: number
  /** 用量按 agent 分账（与真后端 usageView.byAgent 同形）。 */
  byAgent?: Record<string, Usage>
}

export interface FakeServer {
  url: string
  stop(): void
  /** 供测试断言用：收到过的所有请求（含 method 与 path）。 */
  requests: string[]
  /** 当前会话表，测试可以直接断言。 */
  threads(): FakeThread[]
  reset(): void
  /** 切换回放场景。测试里用，不经过产品代码。 */
  setScenario(name: ScenarioName): void
}

// ── AG-UI 事件 ────────────────────────────────────────────────────────────
//
// 事件字段名与 `api/v2_types.go` 的 DTO 一一对齐；类型名沿用
// `agtransport` 实际发出的那套（见 transport.go 的 emit* 函数）。

interface RawInterrupt {
  id: string
  reason: string
  message?: string
  toolCallId?: string
}

interface RunEvent {
  type: string
  /** 只有 RUN_FINISHED 带。区分 success / interrupt。 */
  outcome?: { type: string; interrupts?: RawInterrupt[]; result?: unknown }
  [k: string]: unknown
}

function started(threadId: string, runId: string): RunEvent {
  return { type: "RUN_STARTED", threadId, runId }
}

/**
 * TEXT_MESSAGE_START。
 *
 * ★ `name` 是**产出这条消息的 agent**（AG-UI 的「发送者名字」）——
 *   后端从 2026-10-22 起发这个字段，多 agent 模式靠它归属。
 */
function textStart(messageId: string, role: string, name = "main"): RunEvent {
  return { type: "TEXT_MESSAGE_START", messageId, role, name }
}

/**
 * 声明：一条有内容、却**没有正文段**的消息（只有工具调用或只有推理）
 * 在末尾补的归属声明。
 *
 * ★ 与真后端同形（agtransport/transport.go 的 emitter.announce）：
 *   补在**末尾**，因为 AG-UI 的 ValidateSequence 规定一条消息一对
 *   START/END，消息开着时再发 START 会被判 "already started"。
 */
function announce(messageId: string, name = "main"): RunEvent[] {
  return [
    { type: "TEXT_MESSAGE_START", messageId, role: "assistant", name },
    { type: "TEXT_MESSAGE_END", messageId },
  ]
}

function textDelta(messageId: string, delta: string): RunEvent {
  return { type: "TEXT_MESSAGE_CONTENT", messageId, delta }
}

function textEnd(messageId: string): RunEvent {
  return { type: "TEXT_MESSAGE_END", messageId }
}

// reasoning 的三个事件。
//
// ★ 存在的理由：未收尾的 part 在界面上是可见的 —— reasoning 没收尾时
//   标题是「Thinking…」，收尾后是「Thinking」。而 text part 收不收尾
//   看不出来，所以要断言「停止后没有停不下来的转圈」就得用它。
function reasoningStart(messageId: string, timestamp?: number): RunEvent {
  return { type: "REASONING_START", messageId, ...at(timestamp) }
}

function reasoningDelta(messageId: string, delta: string, timestamp?: number): RunEvent {
  return { type: "REASONING_MESSAGE_CONTENT", messageId, delta, ...at(timestamp) }
}

/**
 * 思维链**收尾**。
 *
 * ★ 存在的理由：之前没有任何场景把思维链**收完**过 —— `slowRun` 是刻意
 *   不收尾的（测「停止」按钮）。于是「结束后标题变成 Thought 并带上用时」
 *   这条路径根本没有输入可测。
 */
function reasoningEnd(messageId: string, timestamp?: number): RunEvent[] {
  return [
    { type: "REASONING_MESSAGE_END", messageId, ...at(timestamp) },
    { type: "REASONING_END", messageId, ...at(timestamp) },
  ]
}

/**
 * 只有显式给了时刻才写 timestamp —— 没给就由 encode 补一个当前时刻。
 *
 * ★ 显式给是为了**让用时可断言**：思维链的开始与结束之间差多少，
 *   在假后端里是写死的（不用真等 1.5 秒）。
 */
function at(timestamp?: number): { timestamp?: number } {
  return timestamp === undefined ? {} : { timestamp }
}

function toolStart(toolCallId: string, name: string, parentMessageId: string): RunEvent {
  return { type: "TOOL_CALL_START", toolCallId, toolCallName: name, parentMessageId }
}

function toolArgs(toolCallId: string, argsJson: string): RunEvent {
  return { type: "TOOL_CALL_ARGS", toolCallId, delta: argsJson }
}

function toolEnd(toolCallId: string): RunEvent {
  return { type: "TOOL_CALL_END", toolCallId }
}


function finished(threadId: string, runId: string): RunEvent {
  return { type: "RUN_FINISHED", threadId, runId, outcome: { type: "success" } }
}

function interrupted(threadId: string, runId: string, p: PendingApproval | PendingApproval[]): RunEvent {
  const list = Array.isArray(p) ? p : [p]
  return {
    type: "RUN_FINISHED",
    threadId,
    runId,
    outcome: {
      type: "interrupt",
      // ★ 每个待批都带**自己的** id —— 真后端就是这么发的
      //   （agtransport/transport.go 的 interruptsFrom）。
      interrupts: list.map((one) => ({
        id: one.interruptId,
        // ★ reason 用 "tool_call"：真后端 interruptsFrom 里写死的就是它
        //   （agtransport/transport.go）。别用 "needs_approval" 之类自造值。
        reason: "tool_call",
        message: one.message,
        toolCallId: one.toolCallId,
        // ★ 与真后端一致：工具名与参数放在 AG-UI 的 metadata 里，
        //   前端审批卡不再靠解析 message 猜工具名。
        ...(one.toolName || one.arguments !== undefined
          ? {
              metadata: {
                ...(one.toolName ? { toolName: one.toolName } : {}),
                ...(one.arguments !== undefined ? { arguments: one.arguments } : {}),
              },
            }
          : {}),
        ...(one.subagentRunId ? { subagentRunId: one.subagentRunId } : {}),
      })),
    },
  }
}

function runError(code: string, message: string): RunEvent {
  return { type: "RUN_ERROR", code, message }
}

const USAGE: Usage = {
  promptTokens: 100,
  completionTokens: 50,
  totalTokens: 150,
  reasoningTokens: 0,
  cachedTokens: 0,
}

// ── 场景 ──────────────────────────────────────────────────────────────────
//
// 每个场景是一段 AG-UI 事件序列。测试用 ?scenario=xxx 选择。
// 序列固定 ⇒ 渲染结果固定。

export const SCENARIOS = {
  /** 最小可跑场景：一问一答。 */
  simple: ["A"],

  /** 两问：用来验证「切换会话后加载的是另一个会话的消息」。 */
  twoQuestions: ["A", "B"],

  /** 一问 + 工具调用 + 审批中断（RUN_FINISHED 带 interrupt outcome）。 */
  approval: ["A"],

  /**
   * 一问一答，但**流不结束**。
   *
   * ★ 存在的理由：停止按钮只在轮次 running 时出现，而别的场景流一瞬间就
   *   结束了，按钮早没了 —— 「轮次真的在跑」才点得到停止。
   */
  slowRun: ["A"],

  /**
   * 一问 + **多个**独立待批。
   *
   * ★ 存在的理由：模型一次请求几个都要审批的工具，就是几个独立的待批，
   *   每个带自己的 id。而框架的契约是「不在 Targets 里的被中断组件会
   *   重新中断自己」，重新中断拿到的是**新 id** —— 所以答复必须**一次
   *   答完整组**。逐个发的话，答完第一个其余的 id 就全废了。
   */
  approvalMulti: ["A"],

  /** 流式：多段 delta，用来验证增量渲染而不是最后一次性出现。 */
  streaming: ["A"],

  /**
   * **两个 agent 的内容交错**流过来。
   *
   * ★ 存在的理由：多 agent 模式下内容是交错的，而「交错」正是串号的
   *   温床 —— 每条消息有自己的 messageId，delta 按 messageId 归约，
   *   所以 coder 的字不会落进 reviewer 的消息里。
   *
   * ★ 还验「归属在实时流里就生效」：过滤条要在**流还没结束**时就出现
   *   （历史回放路径一直是对的，实时路径曾经是错的）。
   */
  multiAgentStream: ["A"],

  /** 触发 RUN_ERROR，验证失败有可见提示而不是静默。 */
  error: ["A"],

  /** 回复里带代码块与行内代码，验证 markdown 渲染与语法高亮。 */
  code: ["A"],

  /** 回复里带 GFM 表格，验证宽表格在消息列内滚动而不是撑破布局。 */
  table: ["A"],

  /** 回复里带无序、有序、嵌套与任务清单，验证 list-style 没有被 preflight 清掉。 */
  lists: ["A"],

  /** 中英混排：标题层级、行高、引用、分割线与长链接换行。 */
  typography: ["A"],

  /**
   * 长回复 + 慢速增量，用来验证**滚动跟随**。
   *
   * ★ 内容必须超出视口高度，否则「跟到底部」和「本来就在底部」分不开 ——
   *   那样测出来的「通过」什么也证明不了。
   */
  longStream: ["A"],

  /**
   * 每次一大块（整段），用来暴露「跟随被自己打断」的那类 bug。
   *
   * ★ 与 longStream 的区别正是这个：longStream 每次只加一个字符，
   *   单次渲染的高度增长远小于「距底部阈值」，所以测不出问题；
   *   真实模型的 delta 常是整个词/整句话，一次渲染就能涨好几行。
   */
  bigChunks: ["A"],

  /**
   * 一次**跑完**的工具调用（带结果），用来验证工具卡的结果区。
   *
   * ★ 与 approval 场景的区别：approval 被打断，只有 START/ARGS/END，
   *   没有 RESULT。工具卡的结果区根本不会出现。
   */
  toolDone: ["A"],

  /** 工具结果是 **JSON** —— 与 toolDone 相反，验证结果区的着色判定。 */
  toolJson: ["A"],

  /**
   * 思维链**想完**了：收尾之后标题该变成「Thought …」。
   *
   * ★ 存在的理由：思维链的收尾事件此前**没有任何场景发过**（slowRun 是
   *   刻意不发的），于是「结束后更新成 Thought + 用时」这条路径无从测。
   */
  reasoningDone: ["A"],
} as const

export type ScenarioName = keyof typeof SCENARIOS

/** 固定 id，让断言可写死。 */
export const THREAD_A = "11111111-1111-4111-8111-111111111111"
export const THREAD_B = "22222222-2222-4222-8222-222222222222"

/**
 * 一个历史很长的会话。
 *
 * ★ 存在的理由：验证「打开应用 / 切到长历史会话时视图停在最新一条」。
 *   这类场景下消息是**异步加载**的 —— 首屏 0 条，加载完一次撑满。
 *   历史太短的话根本没有滚动空间，什么都测不出来。
 */
export const THREAD_LONG = "33333333-3333-4333-8333-333333333333"

/** 一个已存在但还没有任何消息的会话（「新建之后还没说话」那种状态）。 */
export const THREAD_EMPTY = "44444444-4444-4444-8444-444444444444"
export const THREAD_HISTORY = "55555555-5555-4555-8555-555555555555"

/**
 * 一个**多 agent** 会话：消息分属两个 agent，且带用量分账。
 *
 * ★ 存在的理由：验证「按 agent 过滤」与「分账展示」——
 *   多 agent 模式下消息流是交错的，没有这个夹具就没法在浏览器里看见
 *   过滤条与分账 chip。
 */
export const THREAD_MULTI = "66666666-6666-4666-8666-666666666666"

/** A 会话的权威历史 —— 切换到 B 后不该看到这些内容。 */
export const MARKER_A = "MARKER-ALPHA"
export const MARKER_B = "MARKER-BRAVO"
export const MARKER_LONG = "MARKER-LONGCVT"
export const MARKER_HISTORY = "MARKER-HISTORY"
export const MARKER_MULTI_MAIN = "MARKER-MULTI-MAIN"
export const MARKER_MULTI_CODER = "MARKER-MULTI-CODER"

/** Mirrors api/commands.go — names and kinds are what the UI branches on. */
/**
 * 假 skill 清单。
 *
 * ★ 与真后端同形：name + description + 执行模式 —— description 是模型
 *   选不选它的依据，也是选择器里最想看的那一行。
 */
const FAKE_SKILLS = [
  {
    name: "refactor",
    description: "重构既有代码：先读懂，再小步改，每步都跑验证。",
    context: "inline",
  },
  {
    name: "research",
    description: "调研一个主题：检索、交叉验证、给出带出处的结论。",
    context: "fork",
    agent: "worker",
  },
  {
    name: "review-docs",
    description: "审阅文档的准确性与结构。",
    context: "inline",
  },
]

const FAKE_COMMANDS = [
  { name: "new", description: "Start a new thread", kind: "action", action: "new_thread" },
  { name: "stop", description: "Stop the current run", kind: "action", action: "stop_run" },
  { name: "attach", description: "Attach files to this thread", kind: "action", action: "attach_files" },
  { name: "settings", description: "Open configuration", kind: "action", action: "open_settings" },
  {
    name: "agents",
    description: "Switch the active agent",
    kind: "action",
    action: "open_agents",
    argumentHint: "[agent]",
  },
  {
    name: "skill",
    description: "Use a named skill for this task",
    kind: "prompt",
    argumentHint: "<skill> [task]",
  },
  {
    name: "review",
    description: "Review the current changes or given paths",
    kind: "prompt",
    argumentHint: "[path]",
  },
  {
    name: "test",
    description: "Run the relevant tests and summarize failures",
    kind: "prompt",
    argumentHint: "[path]",
  },
  {
    name: "explain",
    description: "Explain how the given code works",
    kind: "prompt",
    argumentHint: "<path or symbol>",
  },
  {
    name: "fix",
    description: "Find the root cause and fix the given problem",
    kind: "prompt",
    argumentHint: "<problem>",
  },
]

/**
 * `/v2/agents` 的固定拓扑。
 *
 * ★ 下属故意排在前面：底栏必须按 `defaultAgent` 选，不能取 `agents[0]`。
 *   用 E2E 把这条顺序假设钉住。
 */
const FAKE_AGENTS = {
  defaultAgent: "main",
  maxDepth: 1,
  // ★ 配置指纹（真后端是 config.Version）：前端靠它知道「配置变了」。
  version: "fake-v1",
  agents: [
    {
      id: "worker",
      description: "Worker agent",
      model: {
        provider: "openai",
        model: "fast",
        reasoning: "low",
        reasoningAvailable: ["high", "low"],
        reasoningDefault: "low",
      },
      maxIterations: 20,
      subagents: [],
      disabledTools: [],
    },
    {
      id: "main",
      description: "Root agent",
      model: {
        provider: "deepseek",
        model: "deepseek-v4",
        reasoning: "high",
        reasoningAvailable: ["high", "low", "off"],
        reasoningDefault: "off",
      },
      maxIterations: 40,
      subagents: ["worker"],
      disabledTools: [],
    },
  ],
}

/**
 * 多层拓扑的 `/v2/agents` 视图。
 *
 * ★ main → lead → coder：**每个** agent 都能当会话的 agent（嵌套多深都
 *   行），所以这里没有「候选名单」—— 列表就是全部。
 */
const FAKE_AGENTS_MULTI = {
  defaultAgent: "main",
  maxDepth: 2,
  version: "fake-multi-v1",
  agents: [
    {
      id: "coder",
      description: "Coder agent",
      model: { provider: "openai", model: "fast", reasoning: "low", reasoningAvailable: ["low"], reasoningDefault: "low" },
      maxIterations: 20,
      subagents: [],
      disabledTools: [],
    },
    {
      id: "lead",
      description: "Lead agent",
      model: { provider: "deepseek", model: "deepseek-v4", reasoning: "high", reasoningAvailable: ["high", "off"], reasoningDefault: "off" },
      maxIterations: 40,
      subagents: ["coder"],
      disabledTools: [],
    },
    {
      id: "main",
      description: "Root agent",
      model: { provider: "deepseek", model: "deepseek-v4", reasoning: "high", reasoningAvailable: ["high", "off"], reasoningDefault: "off" },
      maxIterations: 40,
      subagents: ["lead"],
      disabledTools: [],
    },
  ],
}

/**
 * 一大堆 agent 的 `/v2/agents` 视图。
 *
 * ★ 用来把选择器的列表**撑到溢出**（`max-h-72`）：列表滚动、高亮项跟随
 *   滚动这些行为，列表不溢出时根本看不出来。
 */
const FAKE_AGENTS_MANY = {
  defaultAgent: "main",
  maxDepth: 1,
  version: "fake-many-v1",
  agents: Array.from({ length: 12 }, (_, i) => ({
    id: i === 0 ? "main" : i === 1 ? "worker" : `agent-${i}`,
    description: `Agent ${i}`,
    model: { provider: "openai", model: "fast", reasoning: "low", reasoningAvailable: ["low"], reasoningDefault: "low" },
    maxIterations: 20,
    subagents: [],
    disabledTools: [],
  })),
}

/**
 * 「配置热更新之后」的 `/v2/agents` 视图：默认那份**多了一个** agent。
 *
 * ★ 模拟用户在 `.openheron/agents/` 里加了一个配置文件 —— 服务端重新加载了配置，
 *   名单变长，指纹也变了。前端此前拿不到任何信号，于是弹窗里还是旧名单。
 */
const FAKE_AGENTS_GREW = {
  defaultAgent: "main",
  maxDepth: 1,
  // ★ 指纹必须**跟着配置一起变** —— 那是前端唯一的判据。
  version: "fake-v2",
  agents: [
    ...FAKE_AGENTS.agents,
    {
      id: "latecomer",
      description: "Agent added after a config reload",
      model: { provider: "openai", model: "fast", reasoning: "low", reasoningAvailable: ["low"], reasoningDefault: "low" },
      maxIterations: 20,
      subagents: [],
      disabledTools: [],
    },
  ],
}

/**
 * fakeAgentIds 是**所有模式**下假后端认识的 agent 名。
 *
 * ★ 与真后端同一条规则：声明了的都能选 —— 所以校验不该按模式区分。
 */
const fakeAgentIds = (): Set<string> =>
  new Set(
    [
      ...FAKE_AGENTS.agents,
      ...FAKE_AGENTS_MULTI.agents,
      ...FAKE_AGENTS_MANY.agents,
      ...FAKE_AGENTS_GREW.agents,
    ].map((a) => a.id),
  )

const FAKE_PROJECT_FILES = [
  "web/src/components/composer.tsx",
  "docs/frontend-design.md",
  "kb/store.go",
  "docs/a b.md",
  "web/src/lib/api.ts",
]

const T0 = "2026-10-12T10:00:00Z"
const T1 = "2026-10-12T10:05:00Z"

function seedThreads(): FakeThread[] {
  const longMessages = Array.from({ length: 14 }, (_, i) => [
    { id: `l-u${i}`, role: "user" as const, content: `${MARKER_LONG} 第 ${i + 1} 个问题` },
    { id: `l-a${i}`, role: "assistant" as const, content: `${MARKER_LONG} 第 ${i + 1} 个回答，内容要够长才能撑出滚动条。` },
  ]).flat()

  // 一轮里包含工具调用与两条 assistant 消息，用来钉住：
  //   · 历史工具卡默认折叠
  //   · 同一轮只显示一次角色标签
  //   · 用户消息的换行按字面保留
  const toolResult = Array.from({ length: 80 }, (_, i) => `tool output line ${i + 1}`).join("\n")
  const historyMessages: FakeMessage[] = [
    {
      id: "h-u1",
      role: "user",
      content: `${MARKER_HISTORY} first line\nsecond line`,
    },
    {
      id: "h-a1",
      role: "assistant",
      content: "",
      toolCalls: [
        {
          id: "h-call-1",
          type: "function",
          function: { name: "read_file", arguments: '{"file_path":"docs/big.txt"}' },
        },
      ],
    },
    { id: "h-t1", role: "tool", content: toolResult, toolCallId: "h-call-1" },
    { id: "h-a2", role: "assistant", content: `${MARKER_HISTORY} answer` },
  ]

  const multiMessages: FakeMessage[] = [
    { id: "m-u1", role: "user", content: `${MARKER_MULTI_MAIN} question` },
    { id: "m-a1", role: "assistant", content: `${MARKER_MULTI_MAIN} planning`, name: "main" },
    { id: "m-a2", role: "assistant", content: `${MARKER_MULTI_CODER} patching`, name: "coder" },
    { id: "m-a3", role: "assistant", content: `${MARKER_MULTI_CODER} verifying`, name: "coder" },
  ]

  return [
    {
      threadId: THREAD_MULTI,
      title: "Multi agent conversation",
      createdAt: T0,
      updatedAt: T1,
      state: "idle" as const,
      files: [],
      messages: multiMessages,
      usage: {
        promptTokens: 900,
        completionTokens: 300,
        totalTokens: 1200,
        reasoningTokens: 0,
        cachedTokens: 0,
        byAgent: {
          main: { promptTokens: 500, completionTokens: 100, totalTokens: 600, reasoningTokens: 0, cachedTokens: 0 },
          coder: { promptTokens: 400, completionTokens: 200, totalTokens: 600, reasoningTokens: 0, cachedTokens: 0 },
        },
      },
    },
    {
      threadId: THREAD_HISTORY,
      title: "History conversation",
      createdAt: T0,
      updatedAt: T1,
      state: "idle" as const,
      files: [],
      messages: historyMessages,
    },
    {
      threadId: THREAD_EMPTY,
      title: "Empty conversation",
      createdAt: T0,
      updatedAt: T0,
      state: "idle" as const,
      files: [],
      messages: [],
    },
    {
      threadId: THREAD_LONG,
      title: "Long conversation",
      createdAt: T0,
      updatedAt: T1,
      state: "idle" as const,
      files: [],
      messages: longMessages,
    },
    {
      threadId: THREAD_A,
      title: "Alpha conversation",
      createdAt: T0,
      updatedAt: T0,
      state: "idle",
      files: [],
      messages: [
        { id: "a-u1", role: "user", content: `${MARKER_A} first question` },
        { id: "a-a1", role: "assistant", content: `${MARKER_A} first answer` },
      ],
    },
    {
      threadId: THREAD_B,
      title: "Bravo conversation",
      createdAt: T1,
      updatedAt: T1,
      state: "idle",
      files: [],
      messages: [
        { id: "b-u1", role: "user", content: `${MARKER_B} only question` },
        { id: "b-a1", role: "assistant", content: `${MARKER_B} only answer` },
      ],
    },
  ]
}

/** 每个 question 标记对应的那一段事件序列。 */
function eventsFor(
  scenario: ScenarioName,
  threadId: string,
  marker: string,
  runId: string,
): RunEvent[] {
  switch (scenario) {
    case "approval":
      // ★ 工具调用与文本用**同一个** messageId。
      //
      //   中断不是存在 agent 上，而是挂在「最后一条 assistant 消息」的
      //   metadata.custom.agui.interrupts 上（AgUiThreadRuntimeCore
      //   .findRequiresActionAssistant 用 findLast 找）。拆成 m-1/m-2
      //   两条消息时，元数据挂在 m-1，而 findLast 返回 m-2 ——
      //   getPendingInterrupts() 于是返回 null，审批卡永远不出现。
      return [
        started(threadId, runId),
        toolStart("call_1", "write_file", "m-1"),
        toolArgs("call_1", JSON.stringify({ file_path: "/tmp/x.txt", content: "hi" })),
        toolEnd("call_1"),
        textStart("m-1", "assistant"),
        textDelta("m-1", "I need approval to write that file."),
        textEnd("m-1"),
        interrupted(threadId, runId, {
          interruptId: "int-1",
          reason: "needs_approval",
          message: "write_file requires approval",
          toolCallId: "call_1",
          toolName: "write_file",
          arguments: { file_path: "/tmp/x.txt", content: "hi" },
          // 子 agent 的审批：卡片必须显示请求方，否则「批的是谁的工具」
          // 无从判断。
          subagentRunId: "worker",
        }),
      ]

    case "approvalMulti":
      // ★ 三个**各自独立**的待批，各带各的 id。
      return [
        started(threadId, runId),
        toolStart("call_1", "write_file", "m-1"),
        toolArgs("call_1", JSON.stringify({ file_path: "/tmp/x.txt", content: "hi" })),
        toolEnd("call_1"),
        textStart("m-1", "assistant"),
        textDelta("m-1", "I need approval for three things."),
        textEnd("m-1"),
        interrupted(threadId, runId, [
          {
            interruptId: "int-1",
            reason: "tool_call",
            message: "write_file requires approval",
            toolCallId: "call_1",
            toolName: "write_file",
            arguments: { file_path: "/tmp/x.txt" },
          },
          {
            interruptId: "int-2",
            reason: "tool_call",
            message: "execute requires approval",
            toolCallId: "call_2",
            toolName: "execute",
            arguments: { command: "go test ./..." },
          },
          {
            interruptId: "int-3",
            reason: "tool_call",
            message: "edit_file requires approval",
            toolCallId: "call_3",
            toolName: "edit_file",
            arguments: { file_path: "/tmp/y.txt" },
          },
        ]),
      ]

    case "slowRun":
      // ★ **不给** textEnd / reasoningEnd / finished —— 流保持打开，轮次
      //   一直 running，用户才点得到「停止」。
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", `${marker} still working`),
        reasoningStart("m-1"),
        reasoningDelta("m-1", "still figuring it out"),
      ]

    case "multiAgentStream": {
      // 两个 agent 交错到达：coder 的第一段 → reviewer 的第一段 →
      // coder 的第二段。内容按 messageId 归约，谁的都不会串。
      return [
        started(threadId, runId),
        textStart("m-coder", "assistant", "coder"),
        textDelta("m-coder", "MARKER-CODER-1 "),
        textStart("m-reviewer", "assistant", "reviewer"),
        textDelta("m-reviewer", "MARKER-REVIEWER-1 "),
        textDelta("m-coder", "MARKER-CODER-2"),
        textEnd("m-coder"),
        textEnd("m-reviewer"),
        finished(threadId, runId),
      ]
    }

    case "streaming": {
      const out: RunEvent[] = [started(threadId, runId), textStart("m-1", "assistant")]
      // 每 3 个字符一段，切成几十段。
      //
      // ★ 为什么要这么碎：测试要断言「增量渲染」—— 也就是流还没结束
      //   时界面上就已经有内容了。段落太少的话，浏览器可能在整段
      //   到达之前就渲染完了，断言就失去意义（测出来的是「最终对」，
      //   而不是「过程对」）。
      const text = `${marker} streamed answer for incremental rendering`
      for (let i = 0; i < text.length; i += 3) out.push(textDelta("m-1", text.slice(i, i + 3)))
      out.push(textEnd("m-1"), finished(threadId, runId))
      return out
    }

    case "bigChunks": {
      const para = Array.from(
        { length: 40 },
        (_, i) => `第 ${i + 1} 段：一次渲染就涨好几行，跟随必须照样生效。`,
      ).join("\n\n")
      // 每 ~120 字发一次，单次高度增长远超 80px
      const chunks: string[] = []
      for (let i = 0; i < para.length; i += 120) chunks.push(para.slice(i, i + 120))
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        ...chunks.map((c) => textDelta("m-1", c)),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    case "toolJson": {
      return [
        started(threadId, runId),
        toolStart("call_1", "execute", "m-1"),
        toolArgs("call_1", JSON.stringify({ command: "ls -la" })),
        toolEnd("call_1"),
        {
          type: "TOOL_CALL_RESULT",
          toolCallId: "call_1",
          messageId: "call_1",
          role: "tool",
          content: JSON.stringify({ exit_code: 0, stdout: "main.go\ngo.mod", duration_ms: 12 }, null, 2),
        },
        finished(threadId, runId),
      ]
    }

    case "toolDone": {
      const msg: RunEvent[] = [
        started(threadId, runId),
        toolStart("call_1", "read_file", "m-1"),
        toolArgs("call_1", JSON.stringify({ file_path: "/tmp/x.txt", line_start: 1, line_end: 20 })),
        toolEnd("call_1"),
        // 故意给**非 JSON** 的结果（真实情形：read_file 返回文件内容、
        // execute 返回命令输出）。highlighter 不该把它当 JSON 着色。
        { type: "TEXT_MESSAGE_START", messageId: "m-2", role: "assistant", name: "main" },
        { type: "TEXT_MESSAGE_CONTENT", messageId: "m-2", delta: "已经读完这个文件。" },
        { type: "TEXT_MESSAGE_END", messageId: "m-2" },
      ]
      return [
        ...msg,
        { type: "TOOL_CALL_RESULT", toolCallId: "call_1", messageId: "call_1", content: "package main\n\nfunc main() {}\n", role: "tool" },
        // 「m-1」只有工具调用、没有正文 → 末尾补声明，归属才不是匿名的。
        ...announce("m-1"),
        finished(threadId, runId),
      ]
    }

    case "longStream": {
      // 分段注入，让每帧都有可观察的滚动位置。
      //
      // ★ 段数与长度都是刻意给足的：内容必须**远超**视口，否则
      //   「滚到顶部」与「在底部」分不开 —— 上滑一次会被误判成还在底部。
      //   第一版只给 12 段、内容仅比视口高 70px，测试红得莫名其妙。
      const para = Array.from(
        { length: 40 },
        (_, i) =>
          `第 ${i + 1} 段：滚动跟随要跟着新内容往下走，否则新回复落在视口之外看不见。` +
          `这一段刻意写长一点，好让它在窄屏上折行，占据真实的垂直空间。`,
      ).join("\n\n")
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        ...para.split("").map((ch) => textDelta("m-1", ch)),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    case "code": {
      const md = [
        "这是两种写法：",
        "",
        "```go",
        "package main",
        "",
        "func main() {",
        "\tprintln(\"hello\")",
        "}",
        "```",
        "",
        "行内代码比如 `panic()` 会渲染成行内的样子。",
      ].join("\n")
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", md),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    case "table": {
      const md = [
        "| Setting | Description |",
        "| --- | --- |",
        `| timeout | ${"a deliberately long description that must not widen the page ".repeat(4)} |`,
        "| retries | three |",
      ].join("\n")
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", md),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    case "lists": {
      const md = [
        "- alpha",
        "- beta",
        "  - nested",
        "",
        "1. first",
        "2. second",
        "",
        "- [ ] todo",
        "- [x] done",
      ].join("\n")
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", md),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    case "typography": {
      const md = [
        "## 中英混排 Heading",
        "",
        "这是一段包含 **粗体 bold**、*斜体 italic*、`inline_code()` 的中英混排 mixed text，用来检查行高与换行。",
        "",
        "> 引用 quote：中英文混排时保持左侧引用线。",
        "",
        "---",
        "",
        `长链接：https://example.com/${"a".repeat(240)}`,
      ].join("\n")
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", md),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    case "error":
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", "starting"),
        textEnd("m-1"),
        runError("upstream_error", "upstream provider is unavailable"),
      ]

    case "reasoningDone": {
      // ★ 思维链 → 收尾 → 正文。收尾之后标题该变成「Thought for 1.5s」。
      //
      //   时刻**写死**：开始与结束相差 1500ms，于是「用时」是可断言的，
      //   不用在假后端里真等 1.5 秒。
      const base = 1750000000000
      return [
        started(threadId, runId),
        reasoningStart("m-1", base),
        reasoningDelta("m-1", "先看看这个仓库的结构", base),
        ...reasoningEnd("m-1", base + 1500),
        textStart("m-1", "assistant"),
        textDelta("m-1", `${marker} thought it through`),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
    }

    default:
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", `${marker} scripted answer`),
        textEnd("m-1"),
        finished(threadId, runId),
      ]
  }
}

/**
 * 把一条 AG-UI 事件应用到正在累积的 assistant 消息上。
 *
 * ★ 为什么必须做：轮次跑完，前端会**重读一次历史**
 *   （`GET /v2/sessions/:id/messages`，见 request 日志）。真后端此时已经
 *   把本轮内容落进了 session.jsonl，所以重读拿到的是完整内容。
 *   假后端若只把事件推给浏览器而不更新会话记录，那次重读就会拿回一条
 *   `content: ""` 的消息，把刚流式渲染出来的内容整个抹掉 ——
 *   现象是「回复闪一下就没了」，而且看起来像前端的 bug。
 */
function applyToMessage(msg: FakeMessage, ev: RunEvent): void {
  switch (ev.type) {
    case "TEXT_MESSAGE_CONTENT":
      msg.content += String(ev.delta ?? "")
      break
    case "REASONING_MESSAGE_CONTENT": {
      // ★ 思维链的正文与用时进 metadata —— 与真后端同一套形状
      //   （api/v2_convert.go）。不这么做，刷新后思维链整个没了。
      msg.metadata ??= {}
      const meta = msg.metadata
      meta.reasoning = `${String(meta.reasoning ?? "")}${String(ev.delta ?? "")}`
      if (typeof ev.timestamp === "number") {
        // ★ 开始时刻只记第一次 —— 用时是从**第一段推理**算到收尾。
        if (meta.reasoningStartedAt === undefined) meta.reasoningStartedAt = ev.timestamp
      }
      break
    }
    case "REASONING_MESSAGE_END":
    case "REASONING_END": {
      const meta = msg.metadata
      if (!meta) break
      const start = meta.reasoningStartedAt
      if (typeof start === "number" && typeof ev.timestamp === "number") {
        meta.reasoningMs = ev.timestamp - start
      }
      delete meta.reasoningStartedAt
      break
    }
    case "TOOL_CALL_START": {
      msg.toolCalls ??= []
      const existing = msg.toolCalls.find((t) => t.id === ev.toolCallId)
      if (!existing) {
        msg.toolCalls.push({
          id: String(ev.toolCallId),
          type: "function",
          function: { name: String(ev.toolCallName ?? ""), arguments: "" },
        })
      }
      break
    }
    case "TOOL_CALL_RESULT": {
      msg.toolCallResults ??= []
      msg.toolCallResults.push({ toolCallId: String(ev.toolCallId), content: String(ev.content ?? "") })
      break
    }
    case "TOOL_CALL_ARGS": {
      const tc = msg.toolCalls?.find((t) => t.id === ev.toolCallId)
      if (tc) tc.function.arguments += String(ev.delta ?? "")
      break
    }
    default:
      break
  }
}

/** 从请求体里取出最后一条 user 文本（与后端 lastUserText 同义）。 */
function lastUserText(messages: FakeMessage[] | undefined): string {
  if (!Array.isArray(messages)) return ""
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m.role === "user") return typeof m.content === "string" ? m.content : ""
  }
  return ""
}

// ── HTTP ──────────────────────────────────────────────────────────────────

export function startFakeServer(port = 0): FakeServer {
  let threads = seedThreads()
  let requests: string[] = []
  let seq = 0
  /**
   * 当前回放场景。
   *
   * ★ 放在服务端而不是 URL 参数上：产品代码不该为了测试多传一个
   *   query。让 Playwright 直接打控制端点来切，产品代码零改动。
   */
  let scenario: ScenarioName = "simple"
  // agentsMode 切 /v2/agents 的视图：默认（单根）、多主、一大堆、或「热更新后」。
  let agentsMode: "default" | "multi" | "many" | "grew" = "default"
  /**
   * 建会话时的**默认根**（真后端恒为 main）。
   *
   * ★ E2E 用它证明前端不替服务端选根：真后端的默认根恒为 main，所以
   *   「前端自作主张发了 main」与「服务端本来就选 main」看起来一模一样 ——
   *   把这里改成 worker 才**看得见**：用户没选过 agent 时，前端若发了
   *   main，服务端就会照着 main 建，而不是它自己定的默认根。
   */
  let defaultRoot = "main"

  const find = (id: string) => threads.find((t) => t.threadId === id)

  const meta = (t: FakeThread) => ({
    threadId: t.threadId,
    title: t.title,
    createdAt: t.createdAt,
    updatedAt: t.updatedAt,
    messageCount: t.messages.length,
    state: t.state,
    hasPendingApproval: !!t.pendings?.length,
    agentId: t.agentId ?? "main",
  })

  /** queryParams 把 ?a=b 摊成普通对象（GET 没有请求体）。 */
  function queryParams(u: URL): Record<string, string> {
    const out: Record<string, string> = {}
    u.searchParams.forEach((v, k) => {
      out[k] = v
    })
    return out
  }

  /** readJSON 读请求体；空体当空对象。 */
  async function readJSON(req: Request): Promise<Record<string, any>> {
    const text = await req.text()
    if (!text) return {}
    return JSON.parse(text) as Record<string, any>
  }

  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    })

  const fail = (status: number, error: string, code: string, retryable = false) =>
    json({ error, code, retryable }, status)

  const notFound = () => fail(404, "session not found", "thread_not_found")

  // ── /v2/settings ────────────────────────────────────────────────────────
  //
  // ★ 这一族的**语义**是真的：遮蔽、409 冲突、422 破坏合并、明文密钥拒绝、
  //   以及「保存成功但未生效」。界面不接真后端就没法验这些分支，而它们
  //   恰恰是最贵出错的地方。
  //
  //   写在一处而不是每个用例现编，是为了让 17 条 E2E 跑的是同一套规则 ——
  //   每条用例自己编的话，「后端行为」会跟着用例一起漂。

  interface SettingFile {
    path: string
    body: string
    /** 乐观并发键；每写一次变一次。 */
    rev: string
  }
  const settingsDisk: SettingFile[] = []
  let settingsRev = 0
  const nextRev = () => "rev-" + ++settingsRev

  const seedSettings = () => {
    settingsDisk.length = 0
    settingsRev = 0
    const put = (path: string, body: string) => settingsDisk.push({ path, body, rev: nextRev() })
    put("policy/permissions.yaml", "allow: [ls, read_file]\nask: [write_file]\ndeny: []\n")
    // ★ policy/ 下有两种文件形态 —— 假后端也要有两种，否则
    //   「limits.yaml 能不能编辑、能不能存」这条根本测不到。
    put("policy/limits.yaml", "agent:\n  max_depth: 2\nretry:\n  max_retries: 1\n")
    put("agents/main.md", "---\nmodel: deepseek/flash\ndescription: root\n---\nbody\n")
    put("llm/deepseek.yaml", "type: openai\nbase_url: https://x\napi_key: ${KEY}\nmodels:\n  - id: flash\n")
    put("server/config.yaml", 'listen_addr: ":8080"\n')
  }
  seedSettings()

  const SETTINGS_MANIFEST = {
    categories: [
      { id: "policy", label: "Approval policy", editable: true, hot: true },
      { id: "agents", label: "Agents", editable: true, hot: true },
      { id: "llm", label: "Providers", editable: true, hot: true },
      { id: "server", label: "Server", editable: false, hot: false, note: "not hot-reloaded; changes apply on restart" },
    ],
    items: [
      { path: "policy/permissions.yaml", exists: true, hot: true, revision: "r1" },
      { path: "policy/limits.yaml", exists: true, hot: true, revision: "r6" },
      { path: "agents/main.md", exists: true, hot: true, revision: "r3" },
      { path: "llm/deepseek.yaml", exists: true, hot: true, revision: "r4" },
      { path: "server/config.yaml", exists: true, hot: false, revision: "r5" },
    ],
    templates: {
      policy: "allow: []\nask: []\ndeny: []\n",
      // ★ 按**路径**的模板键 —— policy/ 下有两种文件形态，一个分区模板放不下。
      "policy/limits.yaml":
        "# 限制规则：数量上的上限。\n# 只能收紧；放宽请改 server/config.yaml。\nagent:\n  max_depth: 0\nretry:\n  max_retries: 0\n",
      agents: "---\nmodel: \ndescription: \n---\n\n# available models: deepseek/flash\n",
      llm: "type: openai\nbase_url: \napi_key: ${}\nmodels: []\n",
    },
    modelRefs: ["deepseek/flash"],
    reasoningLevels: { "deepseek/flash": ["off", "low", "high", "max"] },
    configVersion: "fake-v1",
  }

  const hotFor = (path: string) => !path.startsWith("server/")

  /**
   * validateAndWrite 跑一遍「跨文件」的判定，返回错误或落盘。
   *
   * ★ 真实后端是**试合并**：改一个文件之后整个配置重新加载、重新校验。
   *   假后端不可能那么做，但必须保住那条最重要的性质 ——
   *   **单文件语法没错，合并后仍可能不成立**。
   */
  /** writeSettingOnProbe 只做判定、不落盘 —— /v2/settings/validate 用。 */
  const writeSettingOnProbe = (body: any): Response | null => {
    const path: string = body.path ?? ""
    const content: string = body.content ?? ""
    if (path.startsWith("llm/") && /^api_key:\s*(?!\$\{|@file:)/m.test(content)) {
      return fail(400, "api_key must be a ${ENV} or @file: reference", "config_secret_literal")
    }
    if (content.includes("BROKEN")) {
      return fail(
        422,
        "agents/main.md references model deepseek/gone which is not declared under llm/",
        "config_would_break_merge",
      )
    }
    return null
  }

  const writeSetting = (path: string, body: string, baseRevision?: string): Response => {
    if (path.startsWith("llm/") && /^api_key:\s*(?!\$\{|@file:)/m.test(body)) {
      return fail(400, "api_key must be a ${ENV} or @file: reference", "config_secret_literal")
    }
    if (body.includes("BROKEN")) {
      return fail(
        422,
        "agents/main.md references model deepseek/gone which is not declared under llm/",
        "config_would_break_merge",
      )
    }
    const existing = settingsDisk.find((f) => f.path === path)
    if (existing && baseRevision && existing.rev !== baseRevision) {
      return fail(409, "this file changed on disk", "config_write_conflict")
    }
    if (existing) {
      existing.body = body
      existing.rev = nextRev()
    } else {
      settingsDisk.push({ path, body, rev: nextRev() })
    }
    // ★ 「保存成功」与「已生效」必须能分开：内容里的 NO_WATCH 标记模拟
    //   watch.enabled: false —— 非热更新的分区（server/）在 v1 里不可写，
    //   所以那条路径只能从这里进来。
    return json({
      path,
      revision: settingsDisk.find((f) => f.path === path)!.rev,
      configVersion: "fake-v2",
      applied: !body.includes("NO_WATCH"),
    })
  }

  async function handleRun(req: Request): Promise<Response> {
    if (!(scenario in SCENARIOS)) {
      return fail(400, `unknown scenario: ${scenario}`, "invalid_body")
    }

    let body: {
      threadId?: string
      runId?: string
      /** 新协议字段；老客户端只发 messages。 */
      query?: string
      messages?: FakeMessage[]
      resume?: unknown[]
    }
    try {
      body = await req.json()
    } catch {
      return fail(400, "invalid body", "invalid_body")
    }

    const runId = body.runId ?? `run-${++seq}`
    let thread = body.threadId ? find(body.threadId) : undefined

    // ★ 与真后端一致：**会话只由 POST /v2/sessions 建**。
    //   缺 threadId 是请求不合法（400），不是「顺便建一个」；
    //   给了 id 但没有这个会话是 404 —— 两者答的是不同的问题。
    if (!body.threadId) {
      return fail(400, "threadId is required; create the session with POST /v2/sessions first", "invalid_body")
    }
    if (!thread) return notFound()

    // resume：答复审批，不新建消息
    if (Array.isArray(body.resume) && body.resume.length > 0) {
      if (!thread) return notFound()
      const answers = body.resume as {
        interruptId: string
        payload?: { approved: boolean; reason?: string }
      }[]
      // ★ 校验**每一个** id，而且答的是**整组**。
      //
      //	真后端一次要答完所有待批：框架的契约是「不在 Targets 里的被
      //	中断组件会重新中断自己」，而重新中断拿到的是新 id —— 逐个发的话，
      //	答完第一个其余的 id 就全废了。
      const pendings = thread.pendings ?? []
      if (pendings.length === 0) {
        return fail(409, "interruptId does not match", "interrupt_mismatch")
      }
      for (const a of answers) {
        if (!pendings.some((p) => p.interruptId === a.interruptId)) {
          return fail(409, "interruptId does not match", "interrupt_mismatch")
        }
      }
      const approved = answers[0]?.payload?.approved ?? false
      thread.pendings = undefined
      thread.state = "running"
      // ★ 这里必须 push FakeMessage。
      //   toolResult() 返回的是 AG-UI **事件**，塞进 messages 会让
      //   /messages 吐出错误形状 —— 上一版就是这里错的。
      thread.messages.push({
        id: "call_1-result",
        role: "tool",
        content: approved ? "written" : `denied: ${answers[0]?.payload?.reason ?? "rejected by user"}`,
        toolCallId: "call_1",
      })
      const stream = new ReadableStream<Uint8Array>({
        start(c) {
          // ★ 答复之后回放的是「普通完成」，不是当前场景。
          //   沿用 approval 场景的话，这一轮会以另一个 interrupt 收尾，
          //   审批卡原地不动 —— 测试表现成「点了同意没反应」，而实际上
          //   前端是对的，是假后端在原地打转。
          const msg: FakeMessage = { id: `m-${++seq}`, role: "assistant", content: "" }
          thread!.messages.push(msg)
          for (const ev of eventsFor("simple", thread!.threadId, MARKER_A, runId)) {
            applyToMessage(msg, ev)
            c.enqueue(encode(ev))
          }
          thread!.state = "idle"
          thread!.usage = USAGE
          c.close()
        },
      })
      return sse(stream)
    }

    thread.state = "running"

    // ★ 与 v2_run.go 一致：query 优先，messages 只是兼容回退。
    const query = (body.query ?? "").trim() || lastUserText(body.messages)
    if (!query) return fail(400, "message must not be empty", "empty_message")

    const marker = query.includes(MARKER_B) ? MARKER_B : MARKER_A
    const msgId = `m-${++seq}`
    thread.messages.push({ id: `u-${seq}`, role: "user", content: query })
    const assistantMsg: FakeMessage = { id: msgId, role: "assistant", content: "" }
    thread.messages.push(assistantMsg)
    // ★ 真后端会在用户消息落盘时按它推导标题（session/layout.go:titleOf）。
    //   不做的话侧栏永远显示 "New Session"，测不到标题行为。
    if (thread.title === "New Session") thread.title = query.slice(0, 40)

    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        const events = eventsFor(scenario, thread!.threadId, marker, runId)
        for (const ev of events) {
          applyToMessage(assistantMsg, ev)
          c.enqueue(encode(ev))
        }
        const fin = events.at(-1)
        const hits = fin?.outcome?.type === "interrupt" ? (fin.outcome.interrupts ?? []) : []
        if (hits.length > 0) {
          thread!.state = "waiting_approval"
          // ★ 全部待批都存下来 —— 少存一个，用户就再也答不了它。
          thread!.pendings = hits.map((hit) => ({
            interruptId: hit.id,
            reason: hit.reason,
            message: hit.message,
            toolCallId: hit.toolCallId,
            toolName: "write_file",
            subagentRunId: (hit as { subagentRunId?: string }).subagentRunId,
          }))
        } else {
          thread!.state = "idle"
        }
        thread!.usage = USAGE
        // ★ slowRun **不关流**：轮次保持 running，用户才点得到「停止」。
        //   客户端断开（点停止会 abort 本地 fetch）后写入失败，流自然结束。
        if (scenario !== "slowRun") c.close()
      },
      // ★ 客户端断开（点「停止」会 abort 本地 fetch）时把这一轮收掉 ——
      //   否则连接一直挂着，假后端那边也清理不掉。
      cancel() {
        if (thread && thread.state === "running") thread.state = "idle"
      },
    })
    return sse(stream)
  }

  const server = Bun.serve({
    port,
    async fetch(req) {
      const url = new URL(req.url)
      const path = url.pathname
      requests.push(`${req.method} ${path}`)

      if (path === "/v2/agents") {
        if (agentsMode === "multi") return json(FAKE_AGENTS_MULTI)
        if (agentsMode === "many") return json(FAKE_AGENTS_MANY)
        if (agentsMode === "grew") return json(FAKE_AGENTS_GREW)
        return json(FAKE_AGENTS)
      }
      if (path === "/v2/health") return json({ ok: true })

      // ── 测试控制端点（产品代码永远不会调）──────────────────────────
      if (path === "/__fake/reset" && req.method === "POST") {
        threads = seedThreads()
        // ★ 配置文件也必须复位：settings 用例会编辑并删除文件，只重置
        //   会话表会让「同一个假后端跑第二遍」读到上一轮改过的内容 ——
        //   单独跑绿、连着跑红，而那时很难相信是测试自己的问题。
        seedSettings()
        requests.length = 0
        seq = 0
        scenario = "simple"
        agentsMode = "default"
        defaultRoot = "main"
        return json({ ok: true })
      }
      if (path === "/__fake/default-root" && req.method === "POST") {
        // ★ 改「建会话时的默认根」：真后端恒为 main，改它是为了让
        //   「前端有没有替服务端选根」这件事在 E2E 里看得见。
        const agent = url.searchParams.get("agent") ?? ""
        if (!fakeAgentIds().has(agent)) {
          return fail(400, `unknown agent: ${agent}`, "agent_not_found")
        }
        defaultRoot = agent
        return json({ ok: true, defaultRoot })
      }
      if (path === "/__fake/threads/clear" && req.method === "POST") {
        // ★ 服务端一个会话都没有 —— 模拟「状态目录被清过 / 换了机器」。
        //   此时 localStorage 里记着的那个 id 一定是悬空的 ✅ ✅。
        threads = []
        return json({ ok: true })
      }
      if (path === "/__fake/agents-mode" && req.method === "POST") {
        const mode = url.searchParams.get("mode") ?? "default"
        if (mode !== "default" && mode !== "multi" && mode !== "many" && mode !== "grew") {
          return fail(400, `unknown agents mode: ${mode}`, "invalid_body")
        }
        agentsMode = mode
        return json({ ok: true, mode: agentsMode })
      }
      if (path === "/__fake/scenario" && req.method === "POST") {
        const name = new URL(req.url).searchParams.get("name") ?? ""
        if (!(name in SCENARIOS)) return fail(400, `unknown scenario: ${name}`, "invalid_body")
        scenario = name as ScenarioName
        return json({ ok: true, scenario })
      }
      if (path === "/__fake/threads" && req.method === "GET") {
        // E2E 用它断言「服务端记下的 agentId 是什么」——请求日志只记
        // method+path，读不到 body。
        return json({ items: threads.map(meta) })
      }

      if (path === "/v2/commands" && req.method === "GET") {
        return json({ commands: FAKE_COMMANDS })
      }

      if (path === "/v2/skills" && req.method === "GET") {
        return json({ skills: FAKE_SKILLS })
      }

      if (path === "/v2/project/files" && req.method === "GET") {
        const q = (url.searchParams.get("q") ?? "").trim().toLowerCase()
        const rawLimit = url.searchParams.get("limit")
        const parsed = rawLimit === null ? 50 : Number(rawLimit)
        if (!Number.isFinite(parsed)) return fail(400, "limit must be a number", "invalid_body")
        const limit = Math.max(1, Math.min(200, Math.trunc(parsed)))
        const matches = FAKE_PROJECT_FILES.filter((p) => p.toLowerCase().includes(q))
        return json({
          files: matches.slice(0, limit).map((p) => ({ path: p })),
          truncated: matches.length > limit,
        })
      }

      if (path === "/v2/settings" && req.method === "GET") {
        return json(SETTINGS_MANIFEST)
      }

      if (path === "/v2/settings/validate" && req.method === "POST") {
        const body = (await readJSON(req))
        // 校验与写入**必须**判同一套规则：只在一处判的话，两边会漂，
        // 于是「校验通过但保存失败」或反之，而 E2E 抓不到。
        const probe = writeSettingOnProbe(body)
        if (probe) return probe
        return json({ ok: true, configVersion: "fake-v1" })
      }

      if (path === "/v2/settings/item") {
        // ★ GET 没有 body。直接 req.json() 会抛「Unexpected end of JSON input」，
        //   而那会让**整条 GET 路由**返回 500 —— 表现是「点开就白屏」，
        //   报错还落在假后端里，离真正的病因十万八千里。
        const body = req.method === "GET" ? queryParams(url) : await readJSON(req)
        if (req.method === "GET") {
          const found = settingsDisk.find((f) => f.path === body.path)
          return json({
            path: body.path,
            content: found?.body ?? "",
            exists: !!found,
            revision: found?.rev ?? "",
            hot: hotFor(body.path),
          })
        }
        if (req.method === "POST" || req.method === "PUT") {
          return writeSetting(body.path, body.content ?? "", body.baseRevision)
        }
        if (req.method === "DELETE") {
          const i = settingsDisk.findIndex((f) => f.path === body.path)
          if (i < 0) return fail(404, "no such file", "config_path_invalid")
          settingsDisk.splice(i, 1)
          return json({ path: body.path, revision: "", configVersion: "fake-v2", applied: true })
        }
      }

      if (path === "/v2/run" && req.method === "POST") return handleRun(req)

      if (path === "/v2/sessions" && req.method === "GET") {
        return json({ items: threads.map(meta) })
      }

      if (path === "/v2/sessions" && req.method === "POST") {
        // ★ 建会话时可带**根 agent**（与真后端同形）。
        const body = (await req.json().catch(() => ({}))) as { agentId?: string }
        const agentId = (body.agentId ?? "").trim()
        if (agentId && !fakeAgentIds().has(agentId)) {
          return fail(400, `agent not found: ${agentId}`, "agent_not_found")
        }
        const t: FakeThread = {
          threadId: `new-${++seq}`,
          title: "New Session",
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
          messages: [],
          state: "idle",
          // ★ 根 agent 在建会话时就定下：客户端给了就用它，没给就用
          //   服务端的默认根（真后端恒为 main）。
          agentId: agentId || defaultRoot,
          files: [],
        }
        threads.push(t)
        return json(meta(t), 201)
      }

      // ★ 必须在通用 /v2/sessions/:id 之前：那个正则不认 /agent 后缀，
      //   落到后面会变成 404，而 404 看起来像「接口没实现」。
      const sw = path.match(/^\/v2\/sessions\/([^/]+)\/agent$/)
      if (sw && req.method === "PATCH") {
        const t = find(sw[1])
        if (!t) return notFound()
        const body = (await req.json().catch(() => ({}))) as { agentId?: string }
        const id = (body.agentId ?? "").trim()
        if (!id) return fail(400, "agentId must not be empty", "invalid_body")
        if (!fakeAgentIds().has(id)) {
          return fail(400, `agent not found: ${id}`, "agent_not_found")
        }
        if (t.state !== "idle" || t.pendings?.length) {
          return fail(409, "session is busy; finish or stop the run first", "agent_busy")
        }
        t.agentId = id
        return json(meta(t))
      }

      const m = path.match(/^\/v2\/sessions\/([^/]+)(\/messages)?$/)
      if (m) {
        const t = find(m[1])
        if (!t) return notFound()
        if (!m[2]) return json(meta(t))
        return json({
          threadId: t.threadId,
          state: t.state,
          messages: t.messages,
          pendingApprovals: t.pendings,
          usage: t.usage,
        })
      }

      const p = path.match(/^\/v2\/sessions\/([^/]+)\/(files|abort)$/)
      if (p) {
        const t = find(p[1])
        if (!t) return notFound()
        if (p[2] === "abort") return json({ aborted: true })
        if (req.method === "POST") {
          // multipart 上传：把收到的文件记进会话的 workspace。
          //
          // ★ 真后端把文件落到会话 workspace 目录，模型用工具读那个目录；
          //   这里如实记录，好让 E2E 能断言「附件确实落在会话里」。
          const form = await req.formData().catch(() => null)
          const names = form ? [...form.getAll("files")] : []
          const added = names
            .filter((f): f is File => f instanceof File)
            .map((f) => {
              const entry = { name: f.name, path: `/workspace/${t!.threadId}/${f.name}`, size: f.size }
              t!.files.push(entry)
              return entry
            })
          if (added.length === 0) return fail(400, "file is required", "file_required")
          return json({ files: added, workspacePath: `/workspace/${t.threadId}` }, 201)
        }
        return json({ workspacePath: `/workspace/${t.threadId}`, files: t.files })
      }

      const r = path.match(/^\/v2\/sessions\/([^/]+)$/)
      if (r && req.method === "PATCH") {
        const t = find(r[1])
        if (!t) return notFound()
        const body = (await req.json()) as { title?: string }
        if (body.title) t.title = body.title
        return json(meta(t))
      }
      if (r && req.method === "DELETE") {
        const t = find(r[1])
        if (!t) return notFound()
        threads = threads.filter((x) => x.threadId !== t.threadId)
        return new Response(null, { status: 204 })
      }

      return fail(404, "no route", "not_found")
    },
  })

  return {
    url: `http://127.0.0.1:${server.port}`,
    stop: () => server.stop(true),
    requests,
    threads: () => threads,
    reset: () => {
      threads = seedThreads()
      requests.length = 0
      seq = 0
      scenario = "simple"
    },
    setScenario: (name: ScenarioName) => {
      scenario = name
    },
  }
}

/** ★ 冒号后无空格 —— 与 sseio/hertz 的真实输出一致。 */
function encode(ev: RunEvent): Uint8Array {
  // ★ 没带 timestamp 的补一个当前时刻 —— 真后端每个事件都带，
  //   前端靠它算「思维链想了多久」。
  const withTs = ev.timestamp === undefined ? { ...ev, timestamp: Date.now() } : ev
  return new TextEncoder().encode(`data:${JSON.stringify(withTs)}\n\n`)
}

function sse(stream: ReadableStream<Uint8Array>): Response {
  return new Response(stream, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
    },
  })
}
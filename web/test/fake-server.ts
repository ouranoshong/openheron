
// FakeRun is one run of the fake's session.
//
// ★ The fake keeps it because the real server keeps it: the run ledger is
//   what a client scans to learn whether its run is still going, and the
//   attach path needs the run's events to replay.
export interface FakeRun {
  runId: string
  state: "starting" | "running" | "completed" | "failed"
  kind: "query" | "resume"
  startedAt: string
  endedAt?: string
  // events are the ones the client has already seen — the window.
  events: RunEvent[]
  // remaining are the ones a client that comes back late still gets.
  remaining?: RunEvent[]
  messageId?: string
}

export interface FakeThread {
  threadId: string
  title: string
  createdAt: string
  updatedAt: string
  messages: FakeMessage[]
  state: "idle" | "running" | "waiting_approval"
  agentId?: string
  pendings?: PendingApproval[]
  runs?: FakeRun[]
  usage?: Usage
  files: { name: string; path: string; size: number }[]
}

export interface FakeMessage {
  id: string
  role: "system" | "developer" | "user" | "assistant" | "tool"
  content: string
  name?: string
  toolCallId?: string
  toolCalls?: { id: string; type: "function"; function: { name: string; arguments: string } }[]
  toolCallResults?: unknown[]
  metadata?: Record<string, unknown>
}

interface PendingApproval {
  interruptId: string
  reason: string
  message?: string
  toolCallId?: string
  toolName?: string
  arguments?: unknown
  subagentRunId?: string
}

interface ContextUsage {
  agent: string
  provider?: string
  model?: string
  usedTokens: number
  contextWindow: number
  summarizeAt?: number
  summarized?: number
  updatedAt?: string
}

interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens: number
  cachedTokens: number
  byAgent?: Record<string, Usage>
  // context is the per-agent context occupancy — a point-in-time reading,
  // unlike byAgent, which is a cumulative spend.
  context?: Record<string, ContextUsage>
}

export interface FakeServer {
  url: string
  stop(): void
  requests: string[]
  threads(): FakeThread[]
  reset(): void
  setScenario(name: ScenarioName): void
}

interface RawInterrupt {
  id: string
  reason: string
  message?: string
  toolCallId?: string
}

interface RunEvent {
  type: string
  outcome?: { type: string; interrupts?: RawInterrupt[]; result?: unknown }
  [k: string]: unknown
}

function started(threadId: string, runId: string): RunEvent {
  return { type: "RUN_STARTED", threadId, runId }
}

function textStart(messageId: string, role: string, name = "main"): RunEvent {
  return { type: "TEXT_MESSAGE_START", messageId, role, name }
}

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

function reasoningStart(messageId: string, timestamp?: number): RunEvent {
  return { type: "REASONING_START", messageId, ...at(timestamp) }
}

function reasoningDelta(messageId: string, delta: string, timestamp?: number): RunEvent {
  return { type: "REASONING_MESSAGE_CONTENT", messageId, delta, ...at(timestamp) }
}

function reasoningEnd(messageId: string, timestamp?: number): RunEvent[] {
  return [
    { type: "REASONING_MESSAGE_END", messageId, ...at(timestamp) },
    { type: "REASONING_END", messageId, ...at(timestamp) },
  ]
}

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
      interrupts: list.map((one) => ({
        id: one.interruptId,
        reason: "tool_call",
        message: one.message,
        toolCallId: one.toolCallId,
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

export const SCENARIOS = {
  simple: ["A"],

  twoQuestions: ["A", "B"],

  approval: ["A"],

  slowRun: ["A"],

  dropped: ["A"],

  approvalMulti: ["A"],

  streaming: ["A"],

  multiAgentStream: ["A"],

  error: ["A"],

  code: ["A"],

  table: ["A"],

  lists: ["A"],

  typography: ["A"],

  longStream: ["A"],

  bigChunks: ["A"],

  toolDone: ["A"],

  toolJson: ["A"],

  reasoningDone: ["A"],

  compaction: ["A"],

  // busySession: the session already has a live turn, so a second run is
  // refused with 409 session_busy — the cross-client case.
  busySession: ["A"],
} as const

export type ScenarioName = keyof typeof SCENARIOS

export const THREAD_A = "11111111-1111-4111-8111-111111111111"
export const THREAD_B = "22222222-2222-4222-8222-222222222222"

export const THREAD_LONG = "33333333-3333-4333-8333-333333333333"

export const THREAD_EMPTY = "44444444-4444-4444-8444-444444444444"
export const THREAD_HISTORY = "55555555-5555-4555-8555-555555555555"

export const THREAD_MULTI = "66666666-6666-4666-8666-666666666666"

export const THREAD_COMPACT = "77777777-7777-4777-8777-777777777777"

// A session whose context holds a fraction of a percent of a very large
// window — the case where "0 %" would be a lie.
export const THREAD_TINY = "88888888-8888-4888-8888-888888888888"

export const MARKER_A = "MARKER-ALPHA"
export const MARKER_B = "MARKER-BRAVO"
export const MARKER_LONG = "MARKER-LONGCVT"
export const MARKER_HISTORY = "MARKER-HISTORY"
export const MARKER_MULTI_MAIN = "MARKER-MULTI-MAIN"
export const MARKER_MULTI_CODER = "MARKER-MULTI-CODER"
export const MARKER_COMPACT = "MARKER-COMPACT"
export const SUMMARY_COMPACT = "SUMMARY-OF-THE-CONVERSATION"

/** Mirrors api/commands.go — names and kinds are what the UI branches on. */
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

// The MCP view the fake backend serves.
//
// ★ It carries the three states the panel has to show: a server that is up,
//   one that is unreachable, and one the agent does not use.
const FAKE_MCP_SERVERS = [
  {
    name: "github",
    transport: "stdio",
    status: "ready",
    error: "",
    tools: [
      {
        name: "read_issue",
        exposedName: "mcp__github__read_issue",
        tier: "ask",
        status: "available",
        reason: "",
      },
      {
        name: "delete_repo",
        exposedName: "mcp__github__delete_repo",
        tier: "ask",
        status: "available",
        reason: "",
      },
    ],
  },
  {
    name: "files",
    transport: "http",
    status: "unreachable",
    error: "the command is not on the PATH",
    tools: [
      {
        name: "read_file",
        exposedName: "mcp__files__read_file",
        tier: "allow",
        status: "available",
        reason: "",
      },
    ],
  },
  {
    name: "deploy",
    transport: "stdio",
    status: "unused",
    error: "",
    tools: [],
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
    name: "compact",
    description: "Compact the conversation into a summary",
    kind: "action",
    action: "compact",
  },
  {
    name: "mcps",
    description: "Show the MCP servers of this agent",
    kind: "action",
    action: "open_mcps",
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

const FAKE_AGENTS = {
  defaultAgent: "main",
  maxDepth: 1,
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
        contextWindow: 8192,
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
        contextWindow: 131072,
      },
      maxIterations: 40,
      subagents: ["worker"],
      disabledTools: [],
    },
  ],
}

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

const FAKE_AGENTS_GREW = {
  defaultAgent: "main",
  maxDepth: 1,
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

  // A transcript that was compacted: the messages above the marker are still
  // there, and the marker itself is a message carrying the compaction
  // metadata — the way api/v2_convert.go stamps it.
  const compactMessages: FakeMessage[] = [
    { id: "c-u1", role: "user", content: `${MARKER_COMPACT} first question` },
    { id: "c-a1", role: "assistant", content: `${MARKER_COMPACT} first answer` },
    {
      id: "c-compact",
      role: "user",
      content: SUMMARY_COMPACT,
      name: "main",
      metadata: {
        compaction: { superseded: 12, savedTokens: 62000, at: "2026-10-29T13:04:11.512Z" },
      },
    },
    { id: "c-a2", role: "assistant", content: `${MARKER_COMPACT} answer after the compaction` },
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
        context: {
          main: {
            agent: "main",
            provider: "deepseek",
            model: "deepseek-v4",
            usedTokens: 18432,
            contextWindow: 131072,
            summarizeAt: 104858,
            updatedAt: T1,
          },
          coder: {
            agent: "coder",
            provider: "openai",
            model: "fast",
            usedTokens: 6144,
            contextWindow: 8192,
            updatedAt: T1,
          },
        },
      },
    },
    {
      threadId: THREAD_COMPACT,
      title: "Compacted conversation",
      createdAt: T0,
      updatedAt: T1,
      state: "idle" as const,
      files: [],
      messages: compactMessages,
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
      // A model that declares no window: the occupancy is reported, and
      // there is no ratio to show.
      usage: {
        promptTokens: 1200,
        completionTokens: 300,
        totalTokens: 1500,
        reasoningTokens: 0,
        cachedTokens: 0,
        context: {
          main: { agent: "main", usedTokens: 1200, contextWindow: 0, updatedAt: T1 },
        },
      },
    },
    {
      threadId: THREAD_TINY,
      title: "Tiny context conversation",
      createdAt: T1,
      updatedAt: T1,
      state: "idle",
      files: [],
      messages: [
        { id: "t-u1", role: "user", content: "tiny first question" },
        { id: "t-a1", role: "assistant", content: "tiny first answer" },
      ],
      // A context that holds a third of a percent of a very large window.
      // Rounding that to "0 %" would say "nothing is in there".
      usage: {
        promptTokens: 4300,
        completionTokens: 0,
        totalTokens: 4300,
        reasoningTokens: 0,
        cachedTokens: 0,
        context: {
          main: { agent: "main", usedTokens: 4300, contextWindow: 1311000, updatedAt: T1 },
        },
      },
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

function eventsFor(
  scenario: ScenarioName,
  threadId: string,
  marker: string,
  runId: string,
): RunEvent[] {
  switch (scenario) {
    case "approval":
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
          subagentRunId: "worker",
        }),
      ]

    case "approvalMulti":
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
    case "busySession":
      return [
        started(threadId, runId),
        textStart("m-1", "assistant"),
        textDelta("m-1", `${marker} still working`),
        reasoningStart("m-1"),
        reasoningDelta("m-1", "still figuring it out"),
      ]

    case "multiAgentStream": {
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
        { type: "TEXT_MESSAGE_START", messageId: "m-2", role: "assistant", name: "main" },
        { type: "TEXT_MESSAGE_CONTENT", messageId: "m-2", delta: "已经读完这个文件。" },
        { type: "TEXT_MESSAGE_END", messageId: "m-2" },
      ]
      return [
        ...msg,
        { type: "TOOL_CALL_RESULT", toolCallId: "call_1", messageId: "call_1", content: "package main\n\nfunc main() {}\n", role: "tool" },
        ...announce("m-1"),
        finished(threadId, runId),
      ]
    }

    case "longStream": {
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

    case "compaction":
      // The compaction lands mid-turn: the boundary goes in before the
      // answer that follows it.
      return [
        started(threadId, runId),
        {
          type: "CUSTOM",
          name: "context_compacted",
          value: { agent: "main", at: "2026-10-29T13:04:11.512Z" },
        },
        textStart("m-1", "assistant"),
        textDelta("m-1", `${marker} answer after the compaction`),
        textEnd("m-1"),
        finished(threadId, runId),
      ]

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

function applyToMessage(msg: FakeMessage, ev: RunEvent): void {
  switch (ev.type) {
    case "TEXT_MESSAGE_CONTENT":
      msg.content += String(ev.delta ?? "")
      break
    case "REASONING_MESSAGE_CONTENT": {
      msg.metadata ??= {}
      const meta = msg.metadata
      meta.reasoning = `${String(meta.reasoning ?? "")}${String(ev.delta ?? "")}`
      if (typeof ev.timestamp === "number") {
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
  let scenario: ScenarioName = "simple"
  let agentsMode: "default" | "multi" | "many" | "grew" = "default"
  let mcpMode: "default" | "empty" = "default"
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

  function queryParams(u: URL): Record<string, string> {
    const out: Record<string, string> = {}
    u.searchParams.forEach((v, k) => {
      out[k] = v
    })
    return out
  }

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

  interface SettingFile {
    path: string
    body: string
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
      { id: "tools", label: "Tools", editable: true, hot: true },
      // ★ The MCP partition, exactly as the real server ships it: editable
      //   and hot. The client's own list of editable sections is a second
      //   truth — it is what made "mcps/ is not editable" appear.
      { id: "mcps", label: "MCP servers", editable: true, hot: true },
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
      "policy/limits.yaml":
        "# 限制规则：数量上的上限。\n# 只能收紧；放宽请改 server/config.yaml。\nagent:\n  max_depth: 0\nretry:\n  max_retries: 0\n",
      agents: "---\nmodel: \ndescription: \n---\n\n# available models: deepseek/flash\n",
      llm: "type: openai\nbase_url: \napi_key: ${}\nmodels: []\n",
      mcps: "transport: stdio\ncommand: mcp-example-server\nargs: []\ntimeout: 30s\ncall_timeout: 60s\n",
      // ★ The tools section template is **nothing but comments**, exactly as
      //   the real server ships it. A file built from it has no YAML
      //   document at all, so the skeleton the dialog must offer is the
      //   FILE's template — this is the shape the bug lived in.
      tools:
        "# tools/ one file = one tool:\n" +
        "#   http.yaml         request_get / request_post / request_put / request_delete\n" +
        "#   web_search.yaml   duckduckgo_search\n" +
        "#   wikipedia.yaml    wikipedia_search\n" +
        "#   kb.yaml           knowledge base\n" +
        "#\n" +
        "# ★ The content of the file IS **that tool's own configuration** (no outer wrapper key).\n" +
        "#   Whether the file exists IS the switch — turning a tool off = deleting that file.\n" +
        "#\n" +
        "# ★ tools/ is hot-reloaded: the change takes effect on the next turn, no process restart.\n",
      "tools/http.yaml":
        "# HTTP tool. Writing this file enables it.\n" +
        "#\n" +
        "# ★ methods defaults to get only — write methods (post/put/delete) must be named\n" +
        "#   explicitly, otherwise \"just adding a POST\" turns into an outbound channel.\n" +
        "methods: [get]\n" +
        "headers: {}\n" +
        "timeout: 30s\n",
      "tools/web_search.yaml":
        "# Web search. Writing this file enables it.\n" +
        "#\n" +
        "# ★ provider has only two options: duckduckgo (zero config, zero key) and searxng (self-hosted).\n" +
        "provider: duckduckgo\n" +
        "max_results: 10\n" +
        "timeout: 60s\n" +
        "region: \"\"\n",
      "tools/wikipedia.yaml":
        "# Wikipedia lookup. Writing this file enables it.\n" +
        "top_k: 3\n" +
        "language: zh\n" +
        "timeout: 15s\n" +
        "base_url: \"\"\n",
      "tools/kb.yaml":
        "# Knowledge base. Writing this file enables it.\n" +
        "#\n" +
        "# ★ corpus is the **corpus directory**, relative to the project root. Empty string = knowledge base off.\n" +
        "#   It lives and dies with the index: change the directory without rebuilding the index and it will confidently return unrelated answers.\n" +
        "corpus: \"\"\n" +
        "index: default\n" +
        "# model must point at a model with kind: embedding in llm/*.yaml; empty = pure lexical retrieval.\n" +
        "model: \"\"\n" +
        "query_instruction: \"\"\n" +
        "batch_size: 10\n" +
        "max_batch_chars: 1200\n" +
        "top_k: 8\n",
    },
    modelRefs: ["deepseek/flash"],
    reasoningLevels: { "deepseek/flash": ["off", "low", "high", "max"] },
    configVersion: "fake-v1",
  }

  const hotFor = (path: string) => !path.startsWith("server/")

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

    if (!body.threadId) {
      return fail(400, "threadId is required; create the session with POST /v2/sessions first", "invalid_body")
    }
    if (!thread) return notFound()

    if (Array.isArray(body.resume) && body.resume.length > 0) {
      if (!thread) return notFound()
      const answers = body.resume as {
        interruptId: string
        payload?: { approved: boolean; reason?: string }
      }[]
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
      thread.messages.push({
        id: "call_1-result",
        role: "tool",
        content: approved ? "written" : `denied: ${answers[0]?.payload?.reason ?? "rejected by user"}`,
        toolCallId: "call_1",
      })
      const stream = new ReadableStream<Uint8Array>({
        start(c) {
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

    // ★ Attach: the client comes back for a run it already knows. The
    //   request carries the run's id and no question — a question would
    //   mean "start a new run".
    if (body.runId && !(body.query ?? "").trim() && !body.messages?.length && !body.resume?.length) {
      const run = (thread.runs ?? []).find((r) => r.runId === body.runId)
      if (!run || run.state !== "running") {
        return fail(404, "the run is no longer live; load the session's history instead", "run_not_live")
      }
      const assistant = thread.messages.find((m) => m.id === run.messageId)
      const stream = new ReadableStream<Uint8Array>({
        start(c) {
          // The window: what the client missed when it lost the connection.
          for (const ev of run.events) c.enqueue(encode(ev))
          // And the rest of the turn.
          for (const ev of run.remaining ?? []) {
            if (assistant) applyToMessage(assistant, ev)
            c.enqueue(encode(ev))
          }
          run.state = "completed"
          run.endedAt = new Date().toISOString()
          thread.state = "idle"
          c.close()
        },
      })
      return sse(stream)
    }

    // ★ The admission gate. A session has at most one live turn, so a
    //   second run is refused — and the running turn is left alone.
    if (scenario === "busySession" && thread.state === "running") {
      return fail(409, "session is busy: a turn is already running on this session", "session_busy")
    }

    thread.state = "running"

    const query = (body.query ?? "").trim() || lastUserText(body.messages)
    if (!query) return fail(400, "message must not be empty", "empty_message")

    const marker = query.includes(MARKER_B) ? MARKER_B : MARKER_A
    const msgId = `m-${++seq}`
    thread.messages.push({ id: `u-${seq}`, role: "user", content: query })
    const assistantMsg: FakeMessage = { id: msgId, role: "assistant", content: "" }
    thread.messages.push(assistantMsg)
    if (thread.title === "New Session") thread.title = query.slice(0, 40)

    // ★ The "dropped" scenario: the connection dies mid-turn, but the run
    //   keeps going — the server archives the turn, not the stream. The
    //   client comes back with the run's id and gets the rest of it.
    if (scenario === "dropped") {
      const head: RunEvent[] = [
        started(thread!.threadId, runId),
        textStart(msgId, "assistant"),
        textDelta(msgId, `${marker} 前半`),
      ]
      const tail: RunEvent[] = [
        textDelta(msgId, `${marker} 后半`),
        textEnd(msgId),
        finished(thread!.threadId, runId),
      ]
      const run: FakeRun = {
        runId,
        state: "running",
        kind: "query",
        startedAt: new Date().toISOString(),
        events: head,
        remaining: tail,
        messageId: assistantMsg.id,
      }
      thread!.runs = [...(thread!.runs ?? []), run]
      const stream = new ReadableStream<Uint8Array>({
        start(c) {
          for (const ev of head) {
            applyToMessage(assistantMsg, ev)
            c.enqueue(encode(ev))
          }
          // ★ The connection dies here. The run does not.
          c.close()
        },
      })
      return sse(stream)
    }

    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        const events = eventsFor(scenario, thread!.threadId, marker, runId)
        for (const ev of events) {
          applyToMessage(assistantMsg, ev)
          c.enqueue(encode(ev))
        }
        // ★ The run is a row in the ledger — the client scans it to learn
        //   whether its run is still going.
        //
        //   A scenario that keeps the session busy leaves the run open:
        //   the session's state and the ledger must agree, or the client
        //   gets two different answers to the same question.
        const stillRunning = scenario === "busySession" || scenario === "slowRun"
        thread!.runs = [
          ...(thread!.runs ?? []),
          {
            runId,
            state: stillRunning ? "running" : "completed",
            kind: "query",
            startedAt: new Date().toISOString(),
            ...(stillRunning ? {} : { endedAt: new Date().toISOString() }),
            events,
            messageId: assistantMsg.id,
          },
        ]
        const fin = events.at(-1)
        const hits = fin?.outcome?.type === "interrupt" ? (fin.outcome.interrupts ?? []) : []
        if (hits.length > 0) {
          thread!.state = "waiting_approval"
          thread!.pendings = hits.map((hit) => ({
            interruptId: hit.id,
            reason: hit.reason,
            message: hit.message,
            toolCallId: hit.toolCallId,
            toolName: "write_file",
            subagentRunId: (hit as { subagentRunId?: string }).subagentRunId,
          }))
        } else if (scenario === "busySession") {
          // ★ The session keeps its live turn: the stream stays open and
          //   the state stays "running", so the next run meets the gate.
          thread!.state = "running"
        } else {
          thread!.state = "idle"
        }
        thread!.usage = USAGE
        if (scenario !== "slowRun" && scenario !== "busySession") c.close()
      },
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
      if (path === "/v2/mcps" && req.method === "GET") {
        const agent = url.searchParams.get("agent") ?? "main"
        return json({ agent, servers: mcpMode === "empty" ? [] : FAKE_MCP_SERVERS })
      }
      if (path === "/v2/health") return json({ ok: true })

      if (path === "/__fake/reset" && req.method === "POST") {
        threads = seedThreads()
        seedSettings()
        requests.length = 0
        seq = 0
        scenario = "simple"
        agentsMode = "default"
        mcpMode = "default"
        defaultRoot = "main"
        return json({ ok: true })
      }
      if (path === "/__fake/default-root" && req.method === "POST") {
        const agent = url.searchParams.get("agent") ?? ""
        if (!fakeAgentIds().has(agent)) {
          return fail(400, `unknown agent: ${agent}`, "agent_not_found")
        }
        defaultRoot = agent
        return json({ ok: true, defaultRoot })
      }
      if (path === "/__fake/mcp-mode" && req.method === "POST") {
        const mode = url.searchParams.get("mode") ?? "default"
        mcpMode = mode === "empty" ? "empty" : "default"
        return json({ ok: true, mode: mcpMode })
      }
      if (path === "/__fake/threads/clear" && req.method === "POST") {
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
        const probe = writeSettingOnProbe(body)
        if (probe) return probe
        return json({ ok: true, configVersion: "fake-v1" })
      }

      if (path === "/v2/settings/item") {
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
          agentId: agentId || defaultRoot,
          files: [],
        }
        threads.push(t)
        return json(meta(t), 201)
      }

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

      // ★ The run ledger's scan. It is the truth about "is my run still
      //   going" — the stream cannot say that, because the run outlives
      //   the stream.
      const runsRoute = path.match(/^\/v2\/sessions\/([^/]+)\/runs$/)
      if (runsRoute) {
        const t = find(runsRoute[1])
        if (!t) return notFound()
        const items = (t.runs ?? []).map((r) => ({
          runId: r.runId,
          state: r.state,
          kind: r.kind,
          epoch: 1,
          startedAt: r.startedAt,
          ...(r.endedAt ? { endedAt: r.endedAt } : {}),
        }))
        const active = items.find((r) => r.state === "running" || r.state === "starting")
        return json({ items, ...(active ? { activeRunId: active.runId } : {}) })
      }

      const p = path.match(/^\/v2\/sessions\/([^/]+)\/(files|abort|compact)$/)
      if (p) {
        const t = find(p[1])
        if (!t) return notFound()
        if (p[2] === "abort") {
          // ★ The abort really stops the turn: the session goes idle and
          //   the pending approval dies with it. Otherwise "stop, then
          //   take over" could not be tested — the session would stay
          //   busy forever.
          const wasRunning = t.state === "running"
          t.state = "idle"
          t.pendings = undefined
          // ★ The abort closes the run in the ledger too. The client's scan
          //   asks the ledger, so a run that stays open there would keep
          //   the session busy after it was stopped.
          for (const r of t.runs ?? []) {
            if (r.state === "running") {
              r.state = "completed"
              r.endedAt = new Date().toISOString()
            }
          }
          return json({ aborted: wasRunning })
        }
        if (p[2] === "compact") {
          // The compaction takes a model call, so it is not instant: the
          // client has something to show while it runs.
          await new Promise((r) => setTimeout(r, 800))
          // The compaction appends the summary to the transcript and reports
          // what it achieved — the same shape the real server answers with.
          const superseded = t.messages.length
          const summary: FakeMessage = {
            id: `c-compact-${superseded}`,
            role: "user",
            content: "SUMMARY-OF-THE-CONVERSATION",
            name: "main",
            metadata: {
              compaction: {
                superseded,
                savedTokens: 62000,
                at: "2026-10-30T00:00:00.000Z",
              },
            },
          }
          t.messages.push(summary)
          return json({
            threadId: t.threadId,
            superseded,
            savedTokens: 62000,
            usedTokens: 1200,
            at: "2026-10-30T00:00:00.000Z",
          })
        }
        if (req.method === "POST") {
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

function encode(ev: RunEvent): Uint8Array {
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
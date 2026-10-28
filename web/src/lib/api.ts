import { readSse } from "@/lib/sse"

/**
 * `/v2` 的类型化客户端。
 *
 * 与 Go 侧 `api/v2_types.go` 一一对应 —— 那里的 DTO 改了，这里必须同步，
 * 所以字段名一律保持后端的 camelCase，不要在中间层"翻译"成 TS 风格。
 */

/** 后端 `usageView`。 */
export interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens: number
  cachedTokens: number
  /** 用量按 agent 分账（后端 usageView.byAgent）。 */
  byAgent?: Record<string, Usage>
}

/**
 * 后端 `pendingView`。
 *
 * ★ 它是**列表**的一部分：一轮里可以有多个独立待批（模型一次请求四个
 *   都要审批的工具），每个都要有自己的 id —— 只有其中一个的话，客户端
 *   拿到几张卡就答不了几张。
 */
export interface PendingApproval {
  interruptId: string
  reason: string
  message?: string
  toolCallId?: string
  toolName?: string
  /** 结构化参数，形状随 toolName 变化，前端只做只读展示。 */
  arguments?: unknown
  requestedByAgent?: string
  subagentRunId?: string
  createdAt?: string
}

export interface LastError {
  code: string
  message: string
  at: string
}

/** 后端 `threadMeta`。 */
export interface ThreadMeta {
  threadId: string
  title: string
  createdAt: string
  updatedAt: string
  messageCount: number
  state: "idle" | "running" | "waiting_approval"
  hasPendingApproval: boolean
  /** 会话的根 agent。后端永远给具体名字（没切过就是 main）。 */
  agentId: string
}

/** 后端 `threadDetail`。 */
export interface ThreadDetail extends ThreadMeta {
  runId?: string
  startedAt?: string
  lastError?: LastError
  pendingApproval?: PendingApproval
}

/** AG-UI `types.Message`，只声明前端实际读到的字段。 */
export interface AgMessage {
  id: string
  role: "system" | "developer" | "user" | "assistant" | "tool"
  content: unknown
  name?: string
  /**
   * 额外信息（AG-UI 的通用 metadata）。
   *
   * ★ `createdAt` 是这条消息的**产生时刻**（定宽 UTC 字符串，可按字典序
   *   排序）。后端只在**历史回放**上带它 —— 实时流里还没有。
   */
  metadata?: Record<string, unknown>
  toolCallId?: string
  toolCalls?: {
    id: string
    type: "function"
    function: { name: string; arguments: string }
  }[]
  toolCallResults?: unknown[]
}

/** 后端 `messagesView`。 */
export interface MessagesView {
  threadId: string
  state: ThreadMeta["state"]
  messages: AgMessage[]
  pendingApprovals?: PendingApproval[]
  usage?: Usage
  /** 本次视图的过滤条件（?agent=xxx）；空 = 全部消息。 */
  agent?: string
}

/** 后端 `errBody`。 */
/** 后端 `uploadedFile`（api/v2_files.go）。 */
export interface UploadedFile {
  name: string
  path: string
  size: number
}

/** 后端 `uploadResult`。 */
export interface UploadResult {
  files: UploadedFile[]
  workspacePath: string
}

/** 后端 `projectFile`（api/v2_project.go）。 */
export interface ProjectFile {
  path: string
}

/** 后端 `projectFileList`。 */
export interface ProjectFileList {
  files: ProjectFile[]
  truncated: boolean
}

/** 动作命令的语义由界面执行，服务端只负责声明。 */
export type CommandAction =
  | "new_thread"
  | "stop_run"
  | "attach_files"
  | "open_settings"
  | "open_agents"

/** 后端 `commandView`（api/commands.go）。 */
export interface SlashCommand {
  name: string
  description: string
  argumentHint?: string
  kind: "action" | "prompt"
  action?: CommandAction
}

/** 后端 `modelView`（api/v2_introspect.go）。 */
export interface AgentModelView {
  provider: string
  model: string
  reasoning: string
  reasoningAvailable: string[]
  reasoningDefault: string
}

/** 后端 `agentView`，只声明界面读取的字段。 */
export interface AgentView {
  id: string
  description?: string
  model: AgentModelView
  maxIterations: number
  subagents: string[]
  disabledTools: string[]
}

/** 后端 `SkillView`：一个 skill 的清单项。 */
export interface SkillView {
  name: string
  /** 这个 skill 干什么的（frontmatter 的 description）。 */
  description?: string
  /** 执行模式：inline / fork / fork_with_context。 */
  context?: string
  /** fork 模式要用的 agent 名（空 = 当前 agent）。 */
  agent?: string
  /** 这个 skill 覆盖的模型（空 = 不覆盖）。 */
  model?: string
}

/** 后端 `agentsView`。 */
export interface AgentsView {
  /**
   * 新会话默认用的那个 agent。
   *
   * ★ 不是「主 agent」：每个 agent 都能当会话的 agent，这只是那个
   *   「没指定时用哪个」的约定。
   */
  defaultAgent: string
  maxDepth: number
  agents: AgentView[]
  /**
   * 当前配置的指纹（后端 `config.Version`）。
   *
   * ★ 前端靠它知道「配置变了」：用户可以在**编辑器里**改 `agents/*.md`，
   *   服务端会热更新，但浏览器此前没有任何信号 —— 界面拿着旧缓存，
   *   显示的 agent 名单是过时的。
   */
  version: string
}

export interface ApiErrorBody {
  error: string
  code: string
  kind?: string
  hint?: string
  retryable: boolean
}

/**
 * ApiError 保留后端的 code 与 retryable。
 *
 * ★ 界面提示一律用 error（后端已经是面向人的英文），但分支逻辑必须走
 *   code —— "quota exhausted, 降并发没用" 和 "rate limited, 降并发" 在
 *   error 文本里很像，只有 code 能分开。见 api/v2_types.go 的注释。
 */
export class ApiError extends Error {
  readonly code: string
  readonly status: number
  readonly retryable: boolean
  readonly kind?: string
  readonly hint?: string

  constructor(status: number, body: ApiErrorBody) {
    super(body.error)
    this.name = "ApiError"
    this.status = status
    this.code = body.code
    this.retryable = body.retryable
    this.kind = body.kind
    this.hint = body.hint
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  })
  if (!res.ok) {
    let body: ApiErrorBody
    try {
      body = (await res.json()) as ApiErrorBody
    } catch {
      throw new ApiError(res.status, {
        error: res.statusText || `HTTP ${res.status}`,
        code: "unknown",
        retryable: res.status >= 500,
      })
    }
    throw new ApiError(res.status, body)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

/**
 * sessionPath 拼 `/v2/sessions/<id>` 并在 id 缺失时立刻抛错。
 *
 * ★ 这道断言不是多余的：会话要等第一条消息才创建，在那之前前端拿到的
 *   就是 undefined。参数按 `string` 声明只挡得住编译期 —— 调用方一旦用
 *   `!` 断言（本仓库的 runtime-provider 就干过），undefined 会无声流进来，
 *   模板字符串再把它变成 URL 里的一段字面量：
 *       GET /v2/sessions/undefined/messages
 *   这种请求必然 404，而报错里完全看不出成因。在这里抛，消息直接点名。
 */
function sessionPath(id: string, suffix = ""): string {
  if (!id) {
    throw new Error("session id is missing — the session has not been created yet")
  }
  return `/v2/sessions/${id}${suffix}`
}

export const api = {
  listThreads: () => call<{ items: ThreadMeta[] }>("/v2/sessions"),

  /**
   * 建会话。可选带上**根 agent** —— 前端暂存的那个选择就在这一刻落盘，
   * 省掉「先建、再 PATCH 改根」的一次往返。
   */
  createThread: (agentId?: string) =>
    call<ThreadMeta>("/v2/sessions", {
      method: "POST",
      body: agentId ? JSON.stringify({ agentId }) : undefined,
    }),

  getThread: (id: string) => call<ThreadDetail>(sessionPath(id)),

  /**
   * 改标题走后端而不是前端本地改 —— title 缓存已经落在服务端
   * state.json，前端缓存一份就会和它分叉。
   */
  renameThread: (id: string, title: string) =>
    call<ThreadMeta>(sessionPath(id), {
      method: "PATCH",
      body: JSON.stringify({ title }),
    }),

  deleteThread: (id: string) => call<void>(sessionPath(id), { method: "DELETE" }),

  /**
   * 切换会话的根 agent。
   *
   * ★ 服务端有最终判定权（未知 / 不可切换 / 忙），前端不做第二份规则 ——
   *   两份规则迟早分叉，而界面会先显示已切换再被拒绝。
   */
  switchAgent: (id: string, agentId: string) =>
    call<ThreadMeta>(`${sessionPath(id)}/agent`, {
      method: "PATCH",
      body: JSON.stringify({ agentId }),
    }),

  getMessages: (id: string) => call<MessagesView>(sessionPath(id, "/messages")),

  abortRun: (id: string) => call<{ aborted: boolean }>(sessionPath(id, "/abort"), { method: "POST" }),

  /** 项目文件枚举；`signal` 用于输入变化时取消上一次请求。 */
  listProjectFiles: (q: string, signal?: AbortSignal) =>
    call<ProjectFileList>(
      `/v2/project/files?q=${encodeURIComponent(q)}&limit=50`,
      signal ? { signal } : undefined,
    ),

  listCommands: () => call<{ commands: SlashCommand[] }>("/v2/commands"),

  /** Agent 拓扑与生效模型；配置可热更新，所以不能吃浏览器缓存。 */
  listAgents: () => call<AgentsView>("/v2/agents", { cache: "no-store" }),

  /**
   * 列出这一代挂着的 skill。
   *
   * ★ description 一定要在：它是模型选不选这个 skill 的唯一依据，
   *   也是用户「查看」时最想看的那一行。
   */
  listSkills: () => call<{ skills: SkillView[] }>("/v2/skills", { cache: "no-store" }),

  /**
   * 上传走 multipart —— 字段名必须和 Go 侧 `v2_files.go` 的
   * `c.FormFile("files")` 对齐。
   */
  async uploadFiles(id: string, files: readonly File[]): Promise<UploadResult> {
    const form = new FormData()
    for (const f of files) form.append("files", f, f.name)
    const res = await fetch(sessionPath(id, "/files"), { method: "POST", body: form })
    if (!res.ok) {
      const body = (await res.json().catch(() => null)) as ApiErrorBody | null
      throw new ApiError(res.status, body ?? { error: res.statusText, code: "unknown", retryable: false })
    }
    // ★ 返回上传结果而不是 void —— 界面上要显示文件名与落盘路径，
    //   而且「文件到底传上没有」不能靠乐观假设。
    return (await res.json()) as UploadResult
  },
}

// ── /v2/run ───────────────────────────────────────────────────────────────

/** resume 的一次答复。 */
export interface ResumeInput {
  interruptId: string
  approved: boolean
  reason?: string
}

export interface RunInput {
  /**
   * 会话 id。**必填** —— 会话只由 `POST /v2/sessions` 建。
   *
   * ★ 「建不建会话」的决定权在客户端：它知道当前有没有会话。
   *   服务端只能从「有没有 threadId」推断，而那是**结果**不是**决定**。
   */
  threadId: string
  query?: string
  resume?: ResumeInput[]
}

export interface RunHandle {
  abort(): void
}

export interface RunHandlers {
  onEvent(ev: unknown): void
  onError?(message: string): void
  onDone?(): void
}

/**
 * runStream 发起一轮 run 并把 AG-UI 事件逐条交给回调。
 *
 * ★ 请求体只发服务端真正读的东西：`query` 与可选的 `threadId`
 *   （见 api/v2_run.go 的 startRun）。不发 messages 数组 —— 服务端
 *   历史是权威副本，前端再发一份只会让人以为有两个事实源。
 *
 * ★ 返回 RunHandle 而不是 Promise：流是长连接，调用方需要能中途
 *   abort，而且 abort 不能等流结束。
 *
 * 立刻返回，事件在后台异步到达 —— 所以 `await runStream(...)` 什么
 * 也没等到。
 */
export function runStream(input: RunInput, handlers: RunHandlers): RunHandle {
  const controller = new AbortController()

  void (async () => {
    try {
      const res = await fetch("/v2/run", {
        method: "POST",
        signal: controller.signal,
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({
          threadId: input.threadId,
          ...(input.query !== undefined ? { query: input.query } : {}),
          ...(input.resume
            ? { resume: input.resume.map((r) => ({ interruptId: r.interruptId, payload: { approved: r.approved, reason: r.reason ?? "" } })) }
            : {}),
        }),
      })

      if (!res.ok) {
        // 失败发生在流开始之前，所以拿不到 RUN_ERROR，只能直接报错。
        const body = (await res.json().catch(() => null)) as ApiErrorBody | null
        handlers.onError?.(body?.error ?? `HTTP ${res.status}`)
        return
      }
      if (!res.body) {
        handlers.onError?.("response has no body — expected an event stream")
        return
      }

      for await (const ev of readSse(res.body)) {
        try {
          handlers.onEvent(JSON.parse(ev.data))
        } catch {
          // 单条事件解不开不该毁掉整轮 —— 跳过，继续读后面的。
        }
      }
      handlers.onDone?.()
    } catch (e) {
      // abort 是用户的「停止」，不是错误。
      if (controller.signal.aborted) return
      handlers.onError?.((e as Error).message)
    }
  })()

  return { abort: () => controller.abort() }
}

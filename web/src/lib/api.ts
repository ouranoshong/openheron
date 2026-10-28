import { readSse } from "@/lib/sse"

export interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens: number
  cachedTokens: number
  byAgent?: Record<string, Usage>
}

export interface PendingApproval {
  interruptId: string
  reason: string
  message?: string
  toolCallId?: string
  toolName?: string
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

export interface ThreadMeta {
  threadId: string
  title: string
  createdAt: string
  updatedAt: string
  messageCount: number
  state: "idle" | "running" | "waiting_approval"
  hasPendingApproval: boolean
  agentId: string
}

export interface ThreadDetail extends ThreadMeta {
  runId?: string
  startedAt?: string
  lastError?: LastError
  pendingApproval?: PendingApproval
}

export interface AgMessage {
  id: string
  role: "system" | "developer" | "user" | "assistant" | "tool"
  content: unknown
  name?: string
  metadata?: Record<string, unknown>
  toolCallId?: string
  toolCalls?: {
    id: string
    type: "function"
    function: { name: string; arguments: string }
  }[]
  toolCallResults?: unknown[]
}

export interface MessagesView {
  threadId: string
  state: ThreadMeta["state"]
  messages: AgMessage[]
  pendingApprovals?: PendingApproval[]
  usage?: Usage
  agent?: string
}

export interface UploadedFile {
  name: string
  path: string
  size: number
}

export interface UploadResult {
  files: UploadedFile[]
  workspacePath: string
}

export interface ProjectFile {
  path: string
}

export interface ProjectFileList {
  files: ProjectFile[]
  truncated: boolean
}

export type CommandAction =
  | "new_thread"
  | "stop_run"
  | "attach_files"
  | "open_settings"
  | "open_agents"

export interface SlashCommand {
  name: string
  description: string
  argumentHint?: string
  kind: "action" | "prompt"
  action?: CommandAction
}

export interface AgentModelView {
  provider: string
  model: string
  reasoning: string
  reasoningAvailable: string[]
  reasoningDefault: string
}

export interface AgentView {
  id: string
  description?: string
  model: AgentModelView
  maxIterations: number
  subagents: string[]
  disabledTools: string[]
}

export interface SkillView {
  name: string
  description?: string
  context?: string
  agent?: string
  model?: string
}

export interface AgentsView {
  defaultAgent: string
  maxDepth: number
  agents: AgentView[]
  version: string
}

export interface ApiErrorBody {
  error: string
  code: string
  kind?: string
  hint?: string
  retryable: boolean
}

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

function sessionPath(id: string, suffix = ""): string {
  if (!id) {
    throw new Error("session id is missing — the session has not been created yet")
  }
  return `/v2/sessions/${id}${suffix}`
}

export const api = {
  listThreads: () => call<{ items: ThreadMeta[] }>("/v2/sessions"),

  createThread: (agentId?: string) =>
    call<ThreadMeta>("/v2/sessions", {
      method: "POST",
      body: agentId ? JSON.stringify({ agentId }) : undefined,
    }),

  getThread: (id: string) => call<ThreadDetail>(sessionPath(id)),

  renameThread: (id: string, title: string) =>
    call<ThreadMeta>(sessionPath(id), {
      method: "PATCH",
      body: JSON.stringify({ title }),
    }),

  deleteThread: (id: string) => call<void>(sessionPath(id), { method: "DELETE" }),

  switchAgent: (id: string, agentId: string) =>
    call<ThreadMeta>(`${sessionPath(id)}/agent`, {
      method: "PATCH",
      body: JSON.stringify({ agentId }),
    }),

  getMessages: (id: string) => call<MessagesView>(sessionPath(id, "/messages")),

  abortRun: (id: string) => call<{ aborted: boolean }>(sessionPath(id, "/abort"), { method: "POST" }),

  listProjectFiles: (q: string, signal?: AbortSignal) =>
    call<ProjectFileList>(
      `/v2/project/files?q=${encodeURIComponent(q)}&limit=50`,
      signal ? { signal } : undefined,
    ),

  listCommands: () => call<{ commands: SlashCommand[] }>("/v2/commands"),

  listAgents: () => call<AgentsView>("/v2/agents", { cache: "no-store" }),

  listSkills: () => call<{ skills: SkillView[] }>("/v2/skills", { cache: "no-store" }),

  async uploadFiles(id: string, files: readonly File[]): Promise<UploadResult> {
    const form = new FormData()
    for (const f of files) form.append("files", f, f.name)
    const res = await fetch(sessionPath(id, "/files"), { method: "POST", body: form })
    if (!res.ok) {
      const body = (await res.json().catch(() => null)) as ApiErrorBody | null
      throw new ApiError(res.status, body ?? { error: res.statusText, code: "unknown", retryable: false })
    }
    return (await res.json()) as UploadResult
  },
}

// ── /v2/run ───────────────────────────────────────────────────────────────

export interface ResumeInput {
  interruptId: string
  approved: boolean
  reason?: string
}

export interface RunInput {
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
          // A single event that will not parse must not destroy the whole turn — skip it and keep reading.
        }
      }
      handlers.onDone?.()
    } catch (e) {
      if (controller.signal.aborted) return
      handlers.onError?.((e as Error).message)
    }
  })()

  return { abort: () => controller.abort() }
}

import { readSse } from "@/lib/sse"
import type { ContextUsage } from "@/lib/types"

export interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens: number
  cachedTokens: number
  byAgent?: Record<string, Usage>
  // context is the per-agent context occupancy. It is a point-in-time
  // reading, unlike byAgent, which is a cumulative spend.
  context?: Record<string, ContextUsage>
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

// RunView is one run of the session's ledger.
//
// ★ It is the truth about "what is this session doing". The stream is a
// hint; this is the record.
export interface RunView {
  runId: string
  state: "starting" | "running" | "waiting_approval" | "completed" | "failed" | "aborted" | "interrupted"
  kind: "query" | "resume"
  epoch: number
  parentRunId?: string
  startedAt: string
  endedAt?: string
  error?: { code: string; message: string }
}

// RunsView is the scan of a session's runs.
export interface RunsView {
  items: RunView[]
  activeRunId?: string
}

export interface MessagesView {
  threadId: string
  state: ThreadMeta["state"]
  messages: AgMessage[]
  pendingApprovals?: PendingApproval[]
  usage?: Usage
  agent?: string
}

// CompactionResult is what a manual compaction achieved.
export interface CompactionResult {
  threadId: string
  // superseded is how many transcript messages the summary replaced.
  superseded: number
  // savedTokens is the estimated number of tokens the compaction removed.
  savedTokens: number
  // usedTokens is the estimated size of the context that is left.
  usedTokens: number
  // at is when the compaction happened.
  at: string
}

export interface UploadedFile {
  name: string
  // path is relative to the session's workspace — it is the name the server
  // resolves, and the name the client sends back with the next question.
  path: string
  size: number
  // mime is the content type the server derived from the file's name.
  mime: string
}

export interface UploadResult {
  files: UploadedFile[]
  workspacePath: string
}

// WorkspaceFile is one file in a session's workspace.
export interface WorkspaceFile {
  name: string
  path: string
  size: number
  mime: string
  modifiedAt: string
}

export interface WorkspaceFiles {
  workspacePath: string
  files: WorkspaceFile[]
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
  | "open_mcps"
  | "compact"

export interface SlashCommand {
  name: string
  description: string
  argumentHint?: string
  kind: "action" | "prompt"
  action?: CommandAction
}

// MCPServerView is one MCP server, as the runtime holds it.
//
// The status is about the server ("ready", "starting", "unreachable",
// "unused") and the tool rows are about this agent: a tool can be
// available, skipped (the server offered it and it could not be exposed)
// or blocked (this agent does not reach it).
//
// ★ "starting" is its own state: a server that is on its way is doing what
//   it was asked to do, and a reader who cannot tell it from "unreachable"
//   reads a server on its way as a broken one.
export interface MCPServerView {
  name: string
  transport: string
  status: "ready" | "starting" | "unreachable" | "unused"
  error: string
  tools: MCPToolView[]
}

export interface MCPToolView {
  // name is the server's own name — the vocabulary of the file.
  name: string
  // exposedName is the name the model sees.
  exposedName: string
  tier: string
  status: "available" | "skipped" | "blocked"
  reason: string
}

export interface MCPServersView {
  agent: string
  servers: MCPServerView[]
}

export interface AgentModelView {
  provider: string
  model: string
  reasoning: string
  reasoningAvailable: string[]
  reasoningDefault: string
  // contextWindow is the model's context window. Zero means the user did not
  // configure one.
  contextWindow: number
}

export interface AgentView {
  id: string
  description?: string
  model: AgentModelView
  maxIterations: number
  subagents: string[]
  // ★ tools is the agent's tool allow-list. The default is none — an
  //   absent list and an empty list mean the same thing.
  tools: string[]
  // summarizeAt is the compaction threshold of this agent, in tokens. Zero
  // means it never compacts — either because it says so, or because the
  // model declares no window to measure against.
  summarizeAt?: number
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

// fileUrl is the URL of one file's bytes.
//
// ★ It is a **string**, not a fetch: an `<img src>` hands the file to the
//   browser's own picture pipeline — its cache, its concurrency limits, its
//   decoder — and the URL can be copied out of the page.
//
// ★ max asks the server for a smaller picture. It only means something for
//   an image: text and PDF ignore it.
export function fileUrl(sessionId: string, path: string, max?: number): string {
  if (!sessionId) {
    throw new Error("session id is missing — the file belongs to a session")
  }
  const limit = max && max > 0 ? `&max=${Math.trunc(max)}` : ""
  return `/v2/sessions/${sessionId}/files/raw?path=${encodeURIComponent(path)}${limit}`
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

  // listRuns scans the session's run ledger.
  //
  // ★ It is how a client learns whether the run it started is still going.
  //   The stream cannot say that: a broken connection says nothing about
  //   the run, because the run outlives the connection.
  listRuns: (id: string) => call<RunsView>(sessionPath(id, "/runs")),

  abortRun: (id: string) => call<{ aborted: boolean }>(sessionPath(id, "/abort"), { method: "POST" }),

  // compact compacts the session's context now. It is the manual
  // counterpart of the threshold: the same summary, the same boundary.
  compact: (id: string) =>
    call<CompactionResult>(sessionPath(id, "/compact"), { method: "POST" }),

  listProjectFiles: (q: string, signal?: AbortSignal) =>
    call<ProjectFileList>(
      `/v2/project/files?q=${encodeURIComponent(q)}&limit=50`,
      signal ? { signal } : undefined,
    ),

  listCommands: () => call<{ commands: SlashCommand[] }>("/v2/commands"),

  listAgents: () => call<AgentsView>("/v2/agents", { cache: "no-store" }),

  // configVersion reads the configuration's fingerprint, and nothing else.
  //
  // ★ It is the **cheap** way to ask "did the configuration change?": the
  //   agent table is a fat payload, and a watcher only needs this one
  //   string. The health endpoint carries it.
  //
  // ★ An empty string means the server has no configuration to fingerprint —
  //   the caller has to fall back to reading the whole view.
  configVersion: () =>
    call<{ configVersion?: string }>("/v2/health", { cache: "no-store" }).then((v) =>
      v.configVersion ?? "",
    ),

  listSkills: () => call<{ skills: SkillView[] }>("/v2/skills", { cache: "no-store" }),

  // listMCPServers reads the MCP servers of one agent. The agent is
  // optional and defaults to the configuration's default agent.
  //
  // ★ It is never cached: the status is live state — it changes when a
  //   child process dies — and the tab's caches are the place for
  //   configuration, not for liveness.
  listMCPServers: (agent?: string) =>
    call<MCPServersView>(
      agent ? `/v2/mcps?agent=${encodeURIComponent(agent)}` : "/v2/mcps",
      { cache: "no-store" },
    ),

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

  // listFiles reads what is in the session's workspace.
  //
  // ★ It is the server's own account of the files — the client has no
  //   truth of its own to fall back on after a reload.
  listFiles: (id: string) => call<WorkspaceFiles>(sessionPath(id, "/files")),

  // deleteFile removes one file from the session's workspace.
  //
  // ★ The granularity is the **file**. The attachment list of a message is
  //   history, and history is not rewritten when a file is deleted.
  deleteFile: (id: string, path: string) =>
    call<{ deleted: boolean; path: string }>(
      `${sessionPath(id, "/files")}?path=${encodeURIComponent(path)}`,
      { method: "DELETE" },
    ),
}

// ── /v2/run ───────────────────────────────────────────────────────────────

export interface ResumeInput {
  interruptId: string
  approved: boolean
  reason?: string
}

// RunAttachment is one file the client names with its question.
//
// ★ The path is relative to the session's workspace. The server resolves it
//   against the session's own workspace and refuses anything that escapes
//   it, so there is no path a client could send to read a file outside.
export interface RunAttachment {
  name: string
  path: string
  size?: number
}

export interface RunInput {
  threadId: string
  query?: string
  resume?: ResumeInput[]
  // attachments are the files that came with this question. They are
  // recorded on the message, so the history can show them later.
  attachments?: RunAttachment[]
  // runId asks for a specific run. With no query it means "attach to the
  // run I already know" — the server refuses a run that is not live.
  //
  // ★ It is what turns a lost connection into a pause rather than a loss:
  //   the client comes back with the id it was given and gets the rest of
  //   the turn.
  runId?: string
}

export interface RunHandle {
  abort(): void
}

export interface RunHandlers {
  onEvent(ev: unknown): void
  // onError carries the machine-readable code alongside the message.
  //
  // ★ The prose alone cannot tell "the session is busy elsewhere" from
  //   "the run failed": the first one is a conflict the user can act on
  //   (stop the other turn), the second is a failure to report. The
  //   code is what tells them apart — "session_busy" means the session
  //   has a live turn, so the optimistic message must be rolled back.
  onError?(message: string, code?: string): void
  onDone?(): void
}

// FRAME_MS is how often the stream's events reach the store.
//
// ★ A token per state update makes the whole tree re-render per token,
//   and the streaming message's markdown is re-parsed from scratch every
//   time — so the cost of one answer is quadratic in its length. A
//   frame is what the screen can show anyway: batching at the frame
//   rate costs nothing that is visible and takes the parse count down
//   by an order of magnitude.
const FRAME_MS = 33

// URGENT_EVENTS are the frames the user must see **now**.
//
// ★ An interrupt or the end of a run is not a token: the user is about
//   to click something, and a card that arrives a frame late reads as
//   a broken button. They are never batched.
const URGENT_EVENTS = new Set(["RUN_STARTED", "RUN_FINISHED", "RUN_ERROR"])

/**
 * batcher groups the stream's events into frames.
 */
function batcher(flush: (ev: unknown) => void) {
  let queue: unknown[] = []
  let timer: ReturnType<typeof setTimeout> | null = null

  const drain = () => {
    timer = null
    const batch = queue
    queue = []
    for (const ev of batch) flush(ev)
  }

  return {
    push(ev: unknown) {
      const type = (ev as { type?: string }).type
      if (type && URGENT_EVENTS.has(type)) {
        // ★ The urgent frame goes out now, and it takes the queued ones
        //   with it so the order on screen stays the order on the wire.
        if (timer !== null) {
          clearTimeout(timer)
          timer = null
        }
        drain()
        flush(ev)
        return
      }
      queue.push(ev)
      if (timer === null) timer = setTimeout(drain, FRAME_MS)
    },
    // flushAll empties the queue — the stream is over and nothing may
    // be left behind.
    flushAll() {
      if (timer !== null) {
        clearTimeout(timer)
        timer = null
      }
      drain()
    },
  }
}

export function runStream(input: RunInput, handlers: RunHandlers): RunHandle {
  const controller = new AbortController()
  // ★ The batch lives outside the try: a stream that breaks must still
  //   deliver what it already delivered, or the answer the user saw
  //   halfway would vanish with the error.
  const batch = batcher((ev) => handlers.onEvent(ev))

  void (async () => {
    try {
      const res = await fetch("/v2/run", {
        method: "POST",
        signal: controller.signal,
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({
          threadId: input.threadId,
          ...(input.query !== undefined ? { query: input.query } : {}),
          ...(input.attachments !== undefined ? { attachments: input.attachments } : {}),
          ...(input.runId !== undefined ? { runId: input.runId } : {}),
          ...(input.resume
            ? { resume: input.resume.map((r) => ({ interruptId: r.interruptId, payload: { approved: r.approved, reason: r.reason ?? "" } })) }
            : {}),
        }),
      })

      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as ApiErrorBody | null
        handlers.onError?.(body?.error ?? `HTTP ${res.status}`, body?.code)
        return
      }
      if (!res.body) {
        handlers.onError?.("response has no body — expected an event stream")
        return
      }

      // ★ The events are batched into frames (see FRAME_MS). The order is
      //   the wire's order; only the timing changes.
      for await (const ev of readSse(res.body)) {
        try {
          batch.push(JSON.parse(ev.data))
        } catch {
          // A single event that will not parse must not destroy the whole turn — skip it and keep reading.
        }
      }
      batch.flushAll()
      handlers.onDone?.()
    } catch (e) {
      // ★ Whatever the stream managed to deliver still belongs to the
      //   turn: the user saw half an answer, and half an answer is better
      //   than none.
      batch.flushAll()
      if (controller.signal.aborted) return
      handlers.onError?.((e as Error).message)
    }
  })()

  return { abort: () => controller.abort() }
}

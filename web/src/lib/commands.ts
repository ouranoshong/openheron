import { api, type SlashCommand } from "@/lib/api"
import type { TurnStatus } from "@/lib/types"

/**
 * The command table is static on the server, so the first successful fetch is
 * cached for the tab's lifetime. A failed fetch clears both cache and in-flight
 * promise so reopening the popup retries instead of pinning an empty list.
 */
let cached: SlashCommand[] | null = null
let inflight: Promise<SlashCommand[]> | null = null

export function loadCommands(): Promise<SlashCommand[]> {
  if (cached) return Promise.resolve(cached)
  if (!inflight) {
    inflight = api
      .listCommands()
      .then((res) => {
        cached = res.commands
        return res.commands
      })
      .catch((err) => {
        cached = null
        throw err
      })
      .finally(() => {
        inflight = null
      })
  }
  return inflight
}

/**
 * Commands the current turn can actually use. Stopping only makes sense while
 * a run is live; attaching only while no upload is in flight — showing a
 * menu item that would do nothing is worse than hiding it.
 */
export function filterCommands(
  commands: readonly SlashCommand[],
  query: string,
  opts: { status: TurnStatus; uploading: boolean },
): SlashCommand[] {
  const q = query.trim().toLowerCase()
  return commands.filter((cmd) => {
    if (cmd.action === "stop_run" && opts.status !== "running") return false
    if (cmd.action === "attach_files" && opts.uploading) return false
    if (!q) return true
    return cmd.name.toLowerCase().includes(q) || cmd.description.toLowerCase().includes(q)
  })
}

/**
 * Action commands take no arguments: the whole message must be exactly
 * `/name`. Otherwise a question like "/new is not working" would switch
 * threads instead of being sent.
 */
export function actionCommandFor(
  text: string,
  commands: readonly SlashCommand[],
): SlashCommand | null {
  const m = text.trim().match(/^\/([a-z0-9_-]+)$/i)
  if (!m) return null
  const name = m[1].toLowerCase()
  return commands.find((cmd) => cmd.kind === "action" && cmd.name === name) ?? null
}

/** `/agents` 的两种结果：打开选择器，或直接切到某个名字。 */
export interface AgentCommand {
  mode: "open" | "switch"
  /** switch 模式下用户写的名字；open 模式下可作为过滤词。 */
  agentId?: string
}

/**
 * `/skill` 的两种结果：打开选择器，或按名字执行。
 */
export interface SkillCommand {
  mode: "open" | "run"
  /** run 模式下用户写的 skill 名；open 模式下可作为过滤词。 */
  name?: string
}

/**
 * skillCommandFor 解析整条 `/skill [name] [task]`。
 *
 * ★ 整条匹配：正文里提到 /skill 不该触发执行 —— `/skillx` 同理。
 *
 * ★ 这里**只认命令名**（后端命令表里有它才算数）：老后端没有 /skill 时，
 *   这行文本按普通消息发出去，而不是被前端吞掉。
 *
 * ★ 名字的有效性**不在这里**判：那要 skill 清单，是调用方的上下文。
 *   这里只区分「有名字」与「没名字」。未知名字由调用方退化成
 *   「打开选择器并用它过滤」（与 /agents 同一套处理）。
 */
export function skillCommandFor(
  text: string,
  commands: readonly SlashCommand[],
): SkillCommand | null {
  const m = text.trim().match(/^\/skill(?:\s+(\S+))?\s*$/i)
  if (!m) return null
  const known = commands.some((cmd) => cmd.name === "skill")
  if (!known) return null
  const arg = m[1]?.trim()
  return arg ? { mode: "run", name: arg } : { mode: "open" }
}

/**
 * agentCommandFor 解析整条 `/agents [name]`。
 *
 * ★ 必须整条匹配：正文里提到 /agents 不该触发切换。`/agentsx` 同理 ——
 *   命令名后面直接跟字母说明它是另一个词，不是一个带参数的调用。
 *
 * ★ 命令存在性由服务端表决定：老后端没有 /agents 时返回 null，这行文本
 *   按普通消息发出去，而不是被前端吞掉。
 *
 * ★ 这里**不判断名字是否有效**。有效性要拿 agent 列表，那是调用方的
 *   上下文；这里只区分「有参数」与「没参数」。未知名字由调用方退化成
 *   「打开选择器并用它过滤」，见 §13.6。
 */
export function agentCommandFor(
  text: string,
  commands: readonly SlashCommand[],
): AgentCommand | null {
  const m = text.trim().match(/^\/agents(?:\s+(\S+))?\s*$/i)
  if (!m) return null
  const known = commands.some((cmd) => cmd.kind === "action" && cmd.name === "agents")
  if (!known) return null
  const arg = m[1]?.trim()
  return arg ? { mode: "switch", agentId: arg } : { mode: "open" }
}

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

export interface AgentCommand {
  mode: "open" | "switch"
  agentId?: string
}

export interface SkillCommand {
  mode: "open" | "run"
  name?: string
}

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

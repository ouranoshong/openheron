import { api, type AgentView, type AgentsView } from "@/lib/api"

export interface AgentBadge {
  id: string
  description?: string
  provider: string
  model: string
  reasoning: string
  reasoningAvailable: string[]
}

const EMPTY_LEVELS: string[] = []

export function badgeForAgent(view: AgentsView, agentId?: string): AgentBadge | null {
  const wanted = agentId || view.defaultAgent
  const agent =
    view.agents.find((a) => a.id === wanted) ??
    view.agents.find((a) => a.id === view.defaultAgent) ??
    view.agents[0]
  if (!agent) return null
  return badgeOf(agent)
}

export function badgeFromAgents(view: AgentsView): AgentBadge | null {
  return badgeForAgent(view)
}

function badgeOf(agent: AgentView): AgentBadge {
  const model = agent.model
  return {
    id: agent.id || "unknown",
    description: agent.description,
    provider: model?.provider || "unknown",
    model: model?.model || "unknown",
    reasoning: model?.reasoning || "off",
    reasoningAvailable: model?.reasoningAvailable ?? EMPTY_LEVELS,
  }
}

export function selectableAgents(view: AgentsView): AgentView[] {
  return view.agents ?? []
}

export function teamSize(agent: AgentView): number {
  return agent.subagents?.length ?? 0
}

let cached: AgentsView | null = null
let inflight: Promise<AgentsView | null> | null = null

export function loadAgents(force = false): Promise<AgentsView | null> {
  if (!force && cached) return Promise.resolve(cached)
  if (inflight) return inflight

  inflight = api
    .listAgents()
    .then((view) => {
      cached = view
      return view
    })
    .catch((err: unknown) => {
      if (cached) return cached
      throw err
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

// checkConfigChanged asks whether the configuration moved.
//
// ★ It asks the **cheap** way: the health endpoint carries the
//   configuration's fingerprint and nothing else, while the agent table is
//   a fat payload. ★ A watcher that pulled the whole table every three
//   seconds to read one field was paying for the answer before it knew
//   there was an answer.
//
// ★ It does not refresh the view. It says "it moved"; the caller fetches
//   the view that says **how** it moved — and only when it moved.
export async function checkConfigChanged(): Promise<boolean> {
  try {
    const version = await api.configVersion()
    if (version) {
      if (!cached) {
        // ★ There is nothing to compare against yet: the badge has not been
        //   loaded, so the client knows no version.
        return true
      }
      return version !== cached.version
    }
    // ★ The server could not give the cheap signal — it has no
    //   configuration to fingerprint. Fall back to the fat answer.
    const view = await api.listAgents()
    if (!cached) {
      cached = view
      return true
    }
    if (view.version === cached.version) return false
    cached = view
    return true
  } catch {
    // ★ A failed check says nothing. The badge keeps what it has, and the
    //   next tick tries again.
    return false
  }
}

export function loadAgentBadge(force = false): Promise<AgentBadge | null> {
  return loadAgents(force).then((view) => (view ? badgeFromAgents(view) : null))
}

export function resetAgentBadgeCache(): void {
  cached = null
  inflight = null
}

import { api, type AgentView, type AgentsView } from "@/lib/api"

/**
 * Composer 底栏展示用的 agent 摘要。
 *
 * ★ 只读、来自 `/v2/agents` 的配置视图。这里不推断运行中的 agent：
 *   会话的活动 agent 来自 threadMeta.agentId（或欢迎页的暂存值），
 *   两者都在 store 里，不在这个文件的职责内。
 */
export interface AgentBadge {
  id: string
  description?: string
  provider: string
  model: string
  reasoning: string
  reasoningAvailable: string[]
}

const EMPTY_LEVELS: string[] = []

/**
 * badgeForAgent 从拓扑里挑出指定 agent。
 *
 * ★ 按 id 找，而不是取 `agents[0]` —— 配置里的顺序没有语义，一个 agent
 *   完全可能排在另一个前面。找不到时回退 defaultAgent，再回退第一条：
 *   切换后配置恰好被热改掉时，界面显示一个真实的 agent 比空着好。
 */
export function badgeForAgent(view: AgentsView, agentId?: string): AgentBadge | null {
  const wanted = agentId || view.defaultAgent
  const agent =
    view.agents.find((a) => a.id === wanted) ??
    view.agents.find((a) => a.id === view.defaultAgent) ??
    view.agents[0]
  if (!agent) return null
  return badgeOf(agent)
}

/** badgeFromAgents 是「默认 agent」的简写，保留给既有调用点与测试。 */
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
    // 空档位不是「还没加载」，而是这个模型没有注入任何思考字段。
    reasoning: model?.reasoning || "off",
    reasoningAvailable: model?.reasoningAvailable ?? EMPTY_LEVELS,
  }
}

/**
 * selectableAgents 返回能当会话 agent 的 agent —— **全部**。
 *
 * ★ 没有「候选名单」这回事：每个声明了的 agent 都能当会话的 agent，
 *   装配按它自己的配置来（它自己的正文、模型、工具边界，以及它声明的
 *   那几个下属）。所以前端不需要后端额外给一份名单。
 *
 * ★ 这里只用于**渲染选择器**；是否允许切换仍由 PATCH 的 4xx 回答。
 */
export function selectableAgents(view: AgentsView): AgentView[] {
  return view.agents ?? []
}

/**
 * teamSize 返回一个 agent 的队伍规模（它的直接下属数）。
 *
 * ★ 用它是为了让选择器能一眼看出「切过去会带几个人」——
 *   切到一个光杆 agent 与切到一支队伍，是两种完全不同的事。
 */
export function teamSize(agent: AgentView): number {
  return agent.subagents?.length ?? 0
}

let cached: AgentsView | null = null
let inflight: Promise<AgentsView | null> | null = null

/**
 * loadAgents 取一次拓扑，并在模块内缓存。
 *
 * - 非 force 的并发调用共享同一个请求；
 * - 配置热更新后由调用方传 force 重新拉取；
 * - 刷新失败时保留旧值：底栏是辅助信息，不能因为一次 5xx 变成空白。
 */
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

/**
 * checkConfigChanged 比对**配置指纹**，变了就把缓存换成新的。
 *
 * ★ 为什么需要它：用户可以在**编辑器里**改 `agents/*.md` / `llm/*.yaml`。
 *   服务端会热更新（config/reload.go），但浏览器此前**没有任何信号** ——
 *   而这份列表是模块级缓存，于是界面一直显示过时的 agent 名单。
 *   「设置弹窗关闭时 force 刷新」只覆盖**在应用里改**那条路。
 *
 * ★ 比对**指纹**而不是名单本身：改了 `llm/*.yaml` 时 agent 名单没变，
 *   但每个 agent 的模型与思考档位变了 —— 只比名单会漏掉那种改动。
 *
 * @returns 界面该刷新了吗（是的话缓存已换成新的）
 */
export async function checkConfigChanged(): Promise<boolean> {
  try {
    const view = await api.listAgents()
    if (!cached) {
      // ★ 原来一份都没有（首屏那次请求失败了）—— 对界面来说这就是「变了」：
      //   底栏与选择器还空着，得让它拿到这份。
      cached = view
      return true
    }
    if (view.version === cached.version) return false
    cached = view
    return true
  } catch {
    // ★ 拉不到就当没变：一次 5xx 不该把界面清空，也不该刷屏报错。
    return false
  }
}

/** loadAgentBadge 取默认 agent 的摘要（既有调用点的兼容入口）。 */
export function loadAgentBadge(force = false): Promise<AgentBadge | null> {
  return loadAgents(force).then((view) => (view ? badgeFromAgents(view) : null))
}

/** 测试用：清空模块缓存，避免用例之间互相污染。 */
export function resetAgentBadgeCache(): void {
  cached = null
  inflight = null
}

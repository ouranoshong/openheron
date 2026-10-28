import { api, type SkillView } from "@/lib/api"

/**
 * skill 清单的加载与缓存。
 *
 * ★ 缓存与 [agent-info] 同一套理由：清单是**辅助信息**，不该每次开选择器
 *   都打一次服务端；失败时保留旧值（空列表也算旧值 —— 底栏/选择器不能
 *   因为一次 5xx 变成空白）。
 *
 * ★ 清单是**活**的：skills 目录不在热更新范围内，但 eino 的 backend 每次
 *   List 都重扫目录，所以新建的 skill 下一次就出现在清单里。
 */
let cached: SkillView[] | null = null
let inflight: Promise<SkillView[]> | null = null

/**
 * loadSkills 取一次 skill 清单，并在模块内缓存。
 *
 * - 非 force 的并发调用共享同一个请求；
 * - 失败时保留旧值；
 * - 没有 skills 目录时是**空列表**（不是错误）——「这个 agent 不带说明书」
 *   是合法状态。
 */
export function loadSkills(force = false): Promise<SkillView[]> {
  if (!force && cached) return Promise.resolve(cached)
  if (inflight) return inflight

  inflight = api
    .listSkills()
    .then((res) => {
      cached = res.skills ?? []
      return cached
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
 * skillModeLabel 把执行模式说成人话。
 *
 * ★ 界面上一个空的「模式」列没人知道意味着什么 —— inline 是「内容回到
 * 当前 agent」，fork 是「另起一个 agent 跑」。
 */
export function skillModeLabel(skill: SkillView): string {
  switch (skill.context) {
    case "fork":
      return "另起 agent 执行"
    case "fork_with_context":
      return "另起 agent 执行（带上下文）"
    case "inline":
    default:
      return "当前 agent 内执行"
  }
}

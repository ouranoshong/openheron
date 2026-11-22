import { api, type SkillView } from "@/lib/api"
import type { Translate } from "@/lib/i18n"

let cached: SkillView[] | null = null
let inflight: Promise<SkillView[]> | null = null

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

export function skillModeLabel(skill: SkillView, t: Translate): string {
  switch (skill.context) {
    case "fork":
      return t("skill.mode.fork")
    case "fork_with_context":
      return t("skill.mode.forkWithContext")
    case "inline":
    default:
      return t("skill.mode.inline")
  }
}

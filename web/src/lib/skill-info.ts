import { api, type SkillView } from "@/lib/api"

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

export function skillModeLabel(skill: SkillView): string {
  switch (skill.context) {
    case "fork":
      return "Run in a separate agent"
    case "fork_with_context":
      return "Run in a separate agent (with context)"
    case "inline":
    default:
      return "Run in the current agent"
  }
}

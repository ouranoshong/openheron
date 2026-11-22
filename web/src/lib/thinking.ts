
import type { Translate } from "./i18n"

export interface ThinkingState {
  complete: boolean
  elapsedMs: number | null
  interrupted?: boolean
}

export function formatThinkingDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "0s"
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.round(ms / 1000)}s`
}

export function thinkingLabel(state: ThinkingState, t: Translate): string {
  if (!state.complete) return t("thinking.running")
  if (state.interrupted) {
    return state.elapsedMs === null
      ? t("thinking.interrupted")
      : t("thinking.interruptedFor", { duration: formatThinkingDuration(state.elapsedMs) })
  }
  if (state.elapsedMs === null) return t("thinking.done")
  return t("thinking.doneFor", { duration: formatThinkingDuration(state.elapsedMs) })
}


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

export function thinkingLabel(state: ThinkingState): string {
  if (!state.complete) return "Thinking…"
  if (state.interrupted) {
    return state.elapsedMs === null
      ? "Interrupted"
      : `Interrupted after ${formatThinkingDuration(state.elapsedMs)}`
  }
  if (state.elapsedMs === null) return "Thought"
  return `Thought for ${formatThinkingDuration(state.elapsedMs)}`
}

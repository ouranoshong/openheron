/**
 * 思维链的展示状态。
 *
 * ★ 纯函数放这里，组件只负责**取时间**与渲染 —— 按 AGENTS.md 的分层，
 *   逻辑要落在 lib/ 里保持纯函数，组件里不写判断。
 */

/** 一段思维链的状态。 */
export interface ThinkingState {
  /** 思维链是否已经结束（不再接收内容）。 */
  complete: boolean
  /**
   * 从开始到结束的毫秒数。
   *
   * ★ null = **不知道**，不是 0：
   *   - 还在想（complete=false）；
   *   - 或者来自历史回放 —— 那时没有计时，只能说「想过」，说不出多久。
   */
  elapsedMs: number | null
  /**
   * 是否**被掐断**（用户按了停止，或那一轮报错）。
   *
   * ★ 与 complete 正交：「不再接收内容」不等于「想完了」。
   *   停止时那条思维链是被砍断的，说它「Thought for 2.1s」会让人以为
   *   它想完了 —— 那是两句不同的话。
   */
  interrupted?: boolean
}

/**
 * 把毫秒数格式化成给人看的时长。
 *
 * ★ 一秒以下给一位小数（"0.4s"）—— 四舍五入成 "0s" 会读成「没花时间」。
 *   十秒以上给整数秒（"12s"）—— 一位小数在那个量级上是噪音。
 */
export function formatThinkingDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "0s"
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.round(ms / 1000)}s`
}

/**
 * 思维链标题的文案。
 *
 * ★ 四种状态：
 *   - 还在想："Thinking…"
 *   - 想完了、知道多久："Thought for 3.2s"
 *   - 想完了、不知道多久（历史回放）："Thought"
 *   - 被掐断（停止 / 报错）："Interrupted after 3.2s"
 *
 * ★ 用 "Thought" 而不是 "Thinking"：结束之后它**是**一段已经想完的思考，
 *   而 "Thinking" 读起来像「还在想」—— 标题停在 "Thinking" 会让人以为
 *   界面卡住了。
 *
 * ★ 被掐断的**不**说 "Thought"：它没想完。混起来会把「我让它停了」说成
 *   「它想完了」，于是那句总结里的结论看上去比实际更笃定。
 */
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

"use client"

/**
 * 「当前是哪个会话」的客户端记忆。
 *
 * ★ 这里存的就是服务端 sessionId，没有第二个 id。
 *
 *   上一版存的是本地「槽位」：服务端会话要等点击发送才创建，前端于是先
 *   自造一个占位，然后要一整套注册表去把 slot 收敛成 sessionId ——
 *   收敛失败时 history 打 404，历史整个空掉。
 *
 *   现在 /v2/run 的 threadId 可选（见 api/v2_run.go），会话 id 由服务端
 *   在 RUN_STARTED 里回传，store 收编后写到这里。于是：
 *
 *     打开页面 / 点「New thread」/ 什么都不说 → 服务端一个会话都不增加
 *     刷新页面 → 回到同一个对话，且那个对话一定真实存在
 *
 * 零请求。
 */

const KEY = "openheron.activeThread"

let writeWarned = false

export function readActiveThreadId(): string | undefined {
  try {
    return localStorage.getItem(KEY) ?? undefined
  } catch {
    // 隐私模式下 localStorage 会抛异常。降级成「没有记忆」——
    // 顶多回到欢迎界面，不会让应用起不来。
    return undefined
  }
}

/**
 * writeActiveThreadId 记住当前会话。
 *
 * ★ 传空串表示「回到欢迎界面」，这是刻意与 undefined 不同的：
 *   空串会把 localStorage 清掉，刷新后不会又跳回上一个对话；
 *   而 undefined 表示「还没存过」。
 */
export function writeActiveThreadId(id: string): void {
  try {
    if (id) localStorage.setItem(KEY, id)
    else localStorage.removeItem(KEY)
  } catch {
    // ★ 这里曾经是静默的，但语义已经变了。
    //
    //   它存的不只是「上次打开哪个会话」这种便利信息，而是**服务端认可的
    //   sessionId**。写不进去的话，刷新后只能停在欢迎界面 ——
    //   表现为「刷新一下，刚才那个对话就不见了」。
    //
    //   所以失败要说出来：只警告一次，否则隐私模式反复刷屏。
    if (!writeWarned) {
      writeWarned = true
      console.warn(
        "[openheron] cannot persist the active thread id: the current conversation will not survive a page reload",
      )
    }
  }
}

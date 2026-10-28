import { expect, test } from "@playwright/test"

import {
  COMPOSER,
  SEND_BUTTON,
  openApp,
  openThread,
  resetBackend,
  sendMessage,
  setAgentsMode,
  setScenario,
} from "./fixtures"

const LIST = '[data-testid="message-list"]'
const FILTER = '[data-testid="agent-filter"]'
const PICKER = '[data-testid="agent-picker"]'
const OPTION = (id: string) => `[data-testid="agent-option"][data-agent-id="${id}"]`
const MULTI = "Multi agent conversation"

test.beforeEach(async () => {
  await resetBackend()
})

/**
 * 多 agent 会话要能**按 agent 切**。
 *
 * ★ 这是多 agent 模式最要紧的一件事：消息流是交错的，不切就看不出
 *   「某个子 agent 到底干了什么」。
 */
test("多 agent 会话有过滤条，能按 agent 切消息", async ({ page }) => {
  await openApp(page)
  await openThread(page, MULTI)

  const filter = page.locator(FILTER)
  await expect(filter).toBeVisible()

  // 全部：4 条（1 条 user + 3 条 assistant）
  await expect(page.locator('[data-testid="message"]')).toHaveCount(4)

  await page.locator('[data-testid="agent-filter-coder"]').click()
  // coder 的 2 条 + user 那 1 条（用户自己说的话不该被过滤掉）
  await expect(page.locator('[data-testid="message"]')).toHaveCount(3)
  await expect(page.locator(LIST)).toContainText("MARKER-MULTI-CODER")
  await expect(page.locator(LIST)).not.toContainText("MARKER-MULTI-MAIN planning")

  // 再点一次回到全部 —— chip 是可切换的，不是单向的。
  await page.locator('[data-testid="agent-filter-coder"]').click()
  await expect(page.locator('[data-testid="message"]')).toHaveCount(4)
})

/**
 * 分账要看得见：谁花的 token。
 *
 * ★ chip 上的数字就是分账 —— 「总用量」回答不了「谁贵」。
 */
test("过滤条上显示每个 agent 的用量分账", async ({ page }) => {
  await openApp(page)
  await openThread(page, MULTI)

  const main = page.locator('[data-testid="agent-filter-main"]')
  const coder = page.locator('[data-testid="agent-filter-coder"]')
  await expect(main).toContainText("600")
  await expect(coder).toContainText("600")
  await expect(main).toHaveAttribute("title", /main — 600 tokens/)
})

/**
 * 多 agent 会话里，角色标签显示**是谁**而不是「Assistant」。
 *
 * ★ 单 agent 会话不显示 agent 名 —— 那时每条都标同一个名字，等于没信息。
 */
test("多 agent 会话的角色标签显示 agent 名", async ({ page }) => {
  await openApp(page)
  await openThread(page, MULTI)

  const labels = page.locator('[data-testid="role-label"]')
  await expect(labels.first()).toHaveText("main")
  await expect(labels.nth(1)).toHaveText("coder")
})

/**
 * ★ 归属必须在**实时流**里就生效。
 *
 * 历史回放路径一直是对的（后端把归属填进 AG-UI 的 message.name），
 * 而实时路径曾经把 name 挂到了 state 上而不是消息上 —— 于是流里跑着的
 * 那一轮完全没有归属：界面不显示 agent 名，按 agent 过滤也筛不掉东西。
 * 这条用例盯的是「流还没结束，过滤条就已经在」。
 */
test("实时流里两个 agent 交错，过滤条立刻可用", async ({ page }) => {
  await setScenario("multiAgentStream")
  await openApp(page)

  await page.locator(COMPOSER).fill("并行做两件事")
  await page.locator(SEND_BUTTON).click()

  // 流还没结束，过滤条就该在 —— 归属是当场定的，不是事后补的。
  const filter = page.locator(FILTER)
  await expect(filter).toBeVisible()
  await expect(page.locator('[data-testid="agent-filter-coder"]')).toBeVisible()
  await expect(page.locator('[data-testid="agent-filter-reviewer"]')).toBeVisible()

  // 交错的内容没串：coder 的消息里只有 coder 的字。
  await page.locator('[data-testid="agent-filter-coder"]').click()
  await expect(page.locator(LIST)).toContainText("MARKER-CODER")
  await expect(page.locator(LIST)).not.toContainText("MARKER-REVIEWER")
})

/**
 * 单 agent 会话**不出现**过滤条。
 *
 * ★ 多一行 chrome 只会让人以为这里能点什么 —— 而它确实没什么可点。
 */
test("单 agent 会话不显示过滤条", async ({ page }) => {
  await openApp(page)
  await openThread(page, "History conversation")

  await expect(page.locator('[data-testid="message"]')).toHaveCount(3)
  await expect(page.locator(FILTER)).toHaveCount(0)
})

/**
 * ★ 选择器列**全部** agent —— 没有「主 agent」这回事。
 *
 * 每个声明了的 agent 都能当会话的 agent（装配按它自己的配置来），所以
 * 嵌套的下属也在名单里。
 */
test("选择器列出全部 agent，并显示每个 agent 的团队规模", async ({ page }) => {
  await setAgentsMode("multi")
  await openApp(page)

  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()

  // main → lead → coder：三层都在。
  await expect(page.locator(OPTION("main"))).toBeVisible()
  await expect(page.locator(OPTION("lead"))).toBeVisible()
  await expect(page.locator(OPTION("coder"))).toBeVisible()

  // 队伍规模可见：main 带 lead，lead 带 coder。
  await expect(
    page.locator('[data-testid="agent-option"][data-agent-id="main"] [data-testid="agent-option-team"]'),
  ).toHaveText("1")
  await expect(
    page.locator('[data-testid="agent-option"][data-agent-id="lead"] [data-testid="agent-option-team"]'),
  ).toHaveText("1")
  // coder 是光杆 —— 没有队伍规模徽标（0 不占地方）。
  await expect(
    page.locator('[data-testid="agent-option"][data-agent-id="coder"] [data-testid="agent-option-team"]'),
  ).toHaveCount(0)
})

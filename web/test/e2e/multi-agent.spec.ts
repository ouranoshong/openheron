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

test("多 agent 会话有过滤条，能按 agent 切消息", async ({ page }) => {
  await openApp(page)
  await openThread(page, MULTI)

  const filter = page.locator(FILTER)
  await expect(filter).toBeVisible()

  await expect(page.locator('[data-testid="message"]')).toHaveCount(4)

  await page.locator('[data-testid="agent-filter-coder"]').click()
  await expect(page.locator('[data-testid="message"]')).toHaveCount(3)
  await expect(page.locator(LIST)).toContainText("MARKER-MULTI-CODER")
  await expect(page.locator(LIST)).not.toContainText("MARKER-MULTI-MAIN planning")

  await page.locator('[data-testid="agent-filter-coder"]').click()
  await expect(page.locator('[data-testid="message"]')).toHaveCount(4)
})

test("过滤条上显示每个 agent 的用量分账", async ({ page }) => {
  await openApp(page)
  await openThread(page, MULTI)

  const main = page.locator('[data-testid="agent-filter-main"]')
  const coder = page.locator('[data-testid="agent-filter-coder"]')
  await expect(main).toContainText("600")
  await expect(coder).toContainText("600")
  await expect(main).toHaveAttribute("title", /main — 600 tokens/)
})

test("多 agent 会话的角色标签显示 agent 名", async ({ page }) => {
  await openApp(page)
  await openThread(page, MULTI)

  const labels = page.locator('[data-testid="role-label"]')
  await expect(labels.first()).toHaveText("main")
  await expect(labels.nth(1)).toHaveText("coder")
})

test("实时流里两个 agent 交错，过滤条立刻可用", async ({ page }) => {
  await setScenario("multiAgentStream")
  await openApp(page)

  await page.locator(COMPOSER).fill("并行做两件事")
  await page.locator(SEND_BUTTON).click()

  const filter = page.locator(FILTER)
  await expect(filter).toBeVisible()
  await expect(page.locator('[data-testid="agent-filter-coder"]')).toBeVisible()
  await expect(page.locator('[data-testid="agent-filter-reviewer"]')).toBeVisible()

  await page.locator('[data-testid="agent-filter-coder"]').click()
  await expect(page.locator(LIST)).toContainText("MARKER-CODER")
  await expect(page.locator(LIST)).not.toContainText("MARKER-REVIEWER")
})

test("单 agent 会话不显示过滤条", async ({ page }) => {
  await openApp(page)
  await openThread(page, "History conversation")

  await expect(page.locator('[data-testid="message"]')).toHaveCount(3)
  await expect(page.locator(FILTER)).toHaveCount(0)
})

test("选择器列出全部 agent，并显示每个 agent 的团队规模", async ({ page }) => {
  await setAgentsMode("multi")
  await openApp(page)

  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()

  await expect(page.locator(OPTION("main"))).toBeVisible()
  await expect(page.locator(OPTION("lead"))).toBeVisible()
  await expect(page.locator(OPTION("coder"))).toBeVisible()

  await expect(
    page.locator('[data-testid="agent-option"][data-agent-id="main"] [data-testid="agent-option-team"]'),
  ).toHaveText("1")
  await expect(
    page.locator('[data-testid="agent-option"][data-agent-id="lead"] [data-testid="agent-option-team"]'),
  ).toHaveText("1")
  await expect(
    page.locator('[data-testid="agent-option"][data-agent-id="coder"] [data-testid="agent-option-team"]'),
  ).toHaveCount(0)
})

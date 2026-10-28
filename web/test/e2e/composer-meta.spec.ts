import { expect, test } from "@playwright/test"

import { COMPOSER, openApp, resetBackend, trackRequests } from "./fixtures"

const META = '[data-testid="composer-meta"]'
const AGENT = '[data-testid="composer-meta-agent"]'
const MODEL = '[data-testid="composer-meta-model"]'
const REASONING = '[data-testid="composer-meta-reasoning"]'
const SETTINGS_ENTRY = '[data-testid="settings-entry"]'
const SETTINGS_DIALOG = '[data-testid="settings-dialog"]'

test.beforeEach(async () => {
  await resetBackend()
})

test("底栏显示默认 agent、厂商/模型与思考档位", async ({ page }) => {
  await openApp(page)

  await expect(page.locator(META)).toBeVisible()
  await expect(page.locator(AGENT)).toHaveText("Agent: main")
  await expect(page.locator(MODEL)).toHaveText("deepseek/deepseek-v4")
  await expect(page.locator(REASONING)).toHaveText("Reasoning: high")
  await expect(page.locator(REASONING)).toHaveAttribute(
    "title",
    /Reasoning: high\nAvailable: high, low, off/,
  )
})

test("agent 接口不可用时只弱化信息条，不阻塞输入", async ({ page }) => {
  await page.route("**/v2/agents", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: "config unavailable",
        code: "config_unavailable",
        retryable: true,
      }),
    }),
  )
  await openApp(page)

  const unavailable = page.locator('[data-testid="composer-meta-error"]')
  await expect(unavailable).toHaveText("Agent info unavailable")
  await expect(unavailable).toHaveAttribute("title", "config unavailable")
  await expect(page.locator(COMPOSER)).toBeVisible()
})

test("设置弹窗关闭后重新拉取 agent 信息", async ({ page }) => {
  await openApp(page)
  await expect(page.locator(AGENT)).toHaveText("Agent: main")

  const requests = trackRequests(page)
  const before = requests.filter((p) => p === "/v2/agents").length

  await page.click(SETTINGS_ENTRY)
  await expect(page.locator(SETTINGS_DIALOG)).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.locator(SETTINGS_DIALOG)).toHaveCount(0)

  await expect
    .poll(() => requests.filter((p) => p === "/v2/agents").length)
    .toBeGreaterThan(before)
})

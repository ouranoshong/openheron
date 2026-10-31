import { expect, test } from "@playwright/test"

import { COMPOSER, openApp, openThread, resetBackend, trackRequests } from "./fixtures"

const META = '[data-testid="composer-meta"]'
const AGENT = '[data-testid="composer-meta-agent"]'
const MODEL = '[data-testid="composer-meta-model"]'
const REASONING = '[data-testid="composer-meta-reasoning"]'
const SETTINGS_ENTRY = '[data-testid="settings-entry"]'
const SETTINGS_DIALOG = '[data-testid="settings-dialog"]'
const CONTEXT = '[data-testid="composer-meta-context"]'

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

test("底栏显示该 agent 的上下文占用", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Multi agent conversation")

  const ctx = page.locator(CONTEXT)
  await expect(ctx).toBeVisible()
  await expect(ctx).toContainText("Context: ≈18k / 131k (14 %)")
  await expect(ctx).toHaveAttribute(
    "title",
    /Tokens in this agent's context/,
  )
})

// A context that holds a fraction of a percent must not read as "0 %".
//
// ★ The percentage is the number people watch, and "0 %" reads like "there
//   is nothing in the context" — which is exactly the moment the number
//   starts to matter.
test("占比很小时不四舍五入成 0 %", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Tiny context conversation")

  const ctx = page.locator(CONTEXT)
  await expect(ctx).toBeVisible()
  await expect(ctx).toContainText("Context: ≈4.3k / 1.3M (0.3 %)")
  await expect(ctx).not.toContainText("(0 %)")
})

test("模型没声明窗口时只报绝对 token 数，不报比例", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")

  const ctx = page.locator(CONTEXT)
  await expect(ctx).toBeVisible()
  await expect(ctx).toContainText("Context: ≈1.2k tokens")
  await expect(ctx).not.toContainText("%")
  await expect(ctx).toHaveAttribute("title", /no context window/)
})

test("agent 过滤器按 agent 显示各自的占用", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Multi agent conversation")

  await expect(page.locator('[data-testid="agent-filter-main-context"]')).toHaveText("18k/131k")
  await expect(page.locator('[data-testid="agent-filter-coder-context"]')).toHaveText("6.1k/8.2k")
})

// The threshold is the agent's own: the chip says where its context gets cut.
test("agent 过滤器报出这个 agent 的压缩阈值", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Multi agent conversation")

  const main = page.locator('[data-testid="agent-filter-main"]')
  await expect(main).toHaveAttribute("title", /Compacted at: 104858 tokens/)
  // The agent without a threshold says nothing about one.
  const coder = page.locator('[data-testid="agent-filter-coder"]')
  await expect(coder).not.toHaveAttribute("title", /Compacted at/)
})

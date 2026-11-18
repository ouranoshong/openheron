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

test("agent 过滤器按 agent 报出各自的占用", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Multi agent conversation")

  // ★ The occupancy is in the tooltip: the chip itself only names the agent.
  await expect(page.locator('[data-testid="agent-filter-main"]')).toHaveAttribute(
    "title",
    /Context: ≈18k \/ 131k/,
  )
  await expect(page.locator('[data-testid="agent-filter-coder"]')).toHaveAttribute(
    "title",
    /Context: ≈6.1k \/ 8.2k/,
  )
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

// ★ The line under the input box has to stay readable on a narrow screen.
//
// ★ A phone is not a small laptop: the facts wrap, and nothing is squeezed
//   into an ellipsis — a name cut in half says less than a name on the
//   next line.
test.describe("窄屏", () => {
  const PHONE = { width: 390, height: 844 }

  // cut tells whether an element is ellipsized: a truncated element's
  // content is wider than the box it is drawn in.
  async function cut(page: import("@playwright/test").Page, selector: string) {
    return page.locator(selector).evaluate((el) => el.scrollWidth - el.clientWidth)
  }

  test("★ 这一行不横向溢出", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    const meta = page.locator(META)
    await expect(meta).toBeVisible()

    // ★ The row fits the screen: nothing hangs off the edge.
    expect(await cut(page, META)).toBeLessThanOrEqual(1)

    // ★ And the last entry is on the screen, not past it.
    const right = await page
      .locator('[data-testid="operations-button"]')
      .evaluate((el) => el.getBoundingClientRect().right)
    expect(right).toBeLessThanOrEqual(PHONE.width)
  })

  test("★ 名字换行，不被截成半个", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    await expect(page.locator(MODEL)).toHaveText("deepseek/deepseek-v4")
    // ★ The whole name is on the screen: it is not "deepsee…".
    expect(await cut(page, MODEL)).toBeLessThanOrEqual(1)
    expect(await cut(page, REASONING)).toBeLessThanOrEqual(1)
  })

  test("★ 分隔符跟着后一项走，不留在行尾", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    // ★ The separator and the item it introduces are **one unit**: they are
    //   never on two lines, so no line ends with a dangling "·".
    const pairs = await page.evaluate(() => {
      const out: { sep: number; item: number }[] = []
      for (const id of [
        "composer-meta-model",
        "composer-meta-reasoning",
        "composer-meta-context",
      ]) {
        const item = document.querySelector(`[data-testid="${id}"]`)
        const sep = item?.parentElement?.firstElementChild
        if (item && sep) {
          out.push({
            sep: Math.round(sep.getBoundingClientRect().top),
            item: Math.round(item.getBoundingClientRect().top),
          })
        }
      }
      return out
    })
    expect(pairs).toHaveLength(3)
    for (const p of pairs) expect(p.sep).toBe(p.item)
  })

  test("★ 窄屏上入口还在", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    await expect(page.locator('[data-testid="authorization-button"]')).toBeVisible()
    await expect(page.locator('[data-testid="operations-button"]')).toBeVisible()
  })

  // ★ The line under the input box is the **biggest** block on a phone: it
  //   sits under the keyboard, and every pixel of it comes out of the chat.
  test("★ 窄屏上事实只有一行，入口在第二行", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    const tops = await page.evaluate(() => {
      const top = (id: string) => {
        const el = document.querySelector(`[data-testid="${id}"]`)
        return el ? Math.round(el.getBoundingClientRect().top) : null
      }
      return {
        agent: top("composer-meta-agent"),
        model: top("composer-meta-model"),
        context: top("composer-meta-context"),
        entry: top("authorization-button"),
      }
    })
    expect(tops.agent).not.toBeNull()
    // ★ The facts share **one** line: the agent, the model and the context.
    expect(Math.abs(tops.model! - tops.agent!)).toBeLessThanOrEqual(1)
    expect(Math.abs(tops.context! - tops.agent!)).toBeLessThanOrEqual(1)
    // ★ The entries sit on the line below: they are buttons, and they keep
    //   their labels — an icon alone is a puzzle, not a label.
    expect(tops.entry!).toBeGreaterThan(tops.agent!)
  })

  // ★ A fact that steps aside has to land somewhere it can still be read.
  test("★ 窄屏上让位的思考档位进了提示气泡", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    // ★ The reasoning level is not on the narrow line any more — it is in
    //   the model's tooltip, which a phone reads on demand.
    await expect(page.locator(REASONING)).toBeHidden()
    await expect(page.locator(MODEL)).toHaveAttribute("title", /Reasoning: high/)
    // ★ And the context keeps only the share and the bar; the tokens and
    //   the window are in the tooltip.
    await expect(page.locator(CONTEXT)).toContainText("14 %")
    await expect(page.locator(CONTEXT)).toHaveAttribute("title", /Context: ≈18k \/ 131k/)
  })
})

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
    //   ★ At 390px there is room for it — the name gives way only when the
    //     line really runs out (the next test).
    expect(await cut(page, MODEL)).toBeLessThanOrEqual(1)
    expect(await cut(page, REASONING)).toBeLessThanOrEqual(1)
  })

  // ★ The line's priority when it runs out of room.
  //
  // ★ **The context first, then the model name** — and the agent's name is
  //   always there. ⇒ The **model name** is the one that gives way: it
  //   truncates, and the row keeps **one** line.
  //
  // ★ The context occupancy and the agent's name are never cut: they are the
  //   number and the identity, and a row that says "deepsee… · 14 %" still
  //   says what is left of the context.
  test("★ 挤不下时让位的是模型名", async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 844 })
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    // ★ The model gives way.
    expect(await cut(page, MODEL)).toBeGreaterThan(0)
    // ★ The agent's name does not — and neither does the context.
    expect(await cut(page, AGENT)).toBeLessThanOrEqual(1)
    await expect(page.locator(CONTEXT)).toContainText("14 %")
    expect(await cut(page, CONTEXT)).toBeLessThanOrEqual(1)
    // ★ And the line is still **one** line: the model truncated instead of
    //   the row growing a second one.
    const height = await page
      .locator(META)
      .evaluate((el) => el.getBoundingClientRect().height)
    expect(height).toBeLessThanOrEqual(30)
    // ★ The whole name is not lost: it is in the model's tooltip.
    await expect(page.locator(MODEL)).toHaveAttribute("title", /deepseek\/deepseek-v4/)
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

  // ★ On a narrow screen the entries keep **only their icon**.
  //
  // ★ The words are what make that row wide, and the row sits under the
  //   keyboard — every pixel of it comes out of the chat. ★ The icon is the
  //   entry's shape; the word is a label that a phone has no room for.
  test("★ 窄屏上入口只留图标", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    const entries = [
      '[data-testid="authorization-button"]',
      '[data-testid="projects-label"]',
      '[data-testid="operations-button"]',
    ]

    for (const entry of entries) {
      const el = page.locator(entry)
      await expect(el).toBeVisible()
      // ★ `innerText` is the **shown** text: a word that is hidden by the
      //   stylesheet is not in it, and a word that is only in the DOM is not
      //   what the reader sees.
      expect(await el.evaluate((node) => (node as HTMLElement).innerText.trim())).toBe("")
      // ★ And the icon stays — an entry without one is a mark nobody can
      //   read.
      await expect(el.locator("svg").first()).toBeVisible()
    }

    // ★ The fact that stepped aside is not lost: the name the screen reader
    //   reads still carries it.
    await expect(page.locator('[data-testid="authorization-button"]')).toHaveAttribute(
      "aria-label",
      /Authorization: Default approval/,
    )
    await expect(page.locator('[data-testid="projects-label"]')).toHaveAttribute(
      "aria-label",
      /Project: /,
    )
    await expect(page.locator('[data-testid="operations-button"]')).toHaveAttribute(
      "aria-label",
      /Session activity/,
    )
  })

  // ★ The line under the input box is the **biggest** block on a phone: it
  //   sits under the keyboard, and every pixel of it comes out of the chat.
  //   ⇒ The facts **and** the three entries share **one** line.
  test("★ 窄屏上事实与入口挤在一行", async ({ page }) => {
    await page.setViewportSize(PHONE)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    const tops = await page.evaluate(() => {
      const box = (id: string) => {
        const el = document.querySelector(`[data-testid="${id}"]`)
        if (!el) return null
        const r = el.getBoundingClientRect()
        return { top: Math.round(r.top), bottom: Math.round(r.bottom) }
      }
      const meta = document.querySelector('[data-testid="composer-meta"]')
      return {
        // ★ The name is a **button**, and a button's box is taller than the
        //   word inside it. ★ "Share one line" is a claim about the **words**,
        //   so the word is what is measured.
        agent: box("composer-meta-agent-name"),
        model: box("composer-meta-model"),
        context: box("composer-meta-context"),
        entry: box("authorization-button"),
        height: meta instanceof HTMLElement ? Math.round(meta.getBoundingClientRect().height) : null,
      }
    })
    expect(tops.agent).not.toBeNull()
    // ★ The facts share **one** line: the agent, the model and the context.
    expect(Math.abs(tops.model!.top - tops.agent!.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(tops.context!.top - tops.agent!.top)).toBeLessThanOrEqual(1)
    // ★ And the entries share it with them: the icons sit at the **right**
    //   end of the facts' own line — the entry's box overlaps the facts', so
    //   it is not a line of its own below them.
    expect(tops.entry!.top).toBeLessThan(tops.agent!.bottom)
    // ★ The whole block is one row: a second line is a line of chat the
    //   reader does not get.
    expect(tops.height!).toBeLessThanOrEqual(30)
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

// ★ The line under the input box answers to the **chat column**, not to the
//   window.
//
// ★ A tablet in portrait is the case that proves it: the window is 960px
//   wide, but the docked sidebar takes 256 of them, and the column is left
//   with ~670px — a phone's worth of room on a window that looks like a
//   desktop. ⇒ The entries keep only their icons there, while the facts
//   still have room for their words.
test.describe("宽窗窄列", () => {
  // ★ The Surface Pro 10 in portrait: 960 × 1440.
  const TABLET_PORTRAIT = { width: 960, height: 1440 }

  test("★ 窗口宽但列窄时，右边只留图标", async ({ page }) => {
    await page.setViewportSize(TABLET_PORTRAIT)
    await openApp(page)
    await openThread(page, "Multi agent conversation")

    // ★ The column is the one that is narrow — the window is not.
    const column = await page
      .locator('[data-testid="composer-meta"]')
      .evaluate((el) => el.getBoundingClientRect().width)
    expect(column).toBeLessThan(768)
    expect(column).toBeGreaterThan(512)

    // ★ The entries keep only their icons.
    await expect(page.locator('[data-testid="authorization-label"]')).toBeHidden()
    await expect(page.locator('[data-testid="projects-name"]')).toBeHidden()
    // ★ The facts still have room for their words: this column is not a
    //   phone's.
    await expect(page.locator(REASONING)).toBeVisible()
    await expect(page.locator(CONTEXT)).toContainText("Context:")
    // ★ And it is still one line.
    const height = await page
      .locator(META)
      .evaluate((el) => el.getBoundingClientRect().height)
    expect(height).toBeLessThanOrEqual(30)
  })
})

// ★ The name is an **entry**, and it is rendered like the entries beside it.
//
// ★ It used to underline on hover — and an underline says "this is a link to
//   somewhere else". ★ The name does not go anywhere else: it opens the
//   picker that already lives on this line. ⇒ Its hover is **their** hover.
test.describe("名字的渲染", () => {
  async function read(loc: import("@playwright/test").Locator) {
    return loc.evaluate((el) => {
      const cs = getComputedStyle(el)
      return {
        decoration: cs.textDecorationLine,
        radius: cs.borderRadius,
        height: cs.height,
        weight: cs.fontWeight,
        size: cs.fontSize,
        background: cs.backgroundColor,
        color: cs.color,
      }
    })
  }

  test("★ 名字和右侧的入口同一套渲染，hover 不是下划线", async ({ page }) => {
    await openApp(page)

    const name = page.locator(AGENT)
    const entry = page.locator('[data-testid="projects-button"]')
    await expect(name).toBeVisible()
    await expect(entry).toBeVisible()

    const rest = await read(name)
    const restEntry = await read(entry)

    // ★ One kind of thing: the same box, the same type, the same weight.
    expect(rest.decoration).toBe("none")
    expect(rest.radius).toBe(restEntry.radius)
    expect(rest.height).toBe(restEntry.height)
    expect(rest.weight).toBe(restEntry.weight)
    expect(rest.size).toBe(restEntry.size)

    // ★ The hover is **theirs**: a tint and a brighter word — and still no
    //   underline.
    await name.hover()
    const hovered = await read(name)
    expect(hovered.decoration).toBe("none")
    expect(hovered.background).not.toBe(rest.background)
    expect(hovered.color).not.toBe(rest.color)

    await entry.hover()
    const hoveredEntry = await read(entry)
    expect(hovered.background).toBe(hoveredEntry.background)
    expect(hovered.color).toBe(hoveredEntry.color)
  })
})

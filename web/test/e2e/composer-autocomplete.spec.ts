import { expect, test } from "@playwright/test"

import {
  COMPOSER,
  COMPOSER_OPTION,
  COMPOSER_POPUP,
  MARKER_A,
  NEW_THREAD,
  openApp,
  openThread,
  resetBackend,
  trackRequests,
} from "./fixtures"

test.beforeEach(async () => {
  await resetBackend()
})

test("输入框到 10 行封顶，超过才滚动", async ({ page }) => {
  await openApp(page)
  const box = page.locator(COMPOSER)

  const nine = Array.from({ length: 9 }, (_, i) => `line ${i}`).join("\n")
  await box.fill(nine)
  await expect(box).toHaveValue(nine)
  const nineMetrics = await box.evaluate((el) => {
    const cs = getComputedStyle(el)
    return {
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
      overflowY: cs.overflowY,
    }
  })
  expect(nineMetrics.scrollHeight).toBe(nineMetrics.clientHeight)
  expect(nineMetrics.overflowY).toBe("hidden")

  const eleven = Array.from({ length: 11 }, (_, i) => `line ${i}`).join("\n")
  await box.fill(eleven)
  await expect(box).toHaveValue(eleven)
  const elevenMetrics = await box.evaluate((el) => {
    const cs = getComputedStyle(el)
    return {
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
      overflowY: cs.overflowY,
      lineHeight: parseFloat(cs.lineHeight),
      padding: (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0),
    }
  })
  expect(elevenMetrics.scrollHeight).toBeGreaterThan(elevenMetrics.clientHeight)
  expect(elevenMetrics.overflowY).toBe("auto")
  expect(elevenMetrics.clientHeight).toBeLessThanOrEqual(
    Math.ceil(elevenMetrics.lineHeight * 10 + elevenMetrics.padding),
  )
})

test("输入框出现时自动聚焦", async ({ page }) => {
  await openApp(page)
  const box = page.locator(COMPOSER)
  await expect(box).toBeFocused()

  await box.fill("focus check")
  await page.keyboard.press("Enter")
  await expect(box).toHaveValue("")
  await expect(box).toBeFocused()
})

test("@ 选择文件只插入路径，不发送", async ({ page }) => {
  const requests = trackRequests(page)
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("@comp")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)
  await expect(page.locator(COMPOSER_OPTION).first()).toContainText("composer.tsx")

  await page.keyboard.press("Enter")
  await expect(box).toHaveValue("@web/src/components/composer.tsx ")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
  expect(requests).not.toContain("/v2/run")
})

test("@ 不把邮箱当成提示", async ({ page }) => {
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("a@b")
  await expect(box).toHaveValue("a@b")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
})

test("/ 选择命令后带参数发送", async ({ page }) => {
  const requests = trackRequests(page)
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/re")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()

  const review = page.locator(COMPOSER_OPTION).filter({ hasText: "/review" })
  await expect(review).toHaveCount(1)
  await review.click()
  await expect(box).toHaveValue("/review ")

  await box.evaluate((el) => {
    const ta = el as HTMLTextAreaElement
    ta.setSelectionRange(ta.value.length, ta.value.length)
  })
  await page.keyboard.type("docs/frontend-design.md")
  await expect(box).toHaveValue("/review docs/frontend-design.md")

  await page.keyboard.press("Enter")
  await expect(page.getByText("/review docs/frontend-design.md").first()).toBeVisible()
  expect(requests).toContain("/v2/run")
})

test("/new 执行界面动作，不发送消息", async ({ page }) => {
  const requests = trackRequests(page)
  await openApp(page)
  await page.locator(NEW_THREAD).click()
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/new")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)

  await page.keyboard.press("Enter")
  await expect(box).toHaveValue("")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
  expect(requests).not.toContain("/v2/run")
})

test("/new 切回欢迎页后光标回到输入框", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/new")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)
  await page.keyboard.press("Enter")

  await expect(box).toHaveValue("")
  await expect(box).toBeFocused()
  await expect
    .poll(() => box.evaluate((el) => (el as HTMLTextAreaElement).selectionStart))
    .toBe(0)
})

test("/settings 关闭后光标回到输入框", async ({ page }) => {
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/settings")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)
  await page.keyboard.press("Enter")

  const dialog = page.locator('[data-testid="settings-dialog"]')
  await expect(dialog).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog).toHaveCount(0)

  await expect(box).toBeFocused()
  await expect
    .poll(() => box.evaluate((el) => (el as HTMLTextAreaElement).selectionStart))
    .toBe(0)
})

test("Escape 关闭提示但保留已输入内容", async ({ page }) => {
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("@")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()

  await page.keyboard.press("Escape")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
  await expect(box).toHaveValue("@")
})

test("键盘移动超出可视区时列表自动跟随", async ({ page }) => {
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/")
  const options = page.locator(COMPOSER_OPTION)
  // Every command but /stop, which only makes sense while a run is live:
  // new, attach, settings, agents, mcps, compact, skill, review, test,
  // explain, fix — eleven. ★ This count is a **fixed expectation**: adding a
  // command means adding one here, which is the point (a command that
  // silently stops being offered would otherwise go unnoticed).
  await expect(options).toHaveCount(11)

  for (let i = 0; i < 8; i++) await page.keyboard.press("ArrowDown")
  await expect(options.nth(8)).toHaveAttribute("aria-selected", "true")

  const popup = page.locator(COMPOSER_POPUP)
  await expect
    .poll(async () => {
      const popupBox = await popup.boundingBox()
      const selectedBox = await options.nth(8).boundingBox()
      if (!popupBox || !selectedBox) return false
      return (
        selectedBox.y >= popupBox.y - 1 &&
        selectedBox.y + selectedBox.height <= popupBox.y + popupBox.height + 1
      )
    })
    .toBe(true)
  expect(await popup.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
})

test("Tab 只补全提示词命令，不自动发送", async ({ page }) => {
  const requests = trackRequests(page)
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/review")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)

  await page.keyboard.press("Tab")
  await expect(box).toHaveValue("/review ")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
  expect(requests).not.toContain("/v2/run")
})

test("Tab 只补全动作命令，Enter 才执行", async ({ page }) => {
  const requests = trackRequests(page)
  await openApp(page)
  await openThread(page, "Alpha conversation")
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/new")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)

  await page.keyboard.press("Tab")
  await expect(box).toHaveValue("/new ")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
  await expect(page.getByText(MARKER_A).first()).toBeVisible()

  await page.keyboard.press("Enter")
  await expect(box).toHaveValue("")
  await expect(page.getByText(MARKER_A)).toHaveCount(0)
  expect(requests).not.toContain("/v2/run")
})

test("Enter 执行提示词命令", async ({ page }) => {
  const requests = trackRequests(page)
  await openApp(page)
  const box = page.locator(COMPOSER)

  await box.click()
  await page.keyboard.type("/review")
  await expect(page.locator(COMPOSER_POPUP)).toBeVisible()
  await expect(page.locator(COMPOSER_OPTION)).toHaveCount(1)

  await page.keyboard.press("Enter")
  await expect(box).toHaveValue("")
  await expect(page.locator(COMPOSER_POPUP)).toHaveCount(0)
  await expect(page.getByText("/review", { exact: true }).first()).toBeVisible()
  expect(requests).toContain("/v2/run")
})

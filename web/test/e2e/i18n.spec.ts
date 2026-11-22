import { expect, test } from "@playwright/test"

import { openApp } from "./fixtures"

// ★ The interface follows the **browser's** language, and the browser is
//   pinned to English in `playwright.config.ts` — so the suite starts in
//   English and the specs that assert the English copy keep passing.
test.describe("界面的语言", () => {
  test("★ 切一次语言：偏好记住了，lang 跟着变", async ({ page }) => {
    await openApp(page)

    const toggle = page.locator('[data-testid="locale-toggle"]')
    await expect(toggle).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.lang)).toBe("en")

    await toggle.click()
    expect(await page.evaluate(() => localStorage.getItem("openheron.locale"))).toBe("zh")
    // ★ `lang` is how a screen reader learns to pronounce what it reads.
    expect(await page.evaluate(() => document.documentElement.lang)).toBe("zh-CN")

    // ★ And the choice survives a reload: it is a preference of this machine.
    await page.reload()
    expect(await page.evaluate(() => document.documentElement.lang)).toBe("zh-CN")
  })

  test("★ 切到中文：界面文案真的变成中文", async ({ page }) => {
    await openApp(page)
    const toggle = page.locator('[data-testid="locale-toggle"]')
    await toggle.click()

    // ★ The switch button translates itself too: it now names the language
    //   it will switch **back** to, in that language's own words.
    await expect(toggle).toHaveAttribute("aria-label", "切换到英文")

    // ★ The Chinese catalog is written: the hero copy is Chinese now, not
    //   the English fallback the phase-0 mechanism used to show.
    await expect(page.getByText("关于这个工作区，尽管提问。")).toBeVisible()
  })

  test("★ 切回英文：界面回到英文", async ({ page }) => {
    await openApp(page)
    const toggle = page.locator('[data-testid="locale-toggle"]')

    await toggle.click()
    await expect(toggle).toHaveAttribute("aria-label", "切换到英文")

    await toggle.click()
    expect(await page.evaluate(() => localStorage.getItem("openheron.locale"))).toBe("en")
    await expect(toggle).toHaveAttribute("aria-label", "Switch to Chinese")
    await expect(page.getByText("Ask anything about this workspace.")).toBeVisible()
  })

  test("★ 语言开关在页头的右端，和主题开关并排", async ({ page }) => {
    await openApp(page)

    const locale = page.locator('[data-testid="locale-toggle"]')
    const theme = page.locator('[data-testid="theme-toggle"]')
    const header = page.locator("header")

    const [localeBox, themeBox, headerBox] = await Promise.all([
      locale.boundingBox(),
      theme.boundingBox(),
      header.boundingBox(),
    ])
    expect(localeBox).not.toBeNull()
    expect(themeBox).not.toBeNull()
    expect(headerBox).not.toBeNull()

    // ★ The theme switch is the last thing in the row: its right edge is the
    //   header's right edge, minus the row's own padding.
    const themeRight = themeBox!.x + themeBox!.width
    const headerRight = headerBox!.x + headerBox!.width
    expect(headerRight - themeRight).toBeLessThan(24)

    // ★ And the language switch stands right next to it — the two are a
    //   **preference of this machine**, and they belong in one place at the
    //   end of the row. ★ It used to stand between the agent filter and the
    //   theme switch, stranded in the middle of the title.
    const localeRight = localeBox!.x + localeBox!.width
    expect(localeRight).toBeLessThanOrEqual(themeBox!.x + 1)
    expect(themeBox!.x - localeRight).toBeLessThan(24)
  })
})

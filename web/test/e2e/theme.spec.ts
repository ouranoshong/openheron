import { expect, test } from "@playwright/test"

import { openApp } from "./fixtures"

// ★ The dark palette was in the stylesheet all along, and nothing ever
//   turned it on. These tests are about the **switch**, not about the
//   colors: a second palette that no user can reach is a second palette
//   nobody has to keep true.

test.describe("主题", () => {
  // ★ The default is decided by the media query, so the test has to say
  //   which way it points — otherwise the suite depends on the machine it
  //   runs on.
  test.use({ colorScheme: "light" })

  const isDark = (page: import("@playwright/test").Page) =>
    page.evaluate(() => document.documentElement.classList.contains("dark"))

  test("★ 开关把暗色打开，刷新后还记得", async ({ page }) => {
    await openApp(page)
    expect(await isDark(page), "the light theme is the default under a light desktop").toBe(false)

    await page.locator('[data-testid="theme-toggle"]').click()
    expect(await isDark(page), "the toggle should turn the dark theme on").toBe(true)
    expect(await page.evaluate(() => localStorage.getItem("openheron.theme"))).toBe("dark")

    await page.reload()
    expect(await isDark(page), "the choice has to survive a reload").toBe(true)
  })

  test("★ 切回浅色也记得", async ({ page }) => {
    await openApp(page)
    await page.locator('[data-testid="theme-toggle"]').click()
    await page.locator('[data-testid="theme-toggle"]').click()

    expect(await isDark(page)).toBe(false)
    expect(await page.evaluate(() => localStorage.getItem("openheron.theme"))).toBe("light")
    await page.reload()
    expect(await isDark(page)).toBe(false)
  })

  test("★ 没有存过偏好时跟随系统", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" })
    await page.goto("/")

    expect(await isDark(page), "a dark desktop should get the dark theme").toBe(true)
  })

  test("★ 首屏之前就定了主题", async ({ page }) => {
    // ★ The preference is set before any page script runs, and the decision
    //   is made by the script in index.html — so the page never paints
    //   light and then turns dark.
    await page.addInitScript(() => {
      localStorage.setItem("openheron.theme", "dark")
    })
    await page.goto("/")

    expect(await isDark(page)).toBe(true)
    await expect(page.locator('[data-testid="theme-toggle"]')).toBeVisible()
  })

  test("★ 存过的偏好压过系统", async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem("openheron.theme", "light")
    })
    await page.emulateMedia({ colorScheme: "dark" })
    await page.goto("/")

    expect(await isDark(page), "an explicit choice wins over the desktop").toBe(false)
  })
})

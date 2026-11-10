import { expect, test, type Page } from "@playwright/test"

import { openApp } from "./fixtures"

// ★ Motion is hard to assert: the process is timing-sensitive, and a test
//   that waits for an animation to finish proves nothing about the state the
//   user is left in. So these tests pin the **end state** — the mark is on
//   screen, its parts are opaque, and the reduced-motion rule really does
//   collapse the animation.

/** The welcome screen — the one place that shows the hero. */
async function openWelcome(page: Page): Promise<void> {
  await page.addInitScript(() => {
    localStorage.setItem("openheron.activeThread", "99999999-9999-4999-8999-999999999999")
  })
  await openApp(page)
  await expect(page.getByText("Ask anything about this workspace.").first()).toBeVisible()
}

test.describe("动效", () => {
  test("★ 欢迎页的品牌标记是内联 SVG，在屏幕上", async ({ page }) => {
    await openWelcome(page)

    const mark = page.getByTestId("brand-mark")
    await expect(mark).toBeVisible()

    // ★ Inline SVG, not an image: an SVG loaded through an img element is a
    //   separate document, and the page's stylesheet cannot reach its parts.
    expect(await mark.evaluate((el) => el.tagName.toLowerCase())).toBe("svg")

    const box = await mark.boundingBox()
    expect(box?.width).toBe(64)
  })

  test("★ 入场之后部件是不透明的 —— 动效没有把元素留在起始态", async ({ page }) => {
    await openWelcome(page)

    // ★ `mark-parts` is a hand-written class in public/favicon.svg — not a
    //   Tailwind utility, so it is as stable as a test id.
    const parts = page.getByTestId("brand-mark").locator(".mark-parts")
    await expect(parts.first()).toHaveCSS("opacity", "1")
  })

  test("★ 页头那一份不播", async ({ page }) => {
    await openWelcome(page)

    // 20px is not a place to read a drawing, and the header never leaves.
    await expect(page.locator(".brand-mark--play")).toHaveCount(1)
  })
})

test.describe("动效（用户要求减少动态效果）", () => {
  test.use({ reducedMotion: "reduce" })

  test("★ 动画被压到 0.01ms，标记立刻就位", async ({ page }) => {
    await openWelcome(page)

    const parts = page.getByTestId("brand-mark").locator(".mark-parts")
    // ★ No polling here: the point is that the element is already in its final
    //   state, not that it gets there.
    const opacity = await parts.first().evaluate((el) => getComputedStyle(el).opacity)
    expect(opacity).toBe("1")

    const duration = await parts
      .first()
      .evaluate((el) => getComputedStyle(el).animationDuration)
    expect(parseFloat(duration)).toBeLessThan(0.01)
  })
})

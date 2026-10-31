import { expect, test } from "@playwright/test"

import { MARKER_A, THREAD_ITEM, openApp, openThread, resetBackend, sendMessage, setScenario } from "./fixtures"

const LIST = '[data-testid="message-list"]'

async function distanceToBottom(page: import("@playwright/test").Page): Promise<number> {
  return page.locator(LIST).evaluate((el) => {
    const n = el as HTMLElement
    return n.scrollHeight - n.scrollTop - n.clientHeight
  })
}

async function overflows(page: import("@playwright/test").Page): Promise<boolean> {
  return page.locator(LIST).evaluate((el) => (el as HTMLElement).scrollHeight > (el as HTMLElement).clientHeight + 20)
}

test.beforeEach(async () => {
  await resetBackend()
})

test.describe("新增内容时的滚动跟随", () => {
  test("★ 内容溢出时，长回复的末尾停在视口里", async ({ page }) => {
    await setScenario("longStream")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} tell me a long story`)
    await page.waitForTimeout(300)
    expect(await overflows(page), "the scenario produced no overflowing content, so the scroll behavior cannot be measured").toBe(true)

    await expect
      .poll(() => distanceToBottom(page), { timeout: 8000, message: "should stay at the bottom after the reply ends" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 用户往上翻之后，不该被拽回底部", async ({ page }) => {
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} tell me a long story`)
    await expect(page.getByText(/Working/).first()).toHaveCount(0, { timeout: 15000 })
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)
    await page.waitForTimeout(600)

    const list = page.locator(LIST)
    await list.evaluate((el) => {
      ;(el as HTMLElement).scrollTop = 0
    })
    await page.waitForTimeout(400)

    const away = await distanceToBottom(page)
    expect(away, "the content is not tall enough to measure scrolling up").toBeGreaterThan(400)

    await sendMessage(page, `${MARKER_A} and another one`)
    await page.waitForTimeout(1200)

    const top = await list.evaluate((el) => (el as HTMLElement).scrollTop)
    expect(top, "the user scrolled up but the view was dragged back to the bottom").toBeLessThanOrEqual(40)
  })

  test("★ 流式过程中（未结束时）也在跟随", async ({ page }) => {
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} long one`)
    await expect.poll(() => distanceToBottom(page), { timeout: 8000 }).toBeLessThanOrEqual(120)

    await page.locator(LIST).evaluate((el) => {
      ;(el as HTMLElement).scrollTop = 0
    })
    await page.waitForTimeout(500)
    const top = await page.locator(LIST).evaluate((el) => (el as HTMLElement).scrollTop)
    expect(top).toBeLessThanOrEqual(40)
  })
})

test.describe("markdown 渲染后的滚动", () => {
  test("★ 代码块高亮把内容撑高之后，视图仍停在底部", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} show code`)
    await expect(page.locator('[data-testid="code-block"] span[style*="--shiki-light"]').first()).toBeVisible({
      timeout: 8000,
    })

    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000 })
      .toBeLessThanOrEqual(80)
  })
})

test.describe("大块增量下的跟随", () => {
  test("★ 单次渲染涨好几行时也要跟着走", async ({ page }) => {
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} long one in big chunks`)
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)

    await expect
      .poll(() => distanceToBottom(page), { timeout: 8000, message: "should still follow down to the bottom with large chunks" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 浏览器自己挪了滚动位置之后，仍然继续跟随", async ({ page }) => {
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} long one`)
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)
    await expect.poll(() => distanceToBottom(page), { timeout: 8000 }).toBeLessThanOrEqual(80)

    // ★ A content change and a scroll position change in the same batch is
    //   what the browser itself does when a tall block lands (a clamp, a
    //   scroll anchoring adjustment). The user did not scroll, so the view
    //   must keep following — treating that event as "the user left the
    //   bottom" is what left the list stuck above the bottom.
    await page.locator(LIST).evaluate((el) => {
      const n = el as HTMLElement
      const block = document.createElement("div")
      block.style.height = "200px"
      block.textContent = "block"
      n.firstElementChild!.prepend(block)
      n.scrollTop += 40
      n.dispatchEvent(new Event("scroll"))
    })
    await page.waitForTimeout(200)

    await page.locator(LIST).evaluate((el) => {
      const n = el as HTMLElement
      const block = document.createElement("div")
      block.style.height = "400px"
      block.textContent = "more"
      n.firstElementChild!.appendChild(block)
    })

    await expect
      .poll(() => distanceToBottom(page), { timeout: 3000, message: "the view should keep following after a browser-induced scroll" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 掉队之后滚回底部，跟随重新接上", async ({ page }) => {
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} long one`)
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)
    await expect.poll(() => distanceToBottom(page), { timeout: 8000 }).toBeLessThanOrEqual(80)

    await page.locator(LIST).evaluate((el) => {
      ;(el as HTMLElement).scrollTop = 0
    })
    await page.waitForTimeout(200)
    expect(await distanceToBottom(page)).toBeGreaterThan(400)

    const append = () =>
      page.locator(LIST).evaluate((el) => {
        const block = document.createElement("div")
        block.style.height = "300px"
        block.textContent = "block"
        el.firstElementChild!.appendChild(block)
      })

    // Content grows while the user is away: the view must stay put.
    await append()
    await page.waitForTimeout(200)
    expect(await distanceToBottom(page), "the view was dragged back while the user was reading").toBeGreaterThan(400)

    // The user scrolls back down: the follow has to come back with them.
    await page.locator(LIST).evaluate((el) => {
      ;(el as HTMLElement).scrollTop = (el as HTMLElement).scrollHeight
    })
    await page.waitForTimeout(200)
    await append()

    await expect
      .poll(() => distanceToBottom(page), { timeout: 3000, message: "after the user catches up, the follow should re-attach" })
      .toBeLessThanOrEqual(80)
  })
})

test.describe("打开长历史会话时的初始位置", () => {
  test("★ 打开应用停在最后一条，而不是历史开头", async ({ page }) => {
    await page.goto("/")
    await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
    await openThread(page, "Long conversation")

    await expect.poll(() => overflows(page), { timeout: 5000, message: "the history is too short to measure" }).toBe(true)
    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000, message: "opening a long thread should land on the last message" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 切到另一个长会话也要停在底部", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Long conversation")
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)

    await page.locator(LIST).evaluate((el) => {
      ;(el as HTMLElement).scrollTop = 0
    })
    await page.waitForTimeout(300)

    await openThread(page, "Alpha conversation")
    await page.waitForTimeout(300)
    await openThread(page, "Long conversation")

    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000, message: "switching back to a long thread should stay at the bottom" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 刷新后停在最后一条", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Long conversation")
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)

    await page.reload()
    await expect(page.locator(THREAD_ITEM).first()).toBeVisible()

    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000, message: "after a reload it should stay on the last message" })
      .toBeLessThanOrEqual(80)
  })})
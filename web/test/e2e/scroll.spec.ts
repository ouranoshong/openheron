import { expect, test } from "@playwright/test"

import { MARKER_A, THREAD_ITEM, openApp, openThread, resetBackend, sendMessage, setScenario } from "./fixtures"

/**
 * 滚动行为。
 *
 * ★ 这些断言盯的是「视口位置」，不是「内容对不对」。内容对但滚不过去，
 *   用户看到的就是「回复在屏幕下面，看不到」—— 而只查 DOM 的测试
 *   完全看不见这一类问题。
 */

const LIST = '[data-testid="message-list"]'

/** 距底部还有多远（px）。 */
async function distanceToBottom(page: import("@playwright/test").Page): Promise<number> {
  return page.locator(LIST).evaluate((el) => {
    const n = el as HTMLElement
    return n.scrollHeight - n.scrollTop - n.clientHeight
  })
}

/** 内容是否溢出视口（不溢出的话「跟到底部」无从谈起）。 */
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

    // 先确认内容确实会溢出 —— 否则这条断言没有意义
    await sendMessage(page, `${MARKER_A} tell me a long story`)
    await page.waitForTimeout(300)
    expect(await overflows(page), "场景没造出溢出内容，测不出滚动问题").toBe(true)

    await expect
      .poll(() => distanceToBottom(page), { timeout: 8000, message: "回复结束后应当停在底部" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 用户往上翻之后，不该被拽回底部", async ({ page }) => {
    // 用 bigChunks 而不是 longStream：这条测的是「用户上翻的意图」，
    // 不需要 1 字符/事件的细粒度；而 2400 个事件跑完要好几秒，
    // 慢到足以让后面的断言超时 —— 那是场景的问题，不是被测行为。
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    // 先等这一轮完全跑完 —— 半途滚动会测到「流还在涨」这种别的情况
    await sendMessage(page, `${MARKER_A} tell me a long story`)
    await expect(page.getByText(/Working/).first()).toHaveCount(0, { timeout: 15000 })
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)
    await page.waitForTimeout(600)

    const list = page.locator(LIST)
    // 往上滚到顶部，模拟「我要看前面那段」
    await list.evaluate((el) => {
      ;(el as HTMLElement).scrollTop = 0
    })
    await page.waitForTimeout(400)

    // ★ 前置断言：确认「滚到顶」与「在底部」在这个场景里确实可区分。
    //   第一版没写这条，于是内容只比视口高 70px 时，一次上滑被当成
    //   「还在底部」，测试红得莫名其妙 —— 红的其实是场景，不是实现。
    const away = await distanceToBottom(page)
    expect(away, "内容不够高，测不出上翻").toBeGreaterThan(400)

    // 再来一条消息，视图不该动
    await sendMessage(page, `${MARKER_A} and another one`)
    await page.waitForTimeout(1200)

    const top = await list.evaluate((el) => (el as HTMLElement).scrollTop)
    expect(top, "用户已经往上翻了，视图却被拽回底部").toBeLessThanOrEqual(40)
  })

  test("★ 流式过程中（未结束时）也在跟随", async ({ page }) => {
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    // 已有的历史 + 新回复，过程中就该跟着
    await sendMessage(page, `${MARKER_A} long one`)
    await expect.poll(() => distanceToBottom(page), { timeout: 8000 }).toBeLessThanOrEqual(120)

    // 中途往上翻，然后确认不再被拉回
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
    // 高亮是异步的（懒加载引擎 + 250ms 静默期），撑高发生在渲染之后。
    // 只在「内容到达时」滚一次是不够的。
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
    // 这条针对一个具体的 bug：跟随状态在每次渲染后被「重新测量」，
    // 而测量发生在内容长高之后、滚动之前 —— 于是单次增长超过阈值
    // （80px）就被判定成「用户已经离开底部」，跟随就此停住。
    //
    // longStream 每次只加一个字符，测不出它；真实模型的 delta 常是整句。
    await setScenario("bigChunks")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} long one in big chunks`)
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)

    await expect
      .poll(() => distanceToBottom(page), { timeout: 8000, message: "大块增量下应当仍然跟到底部" })
      .toBeLessThanOrEqual(80)
  })
})

test.describe("打开长历史会话时的初始位置", () => {
  test("★ 打开应用停在最后一条，而不是历史开头", async ({ page }) => {
    // 这条针对的机制：消息是**异步加载**的 —— 首屏 0 条，加载完一次撑满。
    // 而「是否跟随」的判断在每次渲染后重新测量，测量发生在内容长高之后、
    // 滚动之前，于是「加载完」被误判成「用户已经翻到上面去了」。
    // 结果：打开一个有历史的会话，看到的是最开头那几条对话。
    await page.goto("/")
    await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
    await openThread(page, "Long conversation")

    await expect.poll(() => overflows(page), { timeout: 5000, message: "历史太短，测不出" }).toBe(true)
    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000, message: "打开长会话应停在最后一条" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 切到另一个长会话也要停在底部", async ({ page }) => {
    // 滚动容器是同一个 DOM 节点，scrollTop 会跟着上一次留下来。
    await openApp(page)
    await openThread(page, "Long conversation")
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)

    // 先把视图滚上去，制造「脏」的 scrollTop
    await page.locator(LIST).evaluate((el) => {
      ;(el as HTMLElement).scrollTop = 0
    })
    await page.waitForTimeout(300)

    // 切走再切回
    await openThread(page, "Alpha conversation")
    await page.waitForTimeout(300)
    await openThread(page, "Long conversation")

    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000, message: "切回长会话应停在底部" })
      .toBeLessThanOrEqual(80)
  })

  test("★ 刷新后停在最后一条", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Long conversation")
    await expect.poll(() => overflows(page), { timeout: 5000 }).toBe(true)

    await page.reload()
    await expect(page.locator(THREAD_ITEM).first()).toBeVisible()

    await expect
      .poll(() => distanceToBottom(page), { timeout: 5000, message: "刷新后应停在最后一条" })
      .toBeLessThanOrEqual(80)
  })})
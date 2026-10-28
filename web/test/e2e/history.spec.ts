import { expect, test } from "@playwright/test"

import { openApp, openThread, resetBackend } from "./fixtures"

const LIST = '[data-testid="message-list"]'
const HISTORY = "History conversation"
const MARKER_HISTORY = "MARKER-HISTORY"

test.beforeEach(async () => {
  await resetBackend()
})

test("同一轮只显示一次角色标签，用户换行按字面保留", async ({ page }) => {
  await openApp(page)
  await openThread(page, HISTORY)

  await expect(page.locator('[data-testid="message"][data-role="assistant"]')).toHaveCount(2)
  await expect(page.locator('[data-testid="role-label"]')).toHaveCount(1)

  const userText = page.locator('[data-testid="message"][data-role="user"] [data-testid="user-text"]')
  await expect(userText).toBeVisible()
  expect(await userText.innerText()).toContain(`${MARKER_HISTORY} first line\nsecond line`)
})

test("历史工具卡默认折叠，展开后显示完整结果", async ({ page }) => {
  await openApp(page)
  await openThread(page, HISTORY)

  const card = page.locator('[data-testid="tool-card"]').first()
  await expect(card).toHaveAttribute("data-open", "false")
  await expect(card.locator('[data-testid="tool-result"]')).toHaveCount(0)

  await card.getByRole("button").click()
  await expect(card).toHaveAttribute("data-open", "true")
  await expect(card.locator('[data-testid="tool-result"]')).toContainText("tool output line 80")
})

test("历史加载时显示骨架，不闪空白", async ({ page }) => {
  let release: () => void = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/v2/sessions/*/messages", async (route) => {
    await gate
    await route.continue()
  })

  await openApp(page)
  await openThread(page, HISTORY)

  const skeleton = page.locator('[data-testid="message-skeleton"]')
  await expect(skeleton).toBeVisible()

  release()
  await expect(page.locator('[data-testid="message"]')).toHaveCount(3)
  await expect(skeleton).toHaveCount(0)
})

test("上翻历史后可以一键回到底部", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Long conversation")

  const list = page.locator(LIST)
  await expect
    .poll(() =>
      list.evaluate((el) => {
        const n = el as HTMLElement
        return n.scrollHeight - n.scrollTop - n.clientHeight
      }),
    )
    .toBeLessThanOrEqual(80)

  await list.evaluate((el) => {
    const n = el as HTMLElement
    n.scrollTop = 0
    n.dispatchEvent(new Event("scroll"))
  })

  const button = page.locator('[data-testid="jump-to-latest"]')
  await expect(button).toBeVisible()
  await button.click()
  await expect(button).toHaveCount(0)
  await expect
    .poll(() =>
      list.evaluate((el) => {
        const n = el as HTMLElement
        return n.scrollHeight - n.scrollTop - n.clientHeight
      }),
    )
    .toBeLessThanOrEqual(80)
})

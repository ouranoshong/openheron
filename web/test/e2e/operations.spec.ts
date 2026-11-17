import { expect, test } from "@playwright/test"

import {
  OPERATIONS_BUTTON,
  OPERATIONS_DIALOG,
  OPERATIONS_EMPTY,
  OPERATIONS_ITEM,
  openApp,
  openThread,
  resetBackend,
} from "./fixtures"

test.describe("会话的操作时间线", () => {
  test.beforeEach(async () => {
    await resetBackend()
  })

  test("打开面板能看到条目", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator(OPERATIONS_BUTTON).click()
    await expect(page.locator(OPERATIONS_DIALOG)).toBeVisible()

    // ★ The log is the server's record of the session, and it comes back as
    //   it was written.
    await expect(page.locator(OPERATIONS_ITEM)).toHaveCount(5)

    // ★ The lines are read from the top down: the session's own beginning
    //   is first, and the turn's end is last.
    await expect(page.locator(OPERATIONS_ITEM).first()).toContainText("Session created")
    await expect(page.locator(OPERATIONS_ITEM).last()).toContainText("Turn finished")
  })

  test("每条都说清楚是什么事", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator(OPERATIONS_BUTTON).click()
    await expect(page.locator(OPERATIONS_DIALOG)).toBeVisible()

    // ★ The kinds are the server's vocabulary, and the sentences are the
    //   client's — an approval that was asked for and an approval that was
    //   answered are two different lines.
    await expect(page.locator(OPERATIONS_ITEM).nth(2)).toContainText("Approval requested")
    await expect(page.locator(OPERATIONS_ITEM).nth(3)).toContainText("Approval approved")
  })

  test("没有活动时显示空态", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Empty conversation")

    await page.locator(OPERATIONS_BUTTON).click()
    await expect(page.locator(OPERATIONS_DIALOG)).toBeVisible()

    // ★ An empty log is a fact, not a failure: the session has no
    //   operations, and the panel says so.
    await expect(page.locator(OPERATIONS_EMPTY)).toBeVisible()
    await expect(page.locator(OPERATIONS_ITEM)).toHaveCount(0)
  })

  test("★ Escape 能关掉面板", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator(OPERATIONS_BUTTON).click()
    await expect(page.locator(OPERATIONS_DIALOG)).toBeVisible()

    await page.keyboard.press("Escape")
    await expect(page.locator(OPERATIONS_DIALOG)).toHaveCount(0)
  })
})

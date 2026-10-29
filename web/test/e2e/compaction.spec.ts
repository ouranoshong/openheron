import { expect, test } from "@playwright/test"

import { openApp, openThread, resetBackend, sendMessage, setScenario, typeAndEnter } from "./fixtures"

const DIVIDER = '[data-testid="compaction-divider"]'
const NOTICE = '[data-testid="composer-meta-notice"]'
const COMPACTED = "Compacted conversation"
const ALPHA = "Alpha conversation"
const SUMMARY = "SUMMARY-OF-THE-CONVERSATION"

test.beforeEach(async () => {
  await resetBackend()
})

// The compacted message is a boundary, not a chat bubble.
test("压缩消息渲染成分隔线，不是一个气泡", async ({ page }) => {
  await openApp(page)
  await openThread(page, COMPACTED)

  const divider = page.locator(DIVIDER)
  await expect(divider).toHaveCount(1)
  await expect(divider).toContainText("12 earlier messages")
  await expect(divider).toContainText("62k tokens saved")

  // The summary is the model's business; the user gets the boundary.
  await expect(page.getByText(SUMMARY)).toHaveCount(0)
})

// Compaction is not deletion: what was said before the cut is still there.
test("被取代的消息仍然读得到", async ({ page }) => {
  await openApp(page)
  await openThread(page, COMPACTED)

  await expect(page.locator('[data-testid="message"]')).toHaveCount(3)
  await expect(page.getByText("MARKER-COMPACT first answer")).toBeVisible()
  await expect(page.getByText("MARKER-COMPACT answer after the compaction")).toBeVisible()
})

// The compaction happens mid-turn: the user is watching, so the boundary
// goes in at once — without the numbers, which are measured when the turn
// ends.
test("轮内发生的压缩当场画出边界", async ({ page }) => {
  await setScenario("compaction")
  await openApp(page)
  await sendMessage(page, "please compact")

  const divider = page.locator(DIVIDER)
  await expect(divider).toHaveCount(1)
  await expect(divider).toContainText("compacted into a summary")
  await expect(divider).not.toContainText("tokens saved")
})

// /compact compacts on demand — no threshold involved.
test("/compact 手动压缩后画出边界", async ({ page }) => {
  await openApp(page)
  await openThread(page, ALPHA)
  await typeAndEnter(page, "/compact")

  const divider = page.locator(DIVIDER)
  await expect(divider).toHaveCount(1)
  await expect(divider).toContainText("compacted into a summary")
  await expect(divider).toContainText("tokens saved")
})

// The compaction takes a model call: the composer says so while it runs.
test("压缩期间 composer 给出提示", async ({ page }) => {
  await openApp(page)
  await openThread(page, ALPHA)
  await typeAndEnter(page, "/compact")

  await expect(page.locator(NOTICE)).toBeVisible()
  await expect(page.locator(NOTICE)).toContainText("Compacting")

  // And the notice goes away when the work is done.
  await expect(page.locator(DIVIDER)).toHaveCount(1)
  await expect(page.locator(NOTICE)).toHaveCount(0)
})

import { expect, test } from "@playwright/test"

import { openApp, openThread, resetBackend, sendMessage, setScenario } from "./fixtures"

const DIVIDER = '[data-testid="compaction-divider"]'
const COMPACTED = "Compacted conversation"
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

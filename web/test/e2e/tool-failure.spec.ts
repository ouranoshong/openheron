import { expect, test } from "@playwright/test"

import { MARKER_A, openApp, openThread, openToolCard, resetBackend, sendMessage, setScenario } from "./fixtures"

const CARD = '[data-testid="tool-card"]'
const FAILED = '[data-testid="tool-failed"]'
const HISTORY = "History conversation"

test.beforeEach(async () => {
  await resetBackend()
})

// ★ A call that failed says so on the card — and it says it **without** the
//   reader having to open anything: a collapsed card that says "done" about
//   a failed call is a lie about the work that just happened.
test("★ 失败的那次调用在卡上写着 failed", async ({ page }) => {
  await setScenario("toolFailed")
  await openApp(page)
  await openThread(page, "Alpha conversation")
  await sendMessage(page, `${MARKER_A} read a file that is not there`)

  const card = page.locator(CARD).first()
  await expect(card).toBeVisible({ timeout: 7000 })
  await expect(card.locator(FAILED)).toHaveText("failed")
})

// ★ The result still says what the **model** read: the failure judges the
//   call, it does not replace what came back.
test("★ 展开后看到的还是模型读到的那段文字", async ({ page }) => {
  await setScenario("toolFailed")
  await openApp(page)
  await openThread(page, "Alpha conversation")
  await sendMessage(page, `${MARKER_A} read a file that is not there`)

  await openToolCard(page)
  await expect(page.locator('[data-testid="tool-result"]')).toContainText("[tool error] file not found")
})

// ★ The history has to show the same failure the live stream showed: a
//   reload that lost it would show a failed call as a successful one, and
//   the two surfaces would disagree about the same call.
test("★ 历史里那次失败仍然是失败", async ({ page }) => {
  await openApp(page)
  await openThread(page, HISTORY)

  const card = page.locator(CARD).nth(1)
  await expect(card).toBeVisible({ timeout: 7000 })
  await expect(card.locator(FAILED)).toHaveText("failed")
})

// ★ And the call that worked is not painted as a failure: the marker is on
//   the call that failed, and only on it.
test("★ 成功的那次调用没有被说成失败", async ({ page }) => {
  await openApp(page)
  await openThread(page, HISTORY)

  const card = page.locator(CARD).first()
  await expect(card).toBeVisible({ timeout: 7000 })
  await expect(card.locator(FAILED)).toHaveCount(0)
})

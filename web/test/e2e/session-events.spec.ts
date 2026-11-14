import { expect, test } from "@playwright/test"

import {
  MARKER_A,
  clickByText,
  openApp,
  openThread,
  resetBackend,
  sendMessage,
  setScenario,
} from "./fixtures"

test.beforeEach(async () => {
  await resetBackend()
})

// ★ The session's announcement channel.
//
// ★ The bug it fixes: a second surface waits on an approval, the approval is
//   answered in the first one, and the second one keeps the card — so every
//   answer it offers is refused by the server. ★ The card has to go, and the
//   second surface has to follow the round that continued.
test.describe("会话的公告通道", () => {
  test("★ 审批在别处被回答后，另一个标签页的卡片消失，并跟上了继续的那一轮", async ({
    page,
    context,
  }) => {
    await setScenario("approvalLive")
    await openApp(page)

    // ★ The first surface asks, and the round stops for an approval.
    await sendMessage(page, `${MARKER_A} write the file`)
    await expect(page.getByText(/write_file/).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator('[data-testid="approval-card"]')).toBeVisible()

    // ★ The second surface opens the same session and holds the same card.
    const other = await context.newPage()
    await openApp(other)
    await openThread(other, MARKER_A)
    await expect(other.locator('[data-testid="approval-card"]')).toBeVisible({ timeout: 7000 })

    // ★ The first surface answers. ★ The second one is not told by the
    //   answer — it is told by the server.
    await clickByText(page, "Approve", "Allow")

    // ★ The card is gone: the approval was answered, and the server said so.
    await expect(other.locator('[data-testid="approval-card"]')).toHaveCount(0, { timeout: 7000 })

    // ★ And the second surface follows the round that continued.
    await expect(other.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    await expect(other.locator('[data-testid="run-error"]')).toHaveCount(0)
  })
})

import { expect, test } from "@playwright/test"

import {
  COMPOSER,
  MARKER_A,
  clickByText,
  openApp,
  resetBackend,
  sendMessage,
  setScenario,
} from "./fixtures"

const APPROVAL_CARD = '[data-testid="approval-card"]'
const APPROVAL_TOOL = '[data-testid="approval-tool"]'

test.beforeEach(async () => {
  await resetBackend()
})

// ★ **The card must not need a page reload.**
//
// ★ The wire cannot tell a finished round and a cut stream apart: both close
//   the response. ★ The server writes the round's last frame for the first
//   one and deliberately does not for the second (transport/transport.go) —
//   a last frame says "this is everything the run had to say", and an
//   approval raised after that point would stay invisible.
//
// ★ So the client recovers: it asks the ledger whether the round is over,
//   and the transcript answers. ★ Before this, the round after an answer
//   had no way back at all — the card only appeared after a reload.
test.describe("审批卡不靠刷新", () => {
  test("流被切断以后，审批卡自己弹出来", async ({ page }) => {
    await setScenario("cutApproval")
    await openApp(page)

    // ★ The reload count is the point of the test: the card has to arrive
    //   on its own.
    let reloads = 0
    page.on("load", () => {
      reloads += 1
    })

    await sendMessage(page, `${MARKER_A} write the file`)

    await expect(page.locator(APPROVAL_CARD)).toBeVisible({ timeout: 10000 })
    await expect(page.locator(APPROVAL_TOOL)).toHaveText("write_file")
    expect(reloads).toBe(0)
  })

  test("轮次还在跑的时候流被切断，审批卡在它抛出时出现", async ({ page }) => {
    await setScenario("cutLive")
    await openApp(page)

    let reloads = 0
    page.on("load", () => {
      reloads += 1
    })

    await sendMessage(page, `${MARKER_A} write the file`)

    // ★ The round raises the approval a moment after the stream was cut —
    //   and the approval was never on the wire. ★ The card is there when
    //   it is raised, because the client waited for the round and read the
    //   transcript.
    await expect(page.locator(APPROVAL_CARD)).toBeVisible({ timeout: 10000 })
    await expect(page.locator(APPROVAL_TOOL)).toHaveText("write_file")
    expect(reloads).toBe(0)
  })

  test("答完之后下一轮的审批卡自己弹出来", async ({ page }) => {
    await setScenario("cutResume")
    await openApp(page)

    let reloads = 0
    page.on("load", () => {
      reloads += 1
    })

    await sendMessage(page, `${MARKER_A} write the file`)
    await expect(page.locator(APPROVAL_CARD)).toBeVisible({ timeout: 10000 })
    await expect(page.locator(APPROVAL_TOOL)).toHaveText("write_file")

    await clickByText(page, "Approve", "Allow")

    // ★ The round that follows the answer raised another approval, and its
    //   stream was cut before the last frame. ★ This is the case the user
    //   reported: the second card never showed up, and only a reload
    //   brought it back.
    await expect(page.locator(APPROVAL_CARD)).toBeVisible({ timeout: 10000 })
    await expect(page.locator(APPROVAL_TOOL)).toHaveText("edit_file")
    expect(reloads).toBe(0)
  })

  test("审批卡还在的时候不会自己消失", async ({ page }) => {
    await setScenario("cutResume")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} write the file`)
    await expect(page.locator(APPROVAL_CARD)).toBeVisible({ timeout: 10000 })

    // ★ The card is the user's to answer: it must survive the recovery's
    //   own readings of the transcript.
    await page.waitForTimeout(2000)
    await expect(page.locator(APPROVAL_CARD)).toBeVisible()
    await expect(page.locator(COMPOSER)).toHaveCount(0)
  })
})

// ★ **The refusal must not cost the user the question.**
//
// ★ The server refuses an attach on its **runtime**: the turn is gone. ★ The
//   ledger's row is the **barrier**, and it stands up after the approval is
//   written (agentkit/run/bridge.go) — so a refusal can arrive while the
//   approval is still nowhere on disk.
//
// ★ A client that reads the transcript right away finds nothing, and
//   nothing asks again: the card never shows up, and the user reloads the
//   page to get it. ★ That is what the user hit.
test.describe("挂上被拒的轮次", () => {
  test("挂上被拒以后，审批卡自己弹出来", async ({ page }) => {
    await setScenario("refusedAttach")
    await openApp(page)

    let reloads = 0
    page.on("load", () => {
      reloads += 1
    })

    await sendMessage(page, `${MARKER_A} write the file`)

    // ★ The round's approval was never on the wire, and the attach that
    //   would have carried it was refused. ★ The client waits for the
    //   barrier and reads the transcript then.
    await expect(page.locator(APPROVAL_CARD)).toBeVisible({ timeout: 10000 })
    await expect(page.locator(APPROVAL_TOOL)).toHaveText("write_file")
    expect(reloads).toBe(0)
  })
})

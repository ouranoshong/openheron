import { expect, test } from "@playwright/test"

import {
  openApp,
  openThread,
  resetBackend,
  setScenario,
  trackRequests,
  typeAndEnter,
} from "./fixtures"

const BUSY = '[data-testid="composer-meta-busy"]'
const STOP_BUTTON = '[aria-label="Stop generating"]'
const SEND_BUTTON = '[aria-label="Send message"]'
const ALPHA = "Alpha conversation"
const STILL_WORKING = "MARKER-ALPHA still working"

test.beforeEach(async () => {
  await resetBackend()
})

// ★ A second surface does not get kept out of the session: it attaches to
//   the run that owns it and watches the same turn.
//
// ★ Before this, the second tab hydrated as idle, said the session was
//   busy in another window, and showed nothing of the turn that was
//   actually running. The user had to reload by hand to see the answer.
test("第二个界面挂上同一轮，跟着看", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)

  // The first surface starts a turn. The stream stays open, so the session
  // keeps a live turn.
  await typeAndEnter(page, "hello from the first tab")
  await expect(page.getByText(STILL_WORKING)).toBeVisible()

  // A second surface on the same session.
  const other = await context.newPage()
  const seen = trackRequests(other)
  await openApp(other)
  await openThread(other, ALPHA)

  // ★ The text was never in the transcript — the turn has not been
  //   archived — so it can only be on screen because the attach delivered
  //   it.
  await expect(other.getByText(STILL_WORKING)).toBeVisible()

  // ★ And the attach is a request of its own: the run's id, with no
  //   question. It is a read — it neither starts a turn nor claims the
  //   session.
  expect(seen.filter((p) => p === "/v2/run").length).toBeGreaterThan(0)

  // ★ The surface is not kept out: no warning, and the composer offers the
  //   stop button because the session is busy — with the turn this surface
  //   is watching.
  await expect(other.locator(BUSY)).toHaveCount(0)
  await expect(other.locator(STOP_BUTTON)).toBeVisible()
  await expect(other.locator(SEND_BUTTON)).toHaveCount(0)

  // The first surface is still streaming.
  await expect(page.getByText(STILL_WORKING)).toBeVisible()
})

// ★ The run belongs to the server, not to the tab that started it.
//
// ★ So the surface that attached keeps receiving after the other one goes
//   away: the late delta was published after the attach, and it lands
//   here.
test("关掉第一个界面，第二个继续接收", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)
  await typeAndEnter(page, "hello from the first tab")
  await expect(page.getByText(STILL_WORKING)).toBeVisible()

  const other = await context.newPage()
  await openApp(other)
  await openThread(other, ALPHA)
  await expect(other.getByText(STILL_WORKING)).toBeVisible()

  // ★ The first surface goes away.
  await page.close()

  // ★ And the attached one keeps receiving: the run published again after
  //   the attach, and nobody was there to stop it.
  await expect(other.getByText("and still going")).toBeVisible()
  await expect(other.locator(STOP_BUTTON)).toBeVisible()
})

// The race the guard cannot prevent: the second surface hydrated while the
// session was idle, and the first one started a turn in between.
//
// ★ The surface was not there when the turn started — but the session's
//   channel tells it that a round began, so it follows that round instead of
//   saying the session is busy elsewhere. ★ It is the same surface that
//   opens a session with a live turn: that one hears the round from the
//   server's state, this one hears it from the server's announcement.
test("错过一轮开始的第二个界面跟上去，而不是说忙在别处", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)

  // The second surface hydrates while the session is still idle.
  const other = await context.newPage()
  await openApp(other)
  await openThread(other, ALPHA)

  // Now the first surface starts a turn — the second one still believes
  // it may send.
  await typeAndEnter(page, "the first message")
  await expect(page.getByText(STILL_WORKING)).toBeVisible()

  // ★ The second surface hears that a round began, and follows it.
  await expect(other.getByText(STILL_WORKING)).toBeVisible({ timeout: 7000 })

  // ★ And it is not kept out of the session: the round it is watching is
  //   the session's round, so the warning would be a lie.
  await expect(other.locator(BUSY)).toHaveCount(0)
  await expect(other.locator(STOP_BUTTON)).toBeVisible()
})

// A message typed while the round runs never leaves the composer.
//
// ★ The session is busy, so there is nothing to send it to. ★ The draft
//   stays where the user left it — the alternative is losing what they
//   typed — and the transcript never claims a message that was not sent.
test("轮次在跑时写的消息留在输入框里，不进记录", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)

  const other = await context.newPage()
  await openApp(other)
  await openThread(other, ALPHA)

  await typeAndEnter(page, "the first message")
  await expect(page.getByText(STILL_WORKING)).toBeVisible()
  await expect(other.getByText(STILL_WORKING)).toBeVisible({ timeout: 7000 })

  await typeAndEnter(other, "this one is refused")

  // ★ The draft is still there: the user does not lose what they typed.
  await expect(other.locator('[data-testid="composer-textarea"]')).toHaveValue(
    "this one is refused",
  )

  // ★ The transcript holds the round it follows — so the check below is
  //   not vacuous.
  await expect(
    other.locator('[data-testid="message"]').filter({ hasText: STILL_WORKING }).first(),
  ).toBeVisible()

  // ★ And the transcript does not claim the message that was never sent.
  await expect(
    other.locator('[data-testid="message"]').filter({ hasText: "this one is refused" }),
  ).toHaveCount(0)
})

// A busy session is a conflict, not a failure: the user can stop the other
// turn to take over.
//
// ★ And the surface that attached can stop it too: the stop button acts on
//   the session's turn, whoever owns it.
test("挂上以后仍然能停止那一轮来接管", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)
  await typeAndEnter(page, "the first message")
  await expect(page.getByText(STILL_WORKING)).toBeVisible()

  const other = await context.newPage()
  await openApp(other)
  await openThread(other, ALPHA)
  await expect(other.getByText(STILL_WORKING)).toBeVisible()

  // Stopping the turn frees the session.
  //
  // ★ And the session is free because the **ledger says so**: the client
  //   scans the run ledger after the abort. The abort says "I asked"; the
  //   scan says "the session is free".
  const seen = trackRequests(other)
  await other.locator(STOP_BUTTON).click()
  await expect(other.locator(SEND_BUTTON)).toBeVisible()
  expect(seen.filter((p) => p.endsWith("/runs")).length).toBeGreaterThan(0)
})

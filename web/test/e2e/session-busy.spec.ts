import { expect, test } from "@playwright/test"

import { openApp, openThread, resetBackend, setScenario, typeAndEnter } from "./fixtures"

const BUSY = '[data-testid="composer-meta-busy"]'
const STOP_BUTTON = '[aria-label="Stop generating"]'
const ALPHA = "Alpha conversation"

test.beforeEach(async () => {
  await resetBackend()
})

// A second surface sees that the session is busy — from the server's answer,
// not from its own memory. Before this, a fresh tab hydrated as idle and
// its send button worked.
test("第二个界面看到会话正忙，发送按钮被禁用", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)

  // The first surface starts a turn. The stream stays open, so the session
  // keeps a live turn.
  await typeAndEnter(page, "hello from the first tab")
  await expect(page.getByText("MARKER-ALPHA still working")).toBeVisible()

  // A second surface on the same session.
  const other = await context.newPage()
  await openApp(other)
  await openThread(other, ALPHA)

  // Its own view says the session is busy.
  await expect(other.locator(BUSY)).toBeVisible()
  await expect(other.locator(BUSY)).toContainText("running in another window")

  // The composer offers the stop button — the way to take over — and not
  // the send button.
  await expect(other.locator(STOP_BUTTON)).toBeVisible()
  await expect(other.locator('[aria-label="Send message"]')).toHaveCount(0)

  // The first surface is still streaming.
  await expect(page.getByText("MARKER-ALPHA still working")).toBeVisible()
})

// The race the guard cannot prevent: the second surface hydrated while the
// session was idle, and the first one started a turn in between. The
// server refuses, and the refused message leaves no trace.
test("竞态下的第二个请求被拒绝，消息不留痕", async ({ page, context }) => {
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
  await expect(page.getByText("MARKER-ALPHA still working")).toBeVisible()

  await typeAndEnter(other, "this one is refused")

  // The optimistic bubble is rolled back — the server never took it. The
  // scenario's own history stays; what must not be there is the refused
  // message.
  await expect(other.getByText("this one is refused")).toHaveCount(0)

  // And the surface says why.
  await expect(other.locator(BUSY)).toBeVisible()
})

// A busy session is a conflict, not a failure: the user can stop the other
// turn to take over.
test("忙着的会话可以停止另一轮来接管", async ({ page, context }) => {
  await setScenario("busySession")
  await openApp(page)
  await openThread(page, ALPHA)
  await typeAndEnter(page, "the first message")
  await expect(page.getByText("MARKER-ALPHA still working")).toBeVisible()

  const other = await context.newPage()
  await openApp(other)
  await openThread(other, ALPHA)
  await expect(other.locator(BUSY)).toBeVisible()

  // Stopping the turn frees the session.
  //
  // ★ And the session is free because the **ledger says so**: the client
  //   scans the run ledger after the abort. The abort says "I asked"; the
  //   scan says "the session is free".
  const seen: string[] = []
  other.on("request", (r) => seen.push(new URL(r.url()).pathname))
  await other.locator(STOP_BUTTON).click()
  await expect(other.locator(BUSY)).toHaveCount(0)
  expect(seen.filter((p) => p.endsWith("/runs")).length).toBeGreaterThan(0)
})

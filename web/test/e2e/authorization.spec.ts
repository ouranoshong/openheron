import { expect, test } from "@playwright/test"

import { FAKE_URL, MARKER_A, NEW_THREAD, openApp, openThread, resetBackend, sendMessage } from "./fixtures"

const BUTTON = '[data-testid="authorization-button"]'
const LABEL = '[data-testid="authorization-label"]'
const PICKER = '[data-testid="authorization-picker-dialog"]'
const DESCRIPTION = '[data-testid="authorization-picker-description"]'
const OPTION = '[data-testid="authorization-option"]'
const CONFIRM = '[data-testid="authorization-confirm"]'

function option(mode: string) {
  return `${OPTION}[data-mode="${mode}"]`
}

interface FakeThreadMeta {
  threadId: string
  authorizationMode?: string
  authorizationConfirmed?: boolean
}

// fakeThreads reads the server's own table — the mode that is on the screen
// has to be the mode the server holds.
async function fakeThreads(page: import("@playwright/test").Page): Promise<FakeThreadMeta[]> {
  const res = await page.request.get(`${FAKE_URL}/__fake/threads`)
  const body = (await res.json()) as { items: FakeThreadMeta[] }
  return body.items
}

test.beforeEach(async () => {
  await resetBackend()
})

test("输入框下面那一行能选三档", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")

  await expect(page.locator(LABEL)).toHaveText("Default approval")

  await page.locator(BUTTON).click()
  await expect(page.locator(PICKER)).toBeVisible()
  await expect(page.locator(OPTION)).toHaveCount(3)
  await expect(page.locator(OPTION).nth(0)).toContainText("Default approval")
  await expect(page.locator(OPTION).nth(1)).toContainText("Trusted paths")
  await expect(page.locator(OPTION).nth(2)).toContainText("Full authorization")
})

test("★ 「下一轮生效」写在弹窗的描述里", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")

  await page.locator(BUTTON).click()

  // ★ The rule is always true, so it lives in a place that is always there —
  //   not in a badge on the button that would come and go with the run.
  await expect(page.locator(DESCRIPTION)).toContainText("takes effect on the next turn")
  // ★ And the button carries the level and nothing else.
  await expect(page.locator(LABEL)).toHaveText("Default approval")
})

test("选中即生效，刷新后还在", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")

  await page.locator(BUTTON).click()
  await page.locator(option("trusted")).click()
  await expect(page.locator(LABEL)).toHaveText("Trusted paths")

  // ★ The mode is the server's: a reload reads it back from there.
  await page.reload()
  await expect(page.locator(LABEL)).toHaveText("Trusted paths")
})

test("★ 切到完全授权要弹一次确认框，确认之后不再弹", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")

  await page.locator(BUTTON).click()
  await page.locator(option("full")).click()

  // ★ The box says what the level means — it is not "are you sure?".
  await expect(page.locator(CONFIRM)).toBeVisible()
  await expect(page.locator(CONFIRM)).toContainText(
    "every tool runs without asking, including shell commands and configuration changes",
  )

  await page.getByRole("button", { name: "Enable full authorization" }).click()
  await expect(page.locator(LABEL)).toHaveText("Full authorization")

  // ★ The confirmation belongs to the session: the second time, no box.
  await page.locator(BUTTON).click()
  await page.locator(option("default")).click()
  await expect(page.locator(LABEL)).toHaveText("Default approval")

  await page.locator(BUTTON).click()
  await page.locator(option("full")).click()
  await expect(page.locator(CONFIRM)).toHaveCount(0)
  await expect(page.locator(LABEL)).toHaveText("Full authorization")
})

test("★ Escape 能关掉确认框", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")

  await page.locator(BUTTON).click()
  await page.locator(option("full")).click()
  await expect(page.locator(CONFIRM)).toBeVisible()

  await page.keyboard.press("Escape")
  await expect(page.locator(CONFIRM)).toHaveCount(0)
  // ★ The refusal changed nothing: the mode is still the old one.
  await expect(page.locator(LABEL)).toHaveText("Default approval")
})

test("★ 欢迎页上也能改档位 —— 它跟着新会话走", async ({ page }) => {
  await openApp(page)
  const before = await fakeThreads(page)
  await page.locator(NEW_THREAD).click()

  // ★ The button is not disabled: a choice nobody can make is a choice the
  //   welcome page cannot express.
  await expect(page.locator(BUTTON)).toBeEnabled()
  await page.locator(BUTTON).click()
  await expect(page.locator(PICKER)).toBeVisible()
  // ★ There is no running turn yet, so the description says what actually
  //   happens: the mode is carried into the conversation.
  await expect(page.locator(DESCRIPTION)).toContainText("the conversation that is about to start")

  await page.locator(option("trusted")).click()
  await expect(page.locator(LABEL)).toHaveText("Trusted paths")

  // ★ And the staged mode travels with the session that is born — the first
  //   turn already runs in it.
  await sendMessage(page, `${MARKER_A} hello`)
  await expect.poll(async () => (await fakeThreads(page)).length).toBe(before.length + 1)
  const created = (await fakeThreads(page)).filter(
    (t) => !before.some((b) => b.threadId === t.threadId),
  )
  expect(created).toHaveLength(1)
  expect(created[0]!.authorizationMode).toBe("trusted")
})

test("★ 欢迎页上选完全授权要弹一次确认框，确认之后带进新会话", async ({ page }) => {
  await openApp(page)
  const before = await fakeThreads(page)
  await page.locator(NEW_THREAD).click()

  await page.locator(BUTTON).click()
  await page.locator(option("full")).click()

  // ★ There is no server to ask yet, so the client asks — and the server
  //   checks the answer when the conversation is born.
  await expect(page.locator(CONFIRM)).toBeVisible()
  await page.getByRole("button", { name: "Enable full authorization" }).click()
  await expect(page.locator(LABEL)).toHaveText("Full authorization")

  await sendMessage(page, `${MARKER_A} hello`)
  await expect.poll(async () => (await fakeThreads(page)).length).toBe(before.length + 1)
  const created = (await fakeThreads(page)).filter(
    (t) => !before.some((b) => b.threadId === t.threadId),
  )
  expect(created).toHaveLength(1)
  expect(created[0]!.authorizationMode).toBe("full")
  expect(created[0]!.authorizationConfirmed).toBe(true)
})

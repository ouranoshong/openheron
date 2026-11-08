import { expect, test } from "@playwright/test"

import {
  COMPOSER,
  MARKER_A,
  STOP_BUTTON,
  openApp,
  resetBackend,
  sendMessage,
  setMCPMode,
} from "./fixtures"

const PANEL = '[data-testid="mcp-panel"]'
const SERVER = '[data-testid="mcp-server"]'
const TOOL = '[data-testid="mcp-tool"]'
const EMPTY = '[data-testid="mcp-panel-empty"]'
const ERROR = '[data-testid="mcp-server-error"]'

test.beforeEach(async () => {
  await resetBackend()
})

test("/mcps 打开面板，列出服务器的两种名字与档位", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, `${MARKER_A} hello`)

  // ★ The baseline has to be taken on a **settled** transcript, and "settled"
  //   has to be proved, not assumed. A count read while the first turn is
  //   still streaming cannot tell "the command wrote into the transcript" from
  //   "the answer arrived" — and the difference is the whole assertion. Under
  //   load the answer landed between the two reads and the test reported a
  //   leak that never happened.
  //
  // ★ Two waits, and the order matters: waiting only for "not busy" passes
  //   instantly **before** the run starts (the send button is still there and
  //   no stop button has rendered yet), which is the same race wearing a
  //   different hat. The answer must be in the transcript first.
  await expect
    .poll(async () => page.locator('[data-testid="message"]').count(), { timeout: 10_000 })
    .toBeGreaterThan(1)
  await expect(page.locator(STOP_BUTTON)).toHaveCount(0)
  const before = await page.locator('[data-testid="message"]').count()

  await sendMessage(page, "/mcps")
  await expect(page.locator(PANEL)).toBeVisible()

  // The panel is a dialog: it must not write into the transcript.
  // ★ Web-first, not a sampled count: if the client ever shows the command as
  //   an optimistic bubble and rolls it back, sampling fails on the frame it
  //   was there; waiting for the count asks the question the test means.
  await expect(page.locator('[data-testid="message"]')).toHaveCount(before)

  const servers = page.locator(SERVER)
  await expect(servers).toHaveCount(3)

  // ★ Both vocabularies are shown: the server's own name and the name the
  //   model sees.
  const first = servers.filter({ hasText: "github" })
  await expect(first).toHaveAttribute("data-status", "ready")
  await expect(first.locator(TOOL).first()).toContainText("read_issue")
  await expect(first.locator(TOOL).first()).toContainText("mcp__github__read_issue")
  await expect(first.locator(TOOL).first()).toContainText("ask")
})

test("面板照实报出够不着的服务器与断掉的服务器", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, "/mcps")

  const unreachable = page.locator(SERVER).filter({ hasText: "files" })
  await expect(unreachable).toHaveAttribute("data-status", "unreachable")
  await expect(unreachable.locator(ERROR)).toContainText("not on the PATH")

  // ★ An unused server is "unused", not missing — and it has no tool rows.
  const unused = page.locator(SERVER).filter({ hasText: "deploy" })
  await expect(unused).toHaveAttribute("data-status", "unused")
  await expect(unused.locator(TOOL)).toHaveCount(0)
})

test("一个服务器都没配时，面板说没有", async ({ page }) => {
  await setMCPMode("empty")
  await openApp(page)
  await sendMessage(page, "/mcps")

  await expect(page.locator(PANEL)).toBeVisible()
  await expect(page.locator(EMPTY)).toHaveText("No MCP servers are configured")
  await expect(page.locator(SERVER)).toHaveCount(0)
})

test("面板每次打开都重新读，不吃缓存", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, "/mcps")
  await expect(page.locator(SERVER)).toHaveCount(3)

  await page.keyboard.press("Escape")
  await expect(page.locator(PANEL)).toHaveCount(0)

  await setMCPMode("empty")
  await sendMessage(page, "/mcps")
  await expect(page.locator(EMPTY)).toBeVisible()
})

test("Refresh 重新读一次", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, "/mcps")
  await expect(page.locator(SERVER)).toHaveCount(3)

  await setMCPMode("empty")
  await page.click('[data-testid="mcp-refresh"]')
  await expect(page.locator(EMPTY)).toBeVisible()
})

test("/mcps 不进模型，也不建会话", async ({ page }) => {
  await openApp(page)
  const before = await page.locator('[data-testid="message"]').count()

  await sendMessage(page, "/mcps")
  await expect(page.locator(PANEL)).toBeVisible()
  await expect(page.locator(COMPOSER)).toHaveValue("")
  await expect(page.locator('[data-testid="message"]')).toHaveCount(before)
})

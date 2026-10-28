import { expect, test } from "@playwright/test"

import {
  clearFakeThreads,
  COMPOSER,
  FAKE_URL,
  MARKER_A,
  NEW_THREAD,
  SEND_BUTTON,
  openApp,
  openThread,
  resetBackend,
  sendMessage,
  setAgentsMode,
  setDefaultRoot,
  trackRequests,
  typeAndEnter,
} from "./fixtures"

const AGENT = '[data-testid="composer-meta-agent"]'
const MODEL = '[data-testid="composer-meta-model"]'
const REASONING = '[data-testid="composer-meta-reasoning"]'
const PICKER = '[data-testid="agent-picker"]'
const EMPTY = '[data-testid="agent-picker-empty"]'
const OPTION = (id: string) => `[data-testid="agent-option"][data-agent-id="${id}"]`

interface FakeThreadMeta {
  threadId: string
  agentId: string
}

async function fakeThreads(page: import("@playwright/test").Page): Promise<FakeThreadMeta[]> {
  const res = await page.request.get(`${FAKE_URL}/__fake/threads`)
  const body = (await res.json()) as { items: FakeThreadMeta[] }
  return body.items
}

test.beforeEach(async () => {
  await resetBackend()
})

test("/agents 打开选择器，选中后底栏与刷新都跟着变", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, `${MARKER_A} hello`)
  await expect(page.locator(AGENT)).toHaveText("Agent: main")

  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await expect(page.locator(OPTION("main"))).toBeVisible()
  await expect(page.locator(OPTION("worker"))).toBeVisible()

  await page.click(OPTION("worker"))
  await expect(page.locator(PICKER)).toHaveCount(0)
  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
  await expect(page.locator(MODEL)).toHaveText("openai/fast")
  await expect(page.locator(REASONING)).toHaveText("Reasoning: low")

  await page.reload()
  await expect(page.locator(AGENT)).toHaveText("Agent: worker", { timeout: 7000 })
})

test("/agents <name> 直接切换，不弹选择器", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, `${MARKER_A} hello`)
  await sendMessage(page, "/agents worker")

  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
  await expect(page.locator(PICKER)).toHaveCount(0)
  await sendMessage(page, "/agents main")
  await expect(page.locator(AGENT)).toHaveText("Agent: main")
})

test("未知名字打开选择器并清空输入，不发送也不建会话", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, `${MARKER_A} hello`)
  const before = await fakeThreads(page)

  await sendMessage(page, "/agents nope")
  await expect(page.locator(PICKER)).toBeVisible()
  await expect(page.locator(EMPTY)).toBeVisible()
  await expect(page.locator(COMPOSER)).toHaveValue("")

  const after = await fakeThreads(page)
  expect(after.length).toBe(before.length)
})

test("新会话先选 agent 再发送：只建一条，且服务端记下 agentId", async ({ page }) => {
  await openApp(page)
  const before = await fakeThreads(page)

  await page.click(NEW_THREAD)
  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.click(OPTION("worker"))
  await expect(page.locator(AGENT)).toHaveText("Agent: worker")

  await sendMessage(page, `${MARKER_A} first question`)
  await expect.poll(async () => (await fakeThreads(page)).length).toBe(before.length + 1)

  const created = (await fakeThreads(page)).filter((t) => !before.some((b) => b.threadId === t.threadId))
  expect(created).toHaveLength(1)
  expect(created[0]!.agentId).toBe("worker")

  await page.reload()
  await expect(page.locator(AGENT)).toHaveText("Agent: worker", { timeout: 7000 })
})

test("配置热更新后，选择器里的 agent 名单自动跟上", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await expect(page.locator(OPTION("latecomer"))).toHaveCount(0)

  await setAgentsMode("grew")

  await expect(page.locator(OPTION("latecomer"))).toBeVisible({ timeout: 10000 })
})

test("上下移动选项时高亮项滚进视野", async ({ page }) => {
  await setAgentsMode("many")
  await openApp(page)
  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()

  const insideList = (index: number) =>
    page.evaluate((i) => {
      const list = document.querySelector('[data-testid="agent-picker-list"]')
      if (!list) return false
      const opt = list.querySelectorAll('[data-testid="agent-option"]')[i]
      if (!opt) return false
      const l = list.getBoundingClientRect()
      const o = opt.getBoundingClientRect()
      return o.top >= l.top && o.bottom <= l.bottom
    }, index)

  expect(await insideList(0)).toBe(true)

  for (let i = 1; i <= 8; i++) {
    await page.locator('[data-testid="agent-picker-filter"]').press("ArrowDown")
    expect(await insideList(i)).toBe(true)
  }

  for (let i = 7; i >= 0; i--) {
    await page.locator('[data-testid="agent-picker-filter"]').press("ArrowUp")
    expect(await insideList(i)).toBe(true)
  }
})

test("/skill 打开选择器，选中后回填命令（不直接发送）", async ({ page }) => {
  await openApp(page)
  const reqs = trackRequests(page)

  await sendMessage(page, "/skill")
  await expect(page.locator('[data-testid="skill-picker"]')).toBeVisible()

  await expect(page.locator('[data-testid="skill-option"][data-skill-name="refactor"]')).toBeVisible()
  await expect(
    page.locator('[data-testid="skill-option"][data-skill-name="refactor"] [data-testid="skill-option-mode"]'),
  ).toHaveText("Run in the current agent")
  await expect(
    page.locator('[data-testid="skill-option"][data-skill-name="research"] [data-testid="skill-option-mode"]'),
  ).toHaveText("Run in a separate agent")

  await page.locator('[data-testid="skill-option"][data-skill-name="refactor"]').click()
  await expect(page.locator('[data-testid="skill-picker"]')).toHaveCount(0)
  await expect(page.locator(COMPOSER)).toHaveValue("/skill refactor ")

  await page.locator(COMPOSER).fill("/skill refactor 把这个函数拆开")
  await page.locator(SEND_BUTTON).click()

  await expect.poll(() => reqs.filter((p) => p.includes("/v2/run")).length).toBeGreaterThan(0)
  await expect(page.getByText("把这个函数拆开").first()).toBeVisible()
})

test("会话列表为空时清掉记住的 id", async ({ page }) => {
  await openApp(page)
  await clearFakeThreads()

  await page.reload()
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("openheron.activeThread")))
    .toBeNull()
  await expect(page.locator('[data-testid="run-error"]')).toHaveCount(0)
})

test("没选过 agent 时用服务端的默认根", async ({ page }) => {
  await setDefaultRoot("worker")

  await openApp(page)
  const before = await fakeThreads(page)
  await page.click(NEW_THREAD)
  await sendMessage(page, `${MARKER_A} hello`)

  await expect.poll(async () => (await fakeThreads(page)).length).toBe(before.length + 1)
  const created = (await fakeThreads(page)).filter((t) => !before.some((b) => b.threadId === t.threadId))
  expect(created).toHaveLength(1)
  expect(created[0]!.agentId).toBe("worker")

  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
})

test("/v2/run 缺 threadId 报 400，且不建会话", async ({ page }) => {
  await openApp(page)
  const before = (await fakeThreads(page)).length

  const res = await page.request.post(`${FAKE_URL}/v2/run`, {
    data: { query: "hello" },
  })
  expect(res.status()).toBe(400)
  const body = (await res.json()) as { code?: string }
  expect(body.code).toBe("invalid_body")

  expect((await fakeThreads(page)).length).toBe(before)
})

test("切到 worker 后历史消息保留，只有之后的轮次换根", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")
  const historyBefore = await page.locator('[data-testid="message"]').count()

  await sendMessage(page, "/agents worker")
  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
  expect(await page.locator('[data-testid="message"]').count()).toBe(historyBefore)

  await sendMessage(page, `${MARKER_A} after switch`)
  await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
})

test("选择器关掉后焦点回到输入框", async ({ page }) => {
  await openApp(page)
  await typeAndEnter(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.click(OPTION("worker"))
  await expect(page.locator(PICKER)).toHaveCount(0)

  await expect(page.locator(COMPOSER)).toBeFocused()
})

test("Esc 关掉选择器后焦点也回到输入框", async ({ page }) => {
  await openApp(page)
  await typeAndEnter(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.locator(PICKER)).toHaveCount(0)

  await expect(page.locator(COMPOSER)).toBeFocused()
})

test("选择器关掉后能直接接着打字", async ({ page }) => {
  await openApp(page)
  await typeAndEnter(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.click(OPTION("worker"))
  await expect(page.locator(PICKER)).toHaveCount(0)

  await page.keyboard.type("follow up")
  await expect(page.locator(COMPOSER)).toHaveValue("follow up")
})

test("/agents <name> 直接切换后焦点仍在输入框", async ({ page }) => {
  await openApp(page)
  await typeAndEnter(page, "/agents worker")
  await expect(page.locator(PICKER)).toHaveCount(0)

  await expect(page.locator(COMPOSER)).toBeFocused()
})

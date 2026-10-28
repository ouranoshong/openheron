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
  // 只列可当根的 agent：main 与它的直接子 agent。
  await expect(page.locator(OPTION("main"))).toBeVisible()
  await expect(page.locator(OPTION("worker"))).toBeVisible()

  await page.click(OPTION("worker"))
  await expect(page.locator(PICKER)).toHaveCount(0)
  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
  await expect(page.locator(MODEL)).toHaveText("openai/fast")
  await expect(page.locator(REASONING)).toHaveText("Reasoning: low")

  // ★ 刷新后从 threadMeta.agentId 恢复：只改内存的话这里会弹回 main。
  await page.reload()
  await expect(page.locator(AGENT)).toHaveText("Agent: worker", { timeout: 7000 })
})

test("/agents <name> 直接切换，不弹选择器", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, `${MARKER_A} hello`)
  await sendMessage(page, "/agents worker")

  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
  await expect(page.locator(PICKER)).toHaveCount(0)
  // 切回 main 也要生效。
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
  // ★ 前端把暂存值放进了 POST /v2/run 的 body，服务端据此落盘。
  expect(created[0]!.agentId).toBe("worker")

  // 暂存值发送后清空：刷新回到那条新会话，仍然是 worker。
  await page.reload()
  await expect(page.locator(AGENT)).toHaveText("Agent: worker", { timeout: 7000 })
})

/**
 * ★ 配置在**编辑器里**改了 → 弹窗里的 agent 名单也得跟着变。
 *
 * 用户在 `.openheron/agents/` 里加了一个配置文件，服务端热更新了（config/reload.go），
 * 但浏览器此前**没有任何信号** —— 而名单是模块级缓存，于是弹窗一直显示旧名单。
 *
 * 这条钉的是「前端能发现配置变了」：服务端把指纹（config.Version）换掉，
 * 前端轮询比对到不同 ⇒ 自己把缓存换成新的。
 *
 * ★ 不刷新页面：刷新当然会拿到新名单，但那只证明「刷新能解决」，
 *   不证明「不刷新也能跟上」—— 而后者才是用户要的。
 */
test("配置热更新后，选择器里的 agent 名单自动跟上", async ({ page }) => {
  await openApp(page)
  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await expect(page.locator(OPTION("latecomer"))).toHaveCount(0)

  // ★ 用户在编辑器里加了一个 agent —— 服务端重新加载了配置，名单变长、指纹也变。
  await setAgentsMode("grew")

  // 不刷新、不重新打开弹窗：轮询发现指纹变了就该把新名单显示出来。
  await expect(page.locator(OPTION("latecomer"))).toBeVisible({ timeout: 10000 })
})

/**
 * ★ 上下移动选项时，**高亮的那一项要跟着滚**。
 *
 * 列表是 `max-h-72 overflow-y-auto`：agent 多的时候它会滚。而键盘移动只改
 * 游标、不改滚动位置 —— 于是光标走到了看不见的地方：用户按 Enter 选中
 * 了一个自己都看不到的 agent。
 */
test("上下移动选项时高亮项滚进视野", async ({ page }) => {
  // 一堆 agent：列表才会溢出，不溢出就看不出滚动。
  await setAgentsMode("many")
  await openApp(page)
  await sendMessage(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()

  // ★ 断言「高亮项完全落在列表的可见框内」—— 比 toBeInViewport 准：
  //   后者看的是浏览器视口，而被容器裁掉的项也可能落在视口里。
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

  // 第一项一开始就在视野里。
  expect(await insideList(0)).toBe(true)

  // 连按 ↓：游标一路往下，高亮项必须**始终**在视野里。
  for (let i = 1; i <= 8; i++) {
    await page.locator('[data-testid="agent-picker-filter"]').press("ArrowDown")
    expect(await insideList(i)).toBe(true)
  }

  // 反方向也一样：回到顶部时列表也跟着回。
  for (let i = 7; i >= 0; i--) {
    await page.locator('[data-testid="agent-picker-filter"]').press("ArrowUp")
    expect(await insideList(i)).toBe(true)
  }
})

/**
 * ★ `/skill`：打开选择器 → 选中 → 回填命令（**不直接发送**）。
 *
 * 选中只回填命令文本 —— 用户多半还要补一句任务才发。
 *
 * 展开成「用这个 skill」的话是**服务端**的事（api/commands.go 的模板），
 * 这里钉的是界面这一段：选择器列得出、选得中、回填得对；补一句任务后
 * 发出去的是 `/skill <name> …`。
 */
test("/skill 打开选择器，选中后回填命令（不直接发送）", async ({ page }) => {
  await openApp(page)
  const reqs = trackRequests(page)

  // 无参 → 打开选择器。
  await sendMessage(page, "/skill")
  await expect(page.locator('[data-testid="skill-picker"]')).toBeVisible()

  // 清单带描述与执行模式 —— 描述是模型选不选它的依据。
  await expect(page.locator('[data-testid="skill-option"][data-skill-name="refactor"]')).toBeVisible()
  await expect(
    page.locator('[data-testid="skill-option"][data-skill-name="refactor"] [data-testid="skill-option-mode"]'),
  ).toHaveText("当前 agent 内执行")
  await expect(
    page.locator('[data-testid="skill-option"][data-skill-name="research"] [data-testid="skill-option-mode"]'),
  ).toHaveText("另起 agent 执行")

  // 选中 → 回填命令文本（不直接发送：用户还要补一句任务）。
  await page.locator('[data-testid="skill-option"][data-skill-name="refactor"]').click()
  await expect(page.locator('[data-testid="skill-picker"]')).toHaveCount(0)
  await expect(page.locator(COMPOSER)).toHaveValue("/skill refactor ")

  // 补一句任务再发 —— 发出去的是**命令**，服务端负责展开。
  await page.locator(COMPOSER).fill("/skill refactor 把这个函数拆开")
  await page.locator(SEND_BUTTON).click()

  await expect.poll(() => reqs.filter((p) => p.includes("/v2/run")).length).toBeGreaterThan(0)
  // 发出去的是**命令**；展开成「用这个 skill」的话是服务端的事 ✅。
  await expect(page.getByText("把这个函数拆开").first()).toBeVisible()
})

/**
 * ★ 服务端**一个会话都没有** ⇒ 记住的 id 一定悬空 ⇒ 当场清掉。
 *
 * 这条与上面那条（会话被删了）走的是不同的判据：那条看的是「打开它时 404」，
 * 这条看的是「列表是空的」✅ —— 后者不依赖任何一次请求的成败 ✅ ✅。
 */
test("会话列表为空时清掉记住的 id", async ({ page }) => {
  await openApp(page)
  await clearFakeThreads()

  await page.reload()
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("openheron.activeThread")))
    .toBeNull()
  await expect(page.locator('[data-testid="run-error"]')).toHaveCount(0)
})

/**
 * ★ 前端不替服务端选根。
 *
 * 根 agent 是**会话的属性**，建会话那一刻由服务端定：用户选了就用选的，
 * 没选就交给服务端定。前端自作主张发一个 main 过去，等于把「默认根」按死
 * 在界面上 —— 服务端改口时它也不算数。
 *
 * ★ 真后端的默认根恒为 main，所以「前端发了 main」与「服务端选了 main」
 *   看起来一模一样。这个用例把**假后端**的默认根换成 worker，那一眼就
 *   看得出前端有没有自作主张。
 */
test("没选过 agent 时用服务端的默认根", async ({ page }) => {
  // 把服务端的默认根改成 worker（真后端恒为 main —— 换掉它才看得见）。
  await setDefaultRoot("worker")

  await openApp(page)
  const before = await fakeThreads(page)
  await page.click(NEW_THREAD)
  await sendMessage(page, `${MARKER_A} hello`)

  await expect.poll(async () => (await fakeThreads(page)).length).toBe(before.length + 1)
  const created = (await fakeThreads(page)).filter((t) => !before.some((b) => b.threadId === t.threadId))
  expect(created).toHaveLength(1)
  expect(created[0]!.agentId).toBe("worker")

  // 底栏也要换成服务端的根：会话的属性说了算。
  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
})

/**
 * ★ 会话只由 `POST /v2/sessions` 建 —— `/v2/run` 一律不建。
 *
 * 缺 threadId 是**请求不合法**（400），不是「顺便建一个」；而给了 id 但
 * 没有这个会话是 404 —— 两者答的是不同的问题。
 */
test("/v2/run 缺 threadId 报 400，且不建会话", async ({ page }) => {
  await openApp(page)
  const before = (await fakeThreads(page)).length

  const res = await page.request.post(`${FAKE_URL}/v2/run`, {
    data: { query: "hello" },
  })
  expect(res.status()).toBe(400)
  const body = (await res.json()) as { code?: string }
  expect(body.code).toBe("invalid_body")

  // 一个会话都不该多出来。
  expect((await fakeThreads(page)).length).toBe(before)
})

test("切到 worker 后历史消息保留，只有之后的轮次换根", async ({ page }) => {
  await openApp(page)
  await openThread(page, "Alpha conversation")
  const historyBefore = await page.locator('[data-testid="message"]').count()

  await sendMessage(page, "/agents worker")
  await expect(page.locator(AGENT)).toHaveText("Agent: worker")
  expect(await page.locator('[data-testid="message"]').count()).toBe(historyBefore)

  // 下一轮照常跑完（切换不该打断输入）。
  await sendMessage(page, `${MARKER_A} after switch`)
  await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
})

// ── 焦点 ──────────────────────────────────────────────────────────────────

test("选择器关掉后焦点回到输入框", async ({ page }) => {
  // ★ Radix Dialog 的焦点恢复依赖「打开它的那个 trigger」—— 而选择器是
  //   **程序化**打开的（setPicker({open:true})），没有 trigger。所以 Radix
  //   无处可还原，焦点落到 <body>。
  //
  //   症状是「命令执行完了，但接着敲字什么都没进去」—— 用户会以为命令
  //   没生效，而不是「焦点跑了」。
  await openApp(page)
  await typeAndEnter(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.click(OPTION("worker"))
  await expect(page.locator(PICKER)).toHaveCount(0)

  await expect(page.locator(COMPOSER)).toBeFocused()
})

test("Esc 关掉选择器后焦点也回到输入框", async ({ page }) => {
  // 取消选择器之后用户要接着打字 —— 焦点必须还在输入框里。
  await openApp(page)
  await typeAndEnter(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.locator(PICKER)).toHaveCount(0)

  await expect(page.locator(COMPOSER)).toBeFocused()
})

test("选择器关掉后能直接接着打字", async ({ page }) => {
  // ★ 上一条（焦点在不在）是**代理指标**；这一条才是用户真正看见的：
  //   焦点不在输入框时，敲的字根本进不去。
  await openApp(page)
  await typeAndEnter(page, "/agents")
  await expect(page.locator(PICKER)).toBeVisible()
  await page.click(OPTION("worker"))
  await expect(page.locator(PICKER)).toHaveCount(0)

  await page.keyboard.type("follow up")
  await expect(page.locator(COMPOSER)).toHaveValue("follow up")
})

test("/agents <name> 直接切换后焦点仍在输入框", async ({ page }) => {
  // 这条路径**不**开选择器，焦点本来就没丢 —— 但它值得钉住：
  // 免得以后有人把「恢复焦点」做成「每次切换后都重聚焦」，而那会在
  // 用户已经点到别处时把焦点抢回来。
  await openApp(page)
  await typeAndEnter(page, "/agents worker")
  await expect(page.locator(PICKER)).toHaveCount(0)

  await expect(page.locator(COMPOSER)).toBeFocused()
})

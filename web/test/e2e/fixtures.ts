import { expect, type Locator, type Page } from "@playwright/test"

import { FAKE_URL } from "../../playwright.config"

export { FAKE_URL }

/**
 * E2E 公用夹具。
 *
 * ★ 所有定位都走语义（role / aria / 可见文本），不用 CSS 类名。
 *   样式类会被 Tailwind 改、被 shadcn 重新生成；语义标签是产品契约，
 *   界面重写时它应该保持不变。
 */

export const MARKER_A = "MARKER-ALPHA"
export const MARKER_B = "MARKER-BRAVO"

/** 切假后端的回放场景。必须在 openApp 之前调用。 */
export async function setScenario(name: string): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/scenario?name=${name}`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to set scenario ${name}: ${r.status}`)
}

/**
 * 切 `/v2/agents` 的视图：默认（单根）、多主、一大堆、或「热更新后」。
 *
 * ★ 多层拓扑用来验「选择器列出全部 agent，且显示每个 agent 的队伍规模」。
 * ★ `grew` 模拟「用户在 `.openheron/agents/` 里加了一个配置文件」：名单变长，
 *   配置指纹也变了 —— 前端靠指纹知道该刷新了。
 */
export async function setAgentsMode(mode: "default" | "multi" | "many" | "grew"): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/agents-mode?mode=${mode}`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to set agents mode ${mode}: ${r.status}`)
}

/**
 * 改服务端的**默认根**（对应真后端配置里 `main: true` 标了谁）。
 *
 * ★ 用来验「前端不替服务端选根」：默认根不是 main 时，用户没选过 agent
 *   建出来的会话也该是它。
 */
export async function setDefaultRoot(agent: string): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/default-root?agent=${agent}`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to set the default root ${agent}: ${r.status}`)
}

/**
 * 清空假后端的会话表（模拟「状态目录被清过 / 换了机器」）。
 *
 * ★ 服务端一个会话都没有时，localStorage 里记着的那个 id 一定是悬空的 ✅。
 */
export async function clearFakeThreads(): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/threads/clear`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to clear the fake threads: ${r.status}`)
}

/** 重置假后端的会话表与请求记录。 */
export async function resetBackend(): Promise<void> {
  await fetch(`${FAKE_URL}/__fake/reset`, { method: "POST" })
}

// ── 定位点 ────────────────────────────────────────────────────────────────

export const NEW_THREAD = '[data-testid="new-thread"]'
export const THREAD_ITEM = '[data-testid="thread-item"]'
export const THREAD_ROW = '[data-testid="thread-row"]'
export const COMPOSER = '[aria-label="Message input"]'
export const SEND_BUTTON = '[aria-label="Send message"]'
export const COMPOSER_POPUP = '[data-testid="composer-popup"]'
export const COMPOSER_OPTION = '[data-testid="composer-option"]'

/**
 * 审批按钮。
 *
 * ★ 匹配中英两种写法：Phase 0 跑在现有界面（中文），Phase 4 重写后
 *   按 AGENTS.md 改成英文（见计划里的决策）。spec 描述的是行为，
 *   不该因为界面文案改了就失效。
 */
export const APPROVE_BUTTON = "button, [role=button]"
/** 停止按钮（composer 里的「停止生成」）。 */
export const STOP_BUTTON = '[aria-label="Stop generating"]'

/** 点击按钮，按可见文本匹配（支持中英）。 */
export async function clickByText(page: Page, ...texts: string[]): Promise<void> {
  const el = await findByText(page, ["button"], texts)
  if (!el) throw new Error(`no button matching any of: ${texts.join(" / ")}`)
  await el.click()
}

/**
 * 点击菜单项。
 *
 * ★ 与 clickByText 分开：菜单项的 ARIA role 是 menuitem 不是 button。
 *   第一版用 button 去 find，结果那条 spec 一直红 —— 而红的原因是
 *   spec 写错了，不是应用有问题。这个区分能避免把测试的错看成应用的错。
 */
export async function clickMenuItem(page: Page, ...texts: string[]): Promise<void> {
  const el = await findByText(page, ["menuitem", "button", "link"], texts)
  if (!el) throw new Error(`no menu item matching any of: ${texts.join(" / ")}`)
  await el.click()
}

async function findByText(
  page: Page,
  roles: readonly string[],
  texts: readonly string[],
): Promise<Locator | null> {
  for (const role of roles) {
    for (const t of texts) {
      const loc = page.getByRole(role as "button", { name: t }).first()
      if (await loc.count()) return loc
    }
  }
  return null
}

// ── 操作 ──────────────────────────────────────────────────────────────────

/** 打开应用并等首屏稳定（侧栏会话列表已渲染）。 */
export async function openApp(page: Page): Promise<void> {
  await page.goto("/")
  await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
}

/** 在输入框里发送一条消息。 */
export async function sendMessage(page: Page, text: string): Promise<void> {
  const box = page.locator(COMPOSER)
  await box.click()
  await box.fill(text)
  await page.locator(SEND_BUTTON).click()
}

/**
 * 在输入框里打字并按 Enter。
 *
 * ★ 与 [sendMessage] 是**两条不同的路径**，别混用：
 *
 *   sendMessage 点发送按钮 —— 焦点会落到按钮上，而按钮在输入框清空后
 *   变 disabled，于是焦点掉到 body。断言「焦点还在输入框」时用它会
 *   得到假红。
 *
 *   typeAndEnter 走真实路径（用户敲回车），焦点始终在输入框里。
 */
export async function typeAndEnter(page: Page, text: string): Promise<void> {
  const box = page.locator(COMPOSER)
  await box.click()
  await box.fill(text)
  await box.press("Enter")
}

/** 侧栏里所有会话条目的标题文本。 */
export async function threadTitles(page: Page): Promise<string[]> {
  const items = page.locator(THREAD_ITEM)
  const n = await items.count()
  const out: string[] = []
  for (let i = 0; i < n; i++) out.push((await items.nth(i).innerText()).trim())
  return out
}

/** 点击标题匹配的那一条会话。 */
export async function openThread(page: Page, title: string): Promise<void> {
  const row = page.locator(THREAD_ITEM).filter({ hasText: title }).first()
  await row.waitFor({ state: "visible", timeout: 5000 })
  await row.click()
}

/**
 * 页面启动以来发出过的所有请求路径。
 *
 * ★ 用它断言「不该发的请求没发」—— 比如切换会话时的
 *   GET /v2/sessions/undefined/messages。抓网络请求比断言 DOM 可靠：
 *   那类 bug 的表现是请求发出去了但 404，DOM 上反而看不出异常。
 */
export function trackRequests(page: Page): string[] {
  const seen: string[] = []
  page.on("request", (r) => {
    const p = new URL(r.url()).pathname
    if (p.startsWith("/v2/")) seen.push(p)
  })
  return seen
}

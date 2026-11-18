import { expect, test, type Page } from "@playwright/test"

import { openApp, SEND_BUTTON, THREAD_ITEM, THREAD_ROW } from "./fixtures"

// ★ The three viewports the interface has to survive.
//
// ★ Not one: the phone (390), the tablet in portrait (768) and the desktop
//   (1440). ★ The tablet is the interesting one — it sits **on** the `md`
//   breakpoint, so a layout that only holds "above" or "below" it is caught
//   here and nowhere else. ★ The desktop is here for the same reason in the
//   other direction: the window can be narrow on a desktop too.
const PHONE = { width: 390, height: 844 }
const TABLET = { width: 768, height: 1024 }
const DESKTOP = { width: 1440, height: 900 }

/**
 * How much the page overflows sideways, in pixels.
 *
 * ★ The whole point of the chat column is that it never pushes the page
 *   sideways: a wide table or a long line scrolls **inside** its own box,
 *   and the window keeps its scrollbar for the window. ★ A positive number
 *   here is a layout that has broken out of its column.
 */
async function horizontalOverflow(page: Page): Promise<number> {
  return page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
}

/**
 * The app is up on a narrow screen.
 *
 * ★ `openApp` waits for the header on every width, and for the session tree
 *   only where the sidebar is docked. ★ The composer is what a narrow
 *   screen's layout is about, so it is what gets waited for here.
 */
async function openNarrowApp(page: Page): Promise<void> {
  await openApp(page)
  await expect(page.locator('[data-testid="composer-textarea"]')).toBeVisible()
}

/**
 * Make the conversation tree longer than the screen.
 *
 * ★ The tree is the one part of the layout whose length the **user** decides:
 *   a person with thirty conversations must not get a different interface from
 *   a person with three. ★ The rows are injected rather than served — the fake
 *   backend's tree is short, and the point of the test is a tree that is
 *   **longer than the screen**.
 */
async function growTree(page: Page, rows = 30): Promise<void> {
  await page.evaluate((count) => {
    const tree = document.querySelector('[data-testid="session-tree"]')
    if (!(tree instanceof HTMLElement)) throw new Error("no session tree")
    for (let i = 0; i < count; i++) {
      const row = document.createElement("li")
      row.textContent = `Filler conversation ${i}`
      row.style.height = "40px"
      tree.appendChild(row)
    }
  }, rows)
}

/**
 * Where the input box sits.
 */
async function composerBox(page: Page) {
  return page.locator('[data-testid="composer-box"]').boundingBox()
}

/**
 * Open the drawer and wait for the tree it holds.
 */
async function openDrawer(page: Page): Promise<void> {
  const toggle = page.locator('[aria-label="Open conversations"]')
  if (await toggle.count()) {
    await toggle.click()
  }
  await expect(page.locator('[data-testid="sidebar-drawer"]')).toBeVisible()
  await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
}

test.describe("响应式 · 视口", () => {
  test.describe("手机", () => {
    test.use({ viewport: PHONE })

    test("★ 页面不横向滚动", async ({ page }) => {
      await openNarrowApp(page)
      expect(await horizontalOverflow(page)).toBe(0)
    })

    test("★ 聊天列不超过 token 的宽度", async ({ page }) => {
      await openNarrowApp(page)
      const column = await page
        .locator('[data-testid="composer-box"]')
        .evaluate((el) => el.getBoundingClientRect().width)
      // ★ The token is 64rem; the column may be **narrower** than that on a
      //   phone — it may never be wider.
      expect(column).toBeLessThanOrEqual(64 * 16)
    })

    test("★ 输入框和发送键都在窗口里", async ({ page }) => {
      await openNarrowApp(page)
      const send = page.locator(SEND_BUTTON)
      await expect(send).toBeVisible()
      const box = await send.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x + box!.width).toBeLessThanOrEqual(PHONE.width)
      expect(box!.y + box!.height).toBeLessThanOrEqual(PHONE.height)
    })

    test("★ 发送键的触摸目标够大", async ({ page }) => {
      await openNarrowApp(page)
      // ★ The icon is small on purpose; the **target** is what a finger
      //   hits, and it is measured the way a finger meets it: the point
      //   just above the icon still belongs to the button.
      const reaches = await page.evaluate((selector) => {
        const btn = document.querySelector(selector)
        if (!(btn instanceof HTMLElement)) return null
        const r = btn.getBoundingClientRect()
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top - 4)
        return hit === btn || (hit !== null && btn.contains(hit))
      }, SEND_BUTTON)
      expect(reaches, "a tap just above the send button has to reach the button").toBe(true)
    })

    test("★ 会话行自己的菜单键是看得见的", async ({ page }) => {
      await openNarrowApp(page)
      await openDrawer(page)
      const menu = page.locator(THREAD_ROW).first().locator('[aria-label="More options"]')
      await expect(menu).toBeAttached()
      // ★ Playwright calls an `opacity: 0` element visible, so the check is
      //   the computed opacity: `group-hover` compiles into
      //   `@media (hover: hover)`, and a touch screen never fires it.
      const opacity = await menu.evaluate((el) => getComputedStyle(el).opacity)
      expect(opacity, "the row's own menu has to be there without a pointer").toBe("1")
    })

    test("★ 窄屏上侧栏是抽屉", async ({ page }) => {
      await openNarrowApp(page)
      // ★ The sidebar is **not in the flow**: it used to sit above the chat,
      //   which meant scrolling past the whole conversation list to reach
      //   the conversation.
      await expect(page.locator('[data-testid="sidebar"]')).toHaveCount(0)

      await page.locator('[aria-label="Open conversations"]').click()
      await expect(page.locator('[data-testid="sidebar-drawer"]')).toBeVisible()
      await expect(page.locator('[data-testid="sidebar"]')).toBeVisible()

      // ★ Escape closes it — Radix's own rule, and the one the stylesheet
      //   has to live with.
      await page.keyboard.press("Escape")
      await expect(page.locator('[data-testid="sidebar-drawer"]')).toHaveCount(0)
    })

    test("★ 点遮罩也关掉抽屉", async ({ page }) => {
      await openNarrowApp(page)
      await page.locator('[aria-label="Open conversations"]').click()
      await expect(page.locator('[data-testid="sidebar-drawer"]')).toBeVisible()
      await page.locator('[data-testid="sidebar-drawer-overlay"]').click({ position: { x: 380, y: 400 } })
      await expect(page.locator('[data-testid="sidebar-drawer"]')).toHaveCount(0)
    })

    test("★ 会话树再长，输入框也不会被挤走", async ({ page }) => {
      await openNarrowApp(page)
      await openDrawer(page)
      const before = await composerBox(page)
      await growTree(page)
      const after = await composerBox(page)
      expect(before).not.toBeNull()
      expect(after).not.toBeNull()
      // ★ The tree does not move the input box. ★ The drawer is **over** the
      //   chat, not **above** it — and that is the whole difference: a tree
      //   that stands above the chat takes the chat's height with it, and a
      //   person with thirty conversations loses the input box entirely.
      expect(Math.abs(after!.y - before!.y)).toBeLessThan(1)
      expect(after!.y + after!.height).toBeLessThanOrEqual(PHONE.height)
      // ★ The drawer stays inside the screen: the tree scrolls inside it.
      const drawer = await page.locator('[data-testid="sidebar-drawer"]').boundingBox()
      expect(drawer).not.toBeNull()
      expect(drawer!.y).toBeGreaterThanOrEqual(0)
      expect(drawer!.y + drawer!.height).toBeLessThanOrEqual(PHONE.height + 1)
    })

    test("★ 弹层不超出视口", async ({ page }) => {
      await openNarrowApp(page)
      // ★ The settings entry lives in the sidebar, so reaching it on a
      //   phone means opening the drawer first.
      await openDrawer(page)
      await page.locator('[data-testid="settings-entry"]').click()
      const dialog = page.locator('[data-testid="settings-dialog"]')
      await expect(dialog).toBeVisible()
      const box = await dialog.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.y).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(PHONE.width + 1)
      expect(box!.y + box!.height).toBeLessThanOrEqual(PHONE.height + 1)
    })
  })

  test.describe("平板", () => {
    test.use({ viewport: TABLET })

    test("★ 页面不横向滚动", async ({ page }) => {
      await openApp(page)
      expect(await horizontalOverflow(page)).toBe(0)
    })

    test("★ 会话列表和聊天都在窗口里", async ({ page }) => {
      await openApp(page)
      await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
      const sidebar = await page.locator('[data-testid="sidebar"]').boundingBox()
      expect(sidebar).not.toBeNull()
      expect(sidebar!.width).toBeLessThanOrEqual(TABLET.width)
    })

    test("★ 断点边上，侧栏仍然是常驻的一列", async ({ page }) => {
      await openApp(page)
      // ★ 768 is the breakpoint itself, and `min-width: 768px` holds there:
      //   the wide layout is the one that answers, and the drawer is not
      //   the one. ★ A layout that only holds "above" or "below" the
      //   breakpoint is caught here and nowhere else.
      await expect(page.locator('[data-testid="sidebar"]')).toBeVisible()
      await expect(page.locator('[data-testid="sidebar-drawer"]')).toHaveCount(0)
      const sidebar = await page.locator('[data-testid="sidebar"]').boundingBox()
      const composer = await page.locator('[data-testid="composer-textarea"]').boundingBox()
      expect(sidebar).not.toBeNull()
      expect(composer).not.toBeNull()
      expect(composer!.x).toBeGreaterThanOrEqual(sidebar!.x + sidebar!.width - 1)
    })

    test("★ 会话树再长，聊天列也不缩水", async ({ page }) => {
      await openApp(page)
      const before = await composerBox(page)
      await growTree(page)
      const after = await composerBox(page)
      expect(before).not.toBeNull()
      expect(after).not.toBeNull()
      // ★ The docked sidebar is the screen's height, **never** the tree's:
      //   the tree scrolls inside it.
      expect(Math.abs(after!.y - before!.y)).toBeLessThan(1)
      const sidebar = await page.locator('[data-testid="sidebar"]').boundingBox()
      expect(sidebar).not.toBeNull()
      expect(sidebar!.height).toBeLessThanOrEqual(TABLET.height)
    })
  })

  test.describe("桌面", () => {
    test.use({ viewport: DESKTOP })

    test("★ 页面不横向滚动", async ({ page }) => {
      await openApp(page)
      expect(await horizontalOverflow(page)).toBe(0)
    })

    test("★ 侧栏常驻，聊天在它右边", async ({ page }) => {
      await openApp(page)
      const sidebar = await page.locator('[data-testid="sidebar"]').boundingBox()
      const composer = await page.locator('[data-testid="composer-textarea"]').boundingBox()
      expect(sidebar).not.toBeNull()
      expect(composer).not.toBeNull()
      // ★ The chat starts **after** the sidebar: they are side by side, and a
      //   chat that starts under the sidebar is the stacked layout.
      expect(composer!.x).toBeGreaterThanOrEqual(sidebar!.x + sidebar!.width - 1)
    })
  })
})

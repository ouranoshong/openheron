import { expect, test, type Page } from "@playwright/test"

import { resetBackend } from "./fixtures"

/**
 * 配置编辑器的界面用例。
 *
 * ★ 假后端里这一族的**语义**是真的：遮蔽、409 冲突、明文密钥拒绝、
 *   合并失败。打桩成永远成功的话，这里就只能验「界面没崩」。
 *
 * 下面每一条都对应一个具体的失效 —— 或者一类「用户会得到一个说谎的界面」
 * 的失效，那比崩了更难查。
 */

const DIALOG = '[data-testid="settings-dialog"]'
const ENTRY = '[data-testid="settings-entry"]'
const LIST = '[data-testid="settings-file-list"]'
const EDITOR = '[data-testid="config-editor"] .cm-content'
const STATUS = '[data-testid="settings-status"]'
const SAVE = '[data-testid="settings-save"]'

async function openSettings(page: Page): Promise<void> {
  await page.goto("/")
  await page.click(ENTRY)
  await expect(page.locator(DIALOG)).toBeVisible()
  // 懒加载：对话框本身要先到位。
  await expect(page.locator(EDITOR)).toBeVisible()
}

async function selectFile(page: Page, path: string): Promise<void> {
  await page.locator(`${LIST} button`, { hasText: path }).first().click()
}

test.beforeEach(async () => {
  await resetBackend()
})

test.describe("opening", () => {
  test("the entry opens the dialog from the sidebar footer", async ({ page }) => {
    await page.goto("/")
    await expect(page.locator(DIALOG)).toHaveCount(0)
    await page.click(ENTRY)
    await expect(page.locator(DIALOG)).toBeVisible()
  })

  test("the close button does not overlap the title", async ({ page }) => {
    await openSettings(page)
    const dialog = page.locator(DIALOG)
    const close = dialog.getByRole("button", { name: "Close settings" })
    const title = dialog.getByRole("heading", { name: "Settings" })
    await expect(close).toBeVisible()

    const [closeBox, titleBox] = await Promise.all([close.boundingBox(), title.boundingBox()])
    expect(closeBox && titleBox).toBeTruthy()
    if (!closeBox || !titleBox) return

    expect(closeBox.x).toBeGreaterThanOrEqual(titleBox.x + titleBox.width - 1)
  })

  test("Ctrl+, is a no-op when closed and a shortcut when open", async ({ page }) => {
    await page.goto("/")
    await expect(page.locator(DIALOG)).toHaveCount(0)
    await page.keyboard.press("Control+,")
    await expect(page.locator(DIALOG)).toBeVisible()
    await page.keyboard.press("Control+,")
    await expect(page.locator(DIALOG)).toHaveCount(0)
  })

  test("selecting the first file shows its real content", async ({ page }) => {
    await openSettings(page)
    // ★ 不是「编辑器出现了」，是**它装的是这个文件的内容** ——
    //   空编辑器与加载失败的编辑器在 DOM 上长得一样。
    await expect(page.locator(EDITOR)).toContainText("read_file")
  })

  test("CodeMirror renders rather than a plain textarea", async ({ page }) => {
    // ★ 受控组件走错分支会静默退化成 textarea，而那时全部既有
    //   「编辑器」用例仍然会过 —— 因为它们只断言内容，不断言渲染层。
    await openSettings(page)
    await expect(page.locator('[data-testid="config-editor"] .cm-editor')).toHaveCount(1)
    await expect(page.locator('[data-testid="config-editor"] textarea')).toHaveCount(0)
  })

  test("agents/*.md highlights frontmatter separately from the body", async ({ page }) => {
    // ★ yamlFrontmatter 是选 CodeMirror 而非 Monaco 的核心理由：
    //   前后两段走的是**两种**语法。没有它，8 行 YAML 就是正文。
    await openSettings(page)
    await selectFile(page, "agents/main.md")
    await expect(page.locator(EDITOR)).toContainText("model: deepseek/flash")
    await expect(page.locator(EDITOR)).toContainText("body")
  })
})

test.describe("the one config root", () => {
  // ★ 取消继承链之后没有「遮蔽」这回事了 —— 同一层里一个文件名只有一份，
  //   保存它一定生效。界面不该再显示「被覆盖」的警告：那是噪音，
  //   而噪音等于没有警告。
  test("no file claims to be overridden", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await expect(page.getByText(/overridden at the project level/i)).toHaveCount(0)
  })

  test("the list carries no shadowed badge", async ({ page }) => {
    await openSettings(page)
    const row = page.locator(LIST).getByRole("button", { name: /policy\/permissions\.yaml/ }).first()
    await expect(row.getByLabel(/overridden at project level/)).toHaveCount(0)
  })

  test("every file appears exactly once", async ({ page }) => {
    await openSettings(page)
    const names = await page.locator(LIST).getByRole("button").evaluateAll((els) =>
      els.map((e) => (e.textContent ?? "").trim()),
    )
    expect(new Set(names).size).toBe(names.length)
  })
})

test.describe("no level to switch", () => {
  test("there is no level toggle in the dialog", async ({ page }) => {
    // ★ 只有一个配置根 ⇒ 没有「切到哪一级」这回事。
    //   留着那个开关只会让人以为还有第二份配置在生效。
    await openSettings(page)
    await expect(page.locator('[data-testid="level-toggle"]')).toHaveCount(0)
  })
})

test.describe("read-only sections", () => {
  test("server is listed but marked as needing a restart", async ({ page }) => {
    await openSettings(page)
    const row = page.locator(LIST).getByRole("button", { name: /server\/config\.yaml/ }).first()
    await expect(row.getByLabel(/requires a restart/)).toBeVisible()
  })

  test("a non-hot section is not editable", async ({ page }) => {
    // ★ 做成「能保存但不生效」比拒绝更糟 —— 它训练用户不信任界面。
    await openSettings(page)
    await selectFile(page, "server/config.yaml")
    await expect(page.getByText(/not hot-reloaded/i)).toBeVisible()
    await expect(page.locator(SAVE)).toBeDisabled()
  })
})

test.describe("editing and saving", () => {
  test("a clean edit saves and reports that it applied", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("allow: [glob]\n")

    await expect(page.locator(STATUS)).toContainText("Unsaved changes")
    await page.click(SAVE)
    // ★ 「保存成功」与「已生效」必须分开说 —— watcher 关掉时前者为真后者为假。
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  test("a literal api_key is rejected and the draft survives", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    // 塞一段带明文密钥的 llm 内容 —— 服务端 400，界面要显示它。
    await page.keyboard.type("type: openai\napi_key: sk-oops\n")

    // 客户端对 policy 分区判的是「未知键」，所以先看到本地那条。
    await expect(page.locator(STATUS)).toContainText(/api_key|unknown key/i)
    await expect(page.locator(EDITOR)).toContainText("sk-oops")
  })

  test("a broken merge surfaces the server's verdict", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("BROKEN")

    await page.click(SAVE)
    // ★ 跨文件的问题客户端看不见，只有服务端答得出来 —— 所以这一步
    //   不是「优化掉」，是必须问。
    await expect(page.locator(STATUS)).toContainText(/not declared/)
    // 草稿要留着：用户要照着报错改它。
    await expect(page.locator(EDITOR)).toContainText("BROKEN")
  })

  test("saving while hot reload is off says restart is required", async ({ page }) => {
    // ★ 这条路径**只能**从 watch 关闭进来：非热更新的分区（server/）在 v1
    //   里根本不可写，而那是另一条路径（有 read-only 提示、保存按钮禁用）。
    //   所以这里用内容里的标记让假后端回报 applied=false。
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("NO_WATCH\nallow: [ls]\n")

    await page.click(SAVE)
    // 「保存成功」与「已生效」在界面上长得一样，而那个区别是全部要害。
    await expect(page.locator(STATUS)).toContainText(/restart openheron to apply/)
  })
})

test.describe("policy/limits.yaml", () => {
  // ★ policy/ 下有两种文件形态，规则不同 —— 界面必须按**文件名**分派。
  //   只按分区分派的话，limits.yaml 的 agent/retry 会被判成「未知键
  //   allow/ask/deny」，而 permissions.yaml 的规则压根不管限制规则。
  test("it is listed and editable alongside permissions.yaml", async ({ page }) => {
    await openSettings(page)
    await expect(page.locator(LIST)).toContainText("policy/limits.yaml")
    await selectFile(page, "policy/limits.yaml")
    // 内容是限制规则，不是审批档位 —— 断言要看**内容**，不能只看路径。
    await expect(page.locator(EDITOR)).toContainText("max_depth")
  })

  test("a valid limits file saves without a local complaint", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    // ★ 缩进要**显式**敲进去。
    //
    //	编辑器的 YAML 自动缩进是「保持上一行的缩进」，不是「冒号后加一级」：
    //	在「agent:」后回车，光标在第 0 列；在「  max_depth: 1」后回车，
    //	光标在第 2 列。所以「  max_depth: 1」那两格得自己敲，
    //	而要回到第 0 列开下一个分区，得 Shift+Tab 减一级。
    await page.keyboard.type("agent:")
    await page.keyboard.press("Enter")
    await page.keyboard.type("  max_depth: 1")
    await page.keyboard.press("Enter")
    await page.keyboard.press("Shift+Tab")
    await page.keyboard.type("retry:")
    await page.keyboard.press("Enter")
    await page.keyboard.type("  max_retries: 2")

    // ★ 客户端不该把合法的限制规则报成「未知键」—— 那正是分派错了的样子。
    await expect(page.locator(STATUS)).not.toContainText(/unknown key|looks like a new section|unknown section/i)
    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  test("a section indented under another one says what to do", async ({ page }) => {
    // ★ 编辑器的自动缩进会把分区名吞进上一个分区。报错要说清**怎么改**，
    //   而不只是「未知键」—— 否则用户会以为 retry 这个分区不存在。
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("agent:")
    await page.keyboard.press("Enter")
    await page.keyboard.type("  max_depth: 1")
    await page.keyboard.press("Enter")
    // 少了 Shift+Tab ⇒ 「retry:」落在第 2 列，被当成了 agent 的键。
    await page.keyboard.type("retry:")

    await expect(page.locator(STATUS)).toContainText(/looks like a new section/i)
  })

  test("an unknown key in limits.yaml is caught locally", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("agent:\n  max_tokens: 100\n")

    await expect(page.locator(STATUS)).toContainText(/unknown key/i)
  })

  test("the permissions rules do not apply to it", async ({ page }) => {
    // ★ 反向的一条：permissions.yaml 的键在 limits.yaml 里是**未知分区**。
    //   两条规则的边界必须真的存在，而不是共用一套。
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("allow: [ls]\n")

    await expect(page.locator(STATUS)).toContainText(/unknown section/i)
  })
})

test.describe("closing", () => {
  test("closing and reopening discards an unsaved draft", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("allow: [draft-marker]\n")
    await expect(page.locator(EDITOR)).toContainText("draft-marker")

    await page.keyboard.press("Control+,")
    await expect(page.locator(DIALOG)).toHaveCount(0)
    await page.click(ENTRY)
    await expect(page.locator(EDITOR)).not.toContainText("draft-marker")
  })
})
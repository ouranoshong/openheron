import { expect, test, type Page } from "@playwright/test"

import { resetBackend } from "./fixtures"

const DIALOG = '[data-testid="settings-dialog"]'
const ENTRY = '[data-testid="settings-entry"]'
const TAB_FILES = '[data-testid="settings-tab-files"]'
const TAB_SECRETS = '[data-testid="settings-tab-secrets"]'
const PANEL = '[data-testid="secrets-panel"]'
const DETAIL = '[data-testid="secrets-detail"]'
const VALUE = '[data-testid="secrets-value"]'
const SET = '[data-testid="secrets-set"]'
const REMOVE = '[data-testid="secrets-remove"]'
const WARNING = '[data-testid="secrets-warning"]'
const EFFECTIVE = '[data-testid="secrets-effective"]'

const itemOf = (group: string, name: string) => `[data-testid="secrets-item-${group}-${name}"]`

// ★ The two group headings of the panel.
const HEADING = `${PANEL} h3`

// ★ The value that must never appear anywhere in the DOM.
const SECRET = "sk-never-in-the-dom"

async function openSecrets(page: Page): Promise<void> {
  await page.goto("/")
  await page.click(ENTRY)
  await expect(page.locator(DIALOG)).toBeVisible()
  await page.click(TAB_SECRETS)
  await expect(page.locator(PANEL)).toBeVisible()
}

// ★ The same panel, in Chinese: the language is a **preference of the
//   machine**, so it is switched first and the tab is opened afterwards —
//   the panel has to be Chinese because the reader is.
async function openSecretsInChinese(page: Page): Promise<void> {
  await page.goto("/")
  await page.click('[data-testid="locale-toggle"]')
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN")
  await page.click(ENTRY)
  await expect(page.locator(DIALOG)).toBeVisible()
  await page.click(TAB_SECRETS)
  await expect(page.locator(PANEL)).toBeVisible()
}

test.beforeEach(async () => {
  await resetBackend()
})

test.describe("the secrets tab", () => {
  test("it is a tab of the settings dialog, not a branch of the file tree", async ({ page }) => {
    await openSecrets(page)

    // ★ The tree is built from the paths the server sends, and a secret has
    //   no path — so the tree is gone while the secrets are shown.
    await expect(page.locator('[data-testid="settings-file-list"]')).toHaveCount(0)
    await expect(page.locator(TAB_SECRETS)).toHaveAttribute("aria-selected", "true")

    await page.click(TAB_FILES)
    await expect(page.locator('[data-testid="settings-file-list"]')).toBeVisible()
  })

  test("both namespaces are listed", async ({ page }) => {
    await openSecrets(page)

    await expect(page.locator(PANEL)).toContainText("Providers")
    await expect(page.locator(PANEL)).toContainText("MCP servers")
    await expect(page.locator(itemOf("llm", "deepseek"))).toBeVisible()
    await expect(page.locator(itemOf("mcp", "filesystem"))).toBeVisible()
  })

  test("a key that is not there says so", async ({ page }) => {
    await openSecrets(page)
    await page.locator(itemOf("llm", "deepseek")).click()

    await expect(page.locator(DETAIL)).toContainText("not set")
    await expect(page.locator(EFFECTIVE)).toContainText("no key is in effect")
  })

  test("storing a key shows that it is stored and never shows the value", async ({ page }) => {
    await openSecrets(page)
    await page.locator(itemOf("llm", "deepseek")).click()
    await page.locator(VALUE).fill(SECRET)
    await page.click(SET)

    await expect(page.locator(itemOf("llm", "deepseek"))).toContainText("stored")

    // ★ The strongest assertion there is: the value is not in the document.
    //   Not in the text, not in an attribute, not in a hidden node.
    const html = await page.locator("body").innerHTML()
    expect(html).not.toContain(SECRET)
  })

  test("the input is emptied after the write", async ({ page }) => {
    await openSecrets(page)
    await page.locator(itemOf("llm", "deepseek")).click()
    await page.locator(VALUE).fill(SECRET)
    await page.click(SET)

    await expect(page.locator(VALUE)).toHaveValue("")
  })

  test("the environment overriding a stored key is said out loud", async ({ page }) => {
    await openSecrets(page)
    await page.locator(itemOf("mcp", "filesystem")).click()

    await expect(page.locator(WARNING)).toBeVisible()
    await expect(page.locator(WARNING)).toContainText("MCP_FILESYSTEM_TOKEN")
    await expect(page.locator(WARNING)).toContainText("wins")
  })

  test("removing one entry leaves the other group alone", async ({ page }) => {
    await openSecrets(page)
    await page.locator(itemOf("llm", "deepseek")).click()
    await page.locator(VALUE).fill(SECRET)
    await page.click(SET)
    await expect(page.locator(itemOf("llm", "deepseek"))).toContainText("stored")

    await page.locator(REMOVE).click()
    await expect(page.locator(itemOf("llm", "deepseek"))).toContainText("not set")
    await expect(page.locator(itemOf("mcp", "filesystem"))).toContainText("stored")
  })

  test("removing a group names the count and the scope, and touches only that group", async ({ page }) => {
    await openSecrets(page)

    await page.locator(itemOf("llm", "deepseek")).click()
    await page.locator(VALUE).fill(SECRET)
    await page.click(SET)
    await expect(page.locator(itemOf("llm", "deepseek"))).toContainText("stored")

    await page.locator('[data-testid="secrets-remove-group-llm"]').click()

    // ★ The confirmation says how many and what: the two groups are separate
    //   namespaces, and "remove everything" has to be a scoped operation.
    const dialog = page.locator('[data-testid="secrets-remove-dialog"]')
    await expect(dialog).toContainText("Remove 1 provider secret")
    await expect(dialog).toContainText("MCP server secrets are untouched")

    await page.click('[data-testid="secrets-remove-confirm"]')

    await expect(page.locator(itemOf("llm", "deepseek"))).toContainText("not set")
    // ★ The other group is untouched.
    await expect(page.locator(itemOf("mcp", "filesystem"))).toContainText("stored")
  })

  test("the confirmation can be cancelled", async ({ page }) => {
    await openSecrets(page)
    await page.locator(itemOf("llm", "deepseek")).click()
    await page.locator(VALUE).fill(SECRET)
    await page.click(SET)

    await page.locator('[data-testid="secrets-remove-group-llm"]').click()
    await page.click('[data-testid="secrets-remove-cancel"]')

    await expect(page.locator('[data-testid="secrets-remove-dialog"]')).toHaveCount(0)
    await expect(page.locator(itemOf("llm", "deepseek"))).toContainText("stored")
  })
})

// ★ The language of the interface: the panel says the reader's words, and
//   the server's English never leaks into a Chinese window.
test.describe("the secrets tab in Chinese", () => {
  test("★ 分组标题是「服务商」「连接器」，没有英文组名", async ({ page }) => {
    await openSecretsInChinese(page)

    await expect(page.locator(HEADING)).toHaveText(["服务商", "连接器"])

    const text = await page.locator(PANEL).innerText()
    expect(text).not.toMatch(/providers/i)
    expect(text).not.toMatch(/mcp servers/i)
  })

  test("★ 「移除全部 {group}」按钮是中文", async ({ page }) => {
    await openSecretsInChinese(page)

    await expect(page.locator('[data-testid="secrets-remove-group-llm"]')).toHaveText("移除全部 服务商")
    await expect(page.locator('[data-testid="secrets-remove-group-mcp"]')).toHaveText("移除全部 连接器")
  })

  test("★ 确认弹窗是中文：分组名、范围词、数量都在", async ({ page }) => {
    await openSecretsInChinese(page)

    await page.locator('[data-testid="secrets-remove-group-llm"]').click()
    const dialog = page.locator('[data-testid="secrets-remove-dialog"]')
    await expect(dialog).toBeVisible()

    // ★ The count and the scope: the two groups are separate namespaces.
    await expect(dialog).toContainText("移除 1 个 服务商密钥？")
    await expect(dialog).toContainText("仅 服务商 分组受影响")
    await expect(dialog).toContainText("连接器密钥 保持不变")

    const text = await dialog.innerText()
    expect(text).not.toMatch(/providers/i)
    expect(text).not.toMatch(/mcp server/i)
    expect(text).not.toMatch(/provider secret/i)
  })
})

// ★ The failures the panel shows: the server's **code** decides the
//   sentence, and a code nobody knows keeps the server's own words.
test.describe("the failures of the secrets tab", () => {
  const LIST = "**/v2/secrets"

  test("★ 已知错误码：中文句，不带服务端英文原句", async ({ page }) => {
    await page.route(LIST, (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: "the keyring is not available",
          code: "secret_unavailable",
          retryable: true,
        }),
      }),
    )
    await openSecretsInChinese(page)

    const err = page.locator('[data-testid="secrets-error"]')
    await expect(err).toBeVisible()
    await expect(err).toHaveText("本机密钥环不可用。")
  })

  test("★ 未映射的错误码：原样显示服务端英文原句", async ({ page }) => {
    await page.route(LIST, (route) =>
      route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({
          error: "The vault is on fire.",
          code: "vault_on_fire",
          retryable: false,
        }),
      }),
    )
    await openSecretsInChinese(page)

    const err = page.locator('[data-testid="secrets-error"]')
    await expect(err).toBeVisible()
    await expect(err).toHaveText("The vault is on fire.")
  })

  test("★ 连不上服务端：本地化的通用句，没有裸英文异常", async ({ page }) => {
    // ★ The request never reaches the server: the browser's own failure.
    await page.route(LIST, (route) => route.abort("failed"))
    await openSecretsInChinese(page)

    const err = page.locator('[data-testid="secrets-error"]')
    await expect(err).toBeVisible()
    await expect(err).toHaveText("无法连接到服务端。")
    const text = await err.innerText()
    expect(text).not.toContain("Failed to fetch")
  })

  test("★ 响应体不是 JSON：本地化的「出了点问题。」", async ({ page }) => {
    // ★ The envelope could not be parsed: there is no code to speak of,
    //   so the interface says the generic sentence of its own language.
    await page.route(LIST, (route) =>
      route.fulfill({ status: 500, contentType: "text/plain", body: "not json at all" }),
    )
    await openSecretsInChinese(page)

    const err = page.locator('[data-testid="secrets-error"]')
    await expect(err).toBeVisible()
    await expect(err).toHaveText("出了点问题。")
  })
})

test.describe("the secrets tab in English", () => {
  test("★ MCP servers 首字母大写", async ({ page }) => {
    await openSecrets(page)

    await expect(page.locator(HEADING)).toHaveText(["Providers", "MCP servers"])

    // ★ "mcp servers" is the defect: the name is never case-folded.
    const text = await page.locator(PANEL).innerText()
    expect(text).not.toContain("mcp servers")
    expect(text).not.toContain("mcp Servers")
  })

  test("★ 「Remove all {group}」按钮用分组名，不小写化", async ({ page }) => {
    await openSecrets(page)

    await expect(page.locator('[data-testid="secrets-remove-group-llm"]')).toHaveText(
      "Remove all Providers",
    )
    await expect(page.locator('[data-testid="secrets-remove-group-mcp"]')).toHaveText(
      "Remove all MCP servers",
    )
  })
})

test.describe("the language of the secrets tab", () => {
  test("★ 切语言：分组标题当场变，不重开页签", async ({ page }) => {
    await openSecrets(page)
    await expect(page.locator(HEADING)).toHaveText(["Providers", "MCP servers"])

    // ★ The settings dialog covers the whole window, so the header's
    //   language switch sits behind it — the click is dispatched on the
    //   element itself, which is what the user's click does.
    await page.evaluate(() => {
      document.querySelector<HTMLButtonElement>('[data-testid="locale-toggle"]')?.click()
    })
    await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN")

    // ★ The tab was never closed and never reopened: the headings changed
    //   where they stand.
    await expect(page.locator(PANEL)).toBeVisible()
    await expect(page.locator(HEADING)).toHaveText(["服务商", "连接器"])
    await expect(page.locator('[data-testid="secrets-remove-group-llm"]')).toHaveText("移除全部 服务商")
  })
})

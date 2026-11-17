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

// ★ The value that must never appear anywhere in the DOM.
const SECRET = "sk-never-in-the-dom"

async function openSecrets(page: Page): Promise<void> {
  await page.goto("/")
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

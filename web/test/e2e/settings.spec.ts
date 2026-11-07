import { expect, test, type Page } from "@playwright/test"

import { resetBackend } from "./fixtures"

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
    await expect(page.locator(EDITOR)).toContainText("read_file")
  })

  test("CodeMirror renders rather than a plain textarea", async ({ page }) => {
    await openSettings(page)
    await expect(page.locator('[data-testid="config-editor"] .cm-editor')).toHaveCount(1)
    await expect(page.locator('[data-testid="config-editor"] textarea')).toHaveCount(0)
  })

  test("agents/*.md highlights frontmatter separately from the body", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "agents/main.md")
    await expect(page.locator(EDITOR)).toContainText("model: deepseek/flash")
    await expect(page.locator(EDITOR)).toContainText("body")
  })
})

test.describe("the one config root", () => {
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
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  test("a literal api_key is rejected and the draft survives", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("type: openai\napi_key: sk-oops\n")

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
    await expect(page.locator(STATUS)).toContainText(/not declared/)
    await expect(page.locator(EDITOR)).toContainText("BROKEN")
  })

  test("saving while hot reload is off says restart is required", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/permissions.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("NO_WATCH\nallow: [ls]\n")

    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/restart openheron to apply/)
  })
})

test.describe("policy/limits.yaml", () => {
  test("it is listed and editable alongside permissions.yaml", async ({ page }) => {
    await openSettings(page)
    await expect(page.locator(LIST)).toContainText("policy/limits.yaml")
    await selectFile(page, "policy/limits.yaml")
    await expect(page.locator(EDITOR)).toContainText("max_depth")
  })

  test("a valid limits file saves without a local complaint", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("agent:")
    await page.keyboard.press("Enter")
    await page.keyboard.type("  max_depth: 1")
    await page.keyboard.press("Enter")
    await page.keyboard.press("Shift+Tab")
    await page.keyboard.type("retry:")
    await page.keyboard.press("Enter")
    await page.keyboard.type("  max_retries: 2")

    await expect(page.locator(STATUS)).not.toContainText(/unknown key|looks like a new section|unknown section/i)
    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  test("a section indented under another one says what to do", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("agent:")
    await page.keyboard.press("Enter")
    await page.keyboard.type("  max_depth: 1")
    await page.keyboard.press("Enter")
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
    await openSettings(page)
    await selectFile(page, "policy/limits.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("allow: [ls]\n")

    await expect(page.locator(STATUS)).toContainText(/unknown section/i)
  })
})

test.describe("the new file dialog", () => {
  const NEW_FILE = '[data-testid="settings-new-file"]'
  const NEW_DIALOG = '[data-testid="new-file-dialog"]'
  const NEW_NAME = '[data-testid="new-file-name"]'
  const NEW_CONTENT = '[data-testid="new-file-content"]'
  const NEW_CREATE = '[data-testid="new-file-create"]'

  async function openNewFile(page: Page): Promise<void> {
    await openSettings(page)
    await page.click(NEW_FILE)
    await expect(page.locator(NEW_DIALOG)).toBeVisible()
  }

  async function nameToolsFile(page: Page, file: string): Promise<void> {
    await page.getByRole("button", { name: "Tools", exact: true }).click()
    await page.locator(NEW_NAME).fill(file)
  }

  // ★ The skeleton follows the FILE, not the section. The tools/ section
  //   template is nothing but comments, and a file built from it has no YAML
  //   document at all — the server answers that with a bare "EOF". This is
  //   the shape the bug lived in, so it gets a spec of its own.
  test("typing the file name picks that file's template, not the section's", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "http.yaml")

    await expect(page.locator(NEW_CONTENT)).toHaveValue(/methods: \[get\]/)
    await expect(page.locator(NEW_CONTENT)).not.toHaveValue(/one file = one tool/)
  })

  test("a tools file is created with its own template and lands in the editor", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "http.yaml")
    await page.click(NEW_CREATE)

    await expect(page.locator(EDITOR)).toContainText("methods: [get]")
    await expect(page.locator(STATUS)).not.toContainText(/EOF/)
  })

  // ★ The MCP partition is editable and hot, and the **manifest** says so.
  //   A client that keeps its own list of editable sections is a second
  //   truth — and the day a partition is added, the client says it does
  //   not exist. That is exactly the bug this spec pins down.
  test("a file under mcps/ can be created, and the directory is created with it", async ({ page }) => {
    await openNewFile(page)
    await page.getByRole("button", { name: "MCP servers", exact: true }).click()
    await page.locator(NEW_NAME).fill("github")

    // ★ The path is the server's name: the file's name IS the server's name.
    await expect(page.locator(NEW_NAME).locator("xpath=..")).toContainText("mcps/github.yaml")
    await expect(page.locator(NEW_CREATE)).toBeEnabled()

    await page.click(NEW_CREATE)
    await expect(page.locator(EDITOR)).toContainText("transport: stdio")
  })

  test("a server name that could not be read back is refused", async ({ page }) => {
    await openNewFile(page)
    await page.getByRole("button", { name: "MCP servers", exact: true }).click()
    await page.locator(NEW_NAME).fill("github__x")

    await expect(page.locator(NEW_DIALOG)).toContainText(/could not be read back/i)
    await expect(page.locator(NEW_CREATE)).toBeDisabled()
  })

  test("a tools file with no YAML content is refused before the round trip", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "http.yaml")
    await page.click(NEW_CREATE)
    await expect(page.locator(EDITOR)).toContainText("methods: [get]")

    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("# only a comment\n")

    await expect(page.locator(STATUS)).toContainText(/no YAML content/i)
    // ★ The save is blocked **locally**: the button is disabled, so the
    //   request never leaves the browser. Clicking it would wait forever.
    await expect(page.locator(SAVE)).toBeDisabled()
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
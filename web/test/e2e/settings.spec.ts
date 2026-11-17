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

// ★ Folders start closed, so a test that wants a row must open the way in.
//   The tree's own rule is "everything closed except the selection", and a
//   test that reaches past it would be asserting a tree no reader sees.
const toggleOf = (path: string) => `[data-testid="settings-tree-toggle-${path}"]`
const labelOf = (path: string) => `[data-testid="settings-tree-label-${path}"]`

function ancestorsOf(path: string): string[] {
  const segs = path.split("/")
  return segs.slice(0, -1).map((_, i) => segs.slice(0, i + 1).join("/"))
}

async function expandTo(page: Page, path: string): Promise<void> {
  for (const folder of ancestorsOf(path)) {
    const toggle = page.locator(toggleOf(folder))
    if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click()
  }
}

async function expandAll(page: Page): Promise<void> {
  const closed = page.locator(`${LIST} [data-testid^="settings-tree-toggle-"][aria-expanded="false"]`)
  // Opening one folder can reveal another; click until none is left.
  for (let round = 0; round < 20 && (await closed.count()) > 0; round++) {
    await closed.first().click()
  }
}

async function selectFile(page: Page, path: string): Promise<void> {
  // ★ The tree shows a file's own name under its folder, so the row's
  //   accessible name is what carries the path.
  await expandTo(page, path)
  await page.locator(LIST).getByRole("button", { name: path, exact: true }).click()
}

// ★ The tree's top-level folders carry the same words as the new file
//   dialog's section picker — "Skills", "Tools", "MCP servers" — and both are
//   in the DOM at once. The picker is the one inside the new file dialog.
async function pickSection(page: Page, label: string): Promise<void> {
  await page
    .locator('[data-testid="new-file-dialog"]')
    .getByRole("button", { name: label, exact: true })
    .click()
}

/**
 * How many tokens in the editor carry a color of their own.
 *
 * ★ The editor's base color is the app's foreground; a syntax-highlighted
 *   token is a span whose computed color differs from it. Flat text has
 *   zero. This is how "the file is colored" is checked without pinning the
 *   exact palette — the palette is a theme decision, the coloring is not.
 */
async function coloredTokens(page: Page): Promise<number> {
  const base = await page.locator(EDITOR).evaluate((el) => getComputedStyle(el).color)
  return page.locator(`${EDITOR} span`).evaluateAll(
    (els, b) => els.filter((e) => getComputedStyle(e).color !== (b as string)).length,
    base,
  )
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
    // ★ The first **editable** file — llm/deepseek.yaml, because the real
    //   server's partition table starts with llm/. The content is the file's
    //   own, not a template: the point is that the editor shows what is on
    //   disk.
    await expect(page.locator(EDITOR)).toContainText("base_url")
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

  // ★ The editor used to color the file by the **section name**, and it knew
  //   only two names. tools/ and mcps/ ship YAML exactly like llm/ does, and
  //   they came up with no language at all — flat text in a column where the
  //   neighboring file was colored. The language follows the file now.
  test("a file under tools/ is colored as YAML", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/http.yaml")
    await expect(page.locator(EDITOR)).toContainText("methods")
    expect(await coloredTokens(page)).toBeGreaterThan(0)
  })

  test("a file under mcps/ is colored as YAML", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "mcps/filesystem.yaml")
    await expect(page.locator(EDITOR)).toContainText("transport")
    expect(await coloredTokens(page)).toBeGreaterThan(0)
  })

  // ★ The configuration tools have a file of their own now, and it is in
  //   every tree — so the editor must open it, color it as YAML, and save
  //   it, the same way it treats every other tools/ file.
  test("the configuration tools' own file opens, is colored and saves", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/config.yaml")
    await expect(page.locator(EDITOR)).toContainText("max_read_bytes")
    expect(await coloredTokens(page)).toBeGreaterThan(0)

    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("allows: [config_get]\nmax_read_bytes: 4096\n")

    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  // ★ The editor used to paint itself white: the library's default theme
  //   does, and its rule sat after ours. In the dark theme that was white
  //   text on a white background.
  test("★ 配置编辑器跟着主题走", async ({ page }) => {
    await page.goto("/")
    await page.click(ENTRY)
    await expect(page.locator(EDITOR)).toBeVisible()
    const background = () =>
      page.locator('[data-testid="config-editor"] .cm-editor').evaluate((el) =>
        getComputedStyle(el).backgroundColor,
      )

    const light = await background()
    await page.getByRole("button", { name: "Close settings" }).click()
    await page.locator('[data-testid="theme-toggle"]').click()
    await page.click(ENTRY)
    await expect(page.locator(EDITOR)).toBeVisible()

    expect(await background(), "the editor should follow the theme").not.toBe(light)
  })
})

test.describe("the one config root", () => {
  test("no file claims to be overridden", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/http.yaml")
    await expect(page.getByText(/overridden at the project level/i)).toHaveCount(0)
  })

  test("the list carries no shadowed badge", async ({ page }) => {
    await openSettings(page)
    await expandTo(page, "tools/http.yaml")
    const row = page.locator(LIST).getByRole("button", { name: /tools\/http\.yaml/ }).first()
    await expect(row.getByLabel(/overridden at project level/)).toHaveCount(0)
  })

  test("every file appears exactly once", async ({ page }) => {
    await openSettings(page)
    await expandAll(page)
    // Only file rows: a folder's accessible name comes from its label, a
    // file's from the whole path (`aria-label`).
    const paths = await page.locator(`${LIST} button[aria-label]`).evaluateAll((els) =>
      els.map((e) => e.getAttribute("aria-label") ?? ""),
    )
    expect(paths.length).toBeGreaterThan(0)
    expect(new Set(paths).size).toBe(paths.length)
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
    await expandTo(page, "server/config.yaml")
    const row = page.locator(LIST).getByRole("button", { name: /server\/config\.yaml/ }).first()
    await expect(row.getByLabel(/requires a restart/)).toBeVisible()
  })

  // ★ Readable, not writable — two different questions, and the editor has to
  //   answer both in the same screen. It used to answer neither: the read was
  //   refused with a message about restarts, so the file simply never opened
  //   and Save was disabled because the draft was empty.
  test("a non-hot section opens, and is a view rather than a form", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "server/config.yaml")

    await expect(page.getByText(/not hot-reloaded/i)).toBeVisible()
    // ★ The read path is open: the content is the file's own. Before the read
    //   gate was split, this file never loaded at all, and Save was disabled
    //   because the draft was empty — the right answer for the wrong reason.
    await expect(page.locator(EDITOR)).toContainText("listen_addr")

    // ★ And the write path is closed, in the editor itself: not a disabled
    //   button the user can wonder about, but a document they cannot type
    //   into. Two protections, because the one that matters is the server's.
    await expect(page.locator(EDITOR)).toHaveAttribute("contenteditable", "false")
    await expect(page.locator(SAVE)).toBeDisabled()
  })
})

test.describe("editing and saving", () => {
  test("a clean edit saves and reports that it applied", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/http.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("methods: [get]\nallows: [glob]\n")

    await expect(page.locator(STATUS)).toContainText("Unsaved changes")
    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  test("a literal api_key is rejected and the draft survives", async ({ page }) => {
    await openSettings(page)
    // ★ `api_key` belongs to **llm/** — that is the partition that declares
    //   it, so that is where the rule applies. A tools file has no api_key,
    //   and a file that carries one is not a file with a bad api_key.
    await selectFile(page, "llm/deepseek.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("type: openai\nbase_url: https://x\napi_key: sk-oops\nmodels:\n  - id: flash\n")

    await expect(page.locator(STATUS)).toContainText(/api_key/i)
    await expect(page.locator(EDITOR)).toContainText("sk-oops")
  })

  test("a broken merge surfaces the server's verdict", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/http.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("BROKEN")

    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/not declared/)
    await expect(page.locator(EDITOR)).toContainText("BROKEN")
  })

  test("saving while hot reload is off says restart is required", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/http.yaml")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("NO_WATCH\nmethods: [get]\n")

    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/restart openheron to apply/)
  })
})

// ★ A problem in another file is **that** file's problem. The editor used
//   to put it on the file being edited: a user editing agents/main.md saw
//   the model of agents/agent-expert.md on their screen, and there was
//   nothing they could do about it from there.
test.describe("another file's problems", () => {
  async function breakTheTree(page: Page): Promise<void> {
    await openSettings(page)
    await selectFile(page, "agents/main.md")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("---\nmodel: deepseek/gone\n---\nbody\n")
  }

  test("an issue about another file is shown under that file's name", async ({ page }) => {
    await breakTheTree(page)
    const status = page.locator(STATUS)
    await expect(status).toContainText("agents/agent-expert.md")
    await expect(status).toContainText("mcps/memory.yaml")
  })

  test("the other file's problem is a link to that file", async ({ page }) => {
    await breakTheTree(page)
    await page.locator(STATUS).getByRole("button", { name: /agents\/agent-expert\.md/ }).click()

    await expect(page.locator(DIALOG)).toContainText("agents/agent-expert.md")
    await expect(page.locator(EDITOR)).toContainText("expert body")
  })
})

// ★ **An agent whose model is not declared is left out of the tree.** The
//   tree still loads, the reason is said out loud, and the problem is on
//   that agent's own file — as a **hint**, because nothing is broken: the
//   tree is short one worker, and a hint is what says so.
test.describe("an agent left out of the tree", () => {
  test("its reason is a hint on its own file, and the save is not blocked", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "agents/agent-expert.md")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("---\nmodel: openai/gpt-4o-mini\ndescription: expert v2\n---\nexpert body\n")

    const status = page.locator(STATUS)
    await expect(status).toContainText("left out")
    // ★ A hint does not block: the tree loads without that agent.
    await expect(page.locator(SAVE)).toBeEnabled()
  })

  // ★ **The gate.** A change that would take a **working** agent out of the
  //   tree is refused — and the refusal is on the file being edited, because
  //   that is the change being made.
  test("a change that takes a working agent out is refused on that file", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "agents/main.md")
    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("---\nmodel: deepseek/gone\n---\nbody\n")

    const status = page.locator(STATUS)
    await expect(status).toContainText(/would leave the agent "main" out of the tree/)
    await expect(page.locator(SAVE)).toBeDisabled()
  })
})

// ★ The list is a **file tree**, not a flat list of paths. Six partitions and
//   a folder per skill read as a tree in every file browser the user already
//   uses; a list of `skills/code-review/SKILL.md` strings does not.
//
// ★ Folders open **closed** — six rows instead of a scroll through every
//   skill — with one exception: the ancestors of the file the dialog opened
//   on, so the selection is never hidden behind a closed folder.
test.describe("the file list is a tree", () => {
  test("folders start closed", async ({ page }) => {
    await openSettings(page)

    await expect(page.locator(toggleOf("skills"))).toHaveAttribute("aria-expanded", "false")
    await expect(page.locator(toggleOf("tools"))).toHaveAttribute("aria-expanded", "false")
    // Closed means "not rendered", not "visually hidden": the row is absent
    // from the document, so a screen reader does not read past a closed folder.
    await expect(page.locator('[data-testid="settings-tree-folder-skills/code-review"]')).toHaveCount(0)
    await expect(
      page.locator(LIST).getByRole("button", { name: "skills/code-review/SKILL.md" }),
    ).toHaveCount(0)
  })

  test("the file the dialog opened on is visible", async ({ page }) => {
    await openSettings(page)
    // `llm/deepseek.yaml` is the first editable file, and its folder is
    // opened for it — otherwise the dialog opens on a selection it hides.
    await expect(page.locator(toggleOf("llm"))).toHaveAttribute("aria-expanded", "true")
    await expect(
      page.locator(LIST).getByRole("button", { name: "llm/deepseek.yaml", exact: true }),
    ).toBeVisible()
  })

  test("a folder opens and closes on click", async ({ page }) => {
    await openSettings(page)
    const skills = page.locator(toggleOf("skills"))
    const nested = page.locator('[data-testid="settings-tree-folder-skills/code-review"]')

    await skills.click()
    await expect(skills).toHaveAttribute("aria-expanded", "true")
    // The folder inside it is a row of its own, and it is still closed: a
    // folder opens one level, not the whole way down.
    await expect(nested).toBeVisible()
    await expect(page.locator(toggleOf("skills/code-review"))).toHaveAttribute(
      "aria-expanded",
      "false",
    )

    await skills.click()
    await expect(skills).toHaveAttribute("aria-expanded", "false")
    await expect(nested).toHaveCount(0)
  })

  test("each partition is a folder, and its files sit under it", async ({ page }) => {
    await openSettings(page)
    await expandTo(page, "agents/main.md")

    const agents = page.locator('[data-testid="settings-tree-folder-agents"]')
    await expect(agents).toBeVisible()
    await expect(agents.getByRole("button", { name: "agents/main.md" })).toBeVisible()
  })

  test("a skill is a folder of its own, two levels deep", async ({ page }) => {
    await openSettings(page)
    await expandTo(page, "skills/code-review/SKILL.md")

    const folder = page.locator('[data-testid="settings-tree-folder-skills/code-review"]')
    await expect(folder).toBeVisible()
    await expect(
      folder.getByRole("button", { name: "skills/code-review/SKILL.md" }),
    ).toBeVisible()
  })

  test("a row is indented under its folder", async ({ page }) => {
    await openSettings(page)
    await expandTo(page, "agents/main.md")
    // ★ Where the **text** starts, not where the box starts: every row is a
    //   full-width block, so their boxes share a left edge whatever the
    //   indentation is. The indent is inside them.
    const textLeft = (sel: string) =>
      page.locator(sel).evaluate((el) => {
        const range = document.createRange()
        range.selectNodeContents(el)
        return range.getBoundingClientRect().left
      })

    const folder = await textLeft(labelOf("agents"))
    const row = await textLeft(labelOf("agents/main.md"))
    expect(folder).toBeGreaterThan(0)
    expect(row).toBeGreaterThan(folder)
  })
})

test.describe("a skill is editable", () => {
  test("it opens, and what is typed is what the server receives", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "skills/code-review/SKILL.md")
    await expect(page.locator(EDITOR)).toContainText("reviews a diff")

    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("---\ndescription: reviews harder\n---\nbody\n")
    await expect(page.locator(STATUS)).toContainText("Unsaved changes")
    await page.click(SAVE)
    await expect(page.locator(STATUS)).toContainText(/Saved .* applied/)
  })

  // ★ The name is the **folder**, and the file inside it is always SKILL.md.
  //   Asking for a file name would invite `skills/x/README.md` — a file the
  //   server refuses and the runtime would never read.
  test("the new file dialog asks for a skill name and shows the folder", async ({ page }) => {
    await openSettings(page)
    await page.click('[data-testid="settings-new-file"]')
    await pickSection(page, "Skills")
    await page.locator('[data-testid="new-file-name"]').fill("spec-check")

    await expect(page.getByText("Skill name (its folder)")).toBeVisible()
    await expect(page.getByText("skills/spec-check/SKILL.md")).toBeVisible()
  })

  test("a skill name the server cannot read back keeps the dialog open", async ({ page }) => {
    await openSettings(page)
    await page.click('[data-testid="settings-new-file"]')
    await pickSection(page, "Skills")
    await page.locator('[data-testid="new-file-name"]').fill("Code Review")
    await page.click('[data-testid="new-file-create"]')

    await expect(page.locator('[data-testid="new-file-dialog"]')).toBeVisible()
    await expect(page.locator('[data-testid="new-file-refusal"]')).toContainText("skill name")
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
    await pickSection(page, "Tools")
    await page.locator(NEW_NAME).fill(file)
  }

  // ★ At 32rem a YAML skeleton folded every key onto two lines — the editor
  //   was a column that fought the file it was meant to show. The dialog is
  //   three-quarter-width now, and it scrolls inside the window instead of
  //   growing past it.
  test("the new file dialog is wide enough for a YAML skeleton", async ({ page }) => {
    await openNewFile(page)
    const box = await page.locator(NEW_DIALOG).boundingBox()
    expect(box).toBeTruthy()
    expect(box!.width).toBeGreaterThan(600)

    const overflow = await page.locator(NEW_DIALOG).evaluate((el) => getComputedStyle(el).overflowY)
    expect(overflow).toBe("auto")
  })

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
    await pickSection(page, "MCP servers")
    await page.locator(NEW_NAME).fill("github")

    // ★ The path is the server's name: the file's name IS the server's name.
    await expect(page.locator(NEW_NAME).locator("xpath=..")).toContainText("mcps/github.yaml")
    await expect(page.locator(NEW_CREATE)).toBeEnabled()

    await page.click(NEW_CREATE)
    await expect(page.locator(EDITOR)).toContainText("transport: stdio")
  })

  // ★ The client keeps no copy of the path rules any more: the refusal is
  //   `spec.ValidatePath`'s, and the dialog shows the server's sentence and
  //   **stays open** — closing it on a refused create sends the user back to a
  //   list that does not contain the file, with the reason behind the dialog.
  test("a server name that could not be read back is refused by the server, and the dialog stays open", async ({ page }) => {
    await openNewFile(page)
    await pickSection(page, "MCP servers")
    await page.locator(NEW_NAME).fill("github__x")
    await page.click(NEW_CREATE)

    await expect(page.locator(NEW_DIALOG)).toBeVisible()
    await expect(page.locator(NEW_DIALOG)).toContainText(/server name must match/i)
    await expect(page.locator(NEW_NAME)).toHaveValue("github__x")
  })

  // ★ The filesystem tools are mounted regardless, so their file is a **tuner**:
  //   it can only name exceptions. It is the one tools file whose skeleton must
  //   not look like a switch — a user who wrote "enabled: false" would be
  //   writing a key the loader rejects.
  test("the filesystem tool file is a tuner, and its skeleton says so", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "filesystem.yaml")

    await expect(page.locator(NEW_CONTENT)).toHaveValue(/allows: \[\]/)
    await expect(page.locator(NEW_CONTENT)).not.toHaveValue(/enabled/)
  })

  test("a filesystem file is created and lands in the editor", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "filesystem.yaml")
    await page.click(NEW_CREATE)

    await expect(page.locator(EDITOR)).toContainText("allows")
    await expect(page.locator(STATUS)).not.toContainText(/only accepts/i)
  })

  // ★ Validation is the server's answer now, so this is a round trip: the
  //   editor asks after the pause, the loader's own sentence comes back, and
  //   the save is blocked on it. The client no longer has a rule of its own to
  //   fire first.
  test("a tools file with no YAML content is refused by the server", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "http.yaml")
    await page.click(NEW_CREATE)
    await expect(page.locator(EDITOR)).toContainText("methods: [get]")

    await page.locator(EDITOR).click()
    await page.keyboard.press("Control+a")
    await page.keyboard.type("# only a comment\n")

    await expect(page.locator(STATUS)).toContainText(/file is empty/i)
    await expect(page.locator(SAVE)).toBeDisabled()
  })

  // ★ A create the server refuses keeps the dialog open with the draft, and
  //   says why in the dialog — not in the status line behind it.
  test("a tools file name the server does not accept keeps the dialog open", async ({ page }) => {
    await openNewFile(page)
    await nameToolsFile(page, "whatever.yaml")
    await page.click(NEW_CREATE)

    await expect(page.locator(NEW_DIALOG)).toBeVisible()
    await expect(page.locator(NEW_DIALOG)).toContainText(/only accepts/i)
  })
})

test.describe("closing", () => {
  test("closing and reopening discards an unsaved draft", async ({ page }) => {
    await openSettings(page)
    await selectFile(page, "tools/http.yaml")
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
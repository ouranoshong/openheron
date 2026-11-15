import { expect, test } from "@playwright/test"

import {
  FAKE_URL,
  MARKER_A,
  openApp,
  resetBackend,
  sendMessage,
  setProjectsMode,
} from "./fixtures"

// ★ The sidebar groups the conversations by the project they belong to.
//
// ★ The grouping is the **server's** answer: the client reads the project
//   table and the scope, and it draws what it was given. ★ These specs pin
//   the three modes — no projects, many projects, one project — because a
//   sidebar that shows a conversation in the wrong group is not a cosmetic
//   mistake: it says the work happens in a directory it does not happen in.

const GROUP = '[data-testid="session-group"]'
const GROUP_TITLE = '[data-testid="session-group-title"]'
const THREAD_ITEM = '[data-testid="thread-item"]'
const PICKER = '[data-testid="project-picker"]'
const PROJECTS_BUTTON = '[data-testid="projects-button"]'
const PROJECTS_DIALOG = '[data-testid="projects-dialog"]'
const PROJECTS_EMPTY = '[data-testid="projects-empty"]'
const PROJECT_ROW = '[data-testid="project-row"]'
const PROJECT_NAME = '[data-testid="project-name"]'
const PROJECT_NAME_INPUT = '[data-testid="project-name-input"]'
const PROJECT_ROOT_INPUT = '[data-testid="project-root-input"]'
const PROJECT_CREATE = '[data-testid="project-create"]'
const PROJECT_RENAME_INPUT = '[data-testid="project-rename-input"]'
const PROJECT_REFUSAL = '[data-testid="project-refusal"]'

interface FakeThreadMeta {
  threadId: string
  title: string
  projectId: string
}

async function fakeThreads(): Promise<FakeThreadMeta[]> {
  const res = await fetch(`${FAKE_URL}/v2/sessions`)
  const body = (await res.json()) as { items: FakeThreadMeta[] }
  return body.items
}

test.beforeEach(async () => {
  await resetBackend()
})

test("没有项目时侧栏是一张平表，没有分组标题", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  // ★ There is one group — the conversations that belong to no project —
  //   and it has **no heading**: the sidebar looks exactly as it looked
  //   before the projects landed.
  await expect(page.locator(GROUP)).toHaveCount(1)
  await expect(page.locator(GROUP_TITLE)).toHaveCount(0)
  await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
  // ★ There is no project to pick, so there is no picker.
  await expect(page.locator(PICKER)).toHaveCount(0)
})

test("多项目模式按项目分组，每个会话只出现一次", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  await expect(page.locator(GROUP)).toHaveCount(3)
  await expect(page.locator(GROUP_TITLE)).toHaveText(["Alpha", "Beta", "No project"])

  const alpha = page.locator(GROUP).filter({ has: page.getByText("Alpha", { exact: true }) })
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(2)

  // ★ A conversation that belongs to no project is in its own group, and it
  //   is not in the project groups as well.
  const orphan = page.locator(GROUP).filter({ has: page.getByText("No project", { exact: true }) })
  await expect(orphan.locator(THREAD_ITEM)).toHaveCount(4)

  const titles = await page.locator(THREAD_ITEM).allInnerTexts()
  expect(new Set(titles).size).toBe(titles.length)
})

test("单项目模式只有一组，作用域的会话和未选项目的会话在一起", async ({ page }) => {
  await setProjectsMode("single")
  await openApp(page)

  await expect(page.locator(GROUP)).toHaveCount(1)
  await expect(page.locator(GROUP_TITLE)).toHaveText(["Alpha"])
  // ★ The scope leaves no choice, so there is no picker.
  await expect(page.locator(PICKER)).toHaveCount(0)

  // ★ The sessions of the scope project and the ones that belong to no
  //   project are in the same group — the process serves one directory, and
  //   those conversations have nowhere else to go. ★ Every conversation of
  //   the process is in it: the scope project holds one of them, and the
  //   other seven belong to no project.
  const group = page.locator(GROUP).first()
  await expect(group.locator(THREAD_ITEM)).toHaveCount(8)
})

test("新建会话落在侧栏选中的项目里", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  // ★ The first project is the default: a new conversation needs a project,
  //   and the one the user put at the top is the one it gets.
  await expect(page.locator(PICKER)).toHaveValue("alpha")

  await page.locator(PICKER).selectOption("beta")
  await sendMessage(page, `${MARKER_A} in beta`)

  await expect
    .poll(async () => {
      const threads = await fakeThreads()
      // ★ The new conversation is the **last** one: the fake appends it.
      return threads[threads.length - 1]?.projectId
    }, { timeout: 7000 })
    .toBe("beta")

  // ★ And the sidebar shows it under that project. ★ The conversation is
  //   named after its first question once the run is over — the title is
  //   the server's, not the placeholder's.
  const beta = page.locator(GROUP).filter({ has: page.getByText("Beta", { exact: true }) })
  await expect(beta.locator(THREAD_ITEM).filter({ hasText: MARKER_A })).toHaveCount(1)
})

// ★ The project table is writable from the interface.
//
// ★ Before this, the table could only be written from the command line, and
//   a multi-project process started without a directory had **no way at all**
//   to get a project: the picker was hidden because the table was empty, and
//   nothing else spoke to it. ★ The entry is below the input box, because a
//   project is what a conversation belongs to — a thing decided before the
//   first word is typed.

test("项目可以从界面创建、重命名、删除", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  // ★ The table is empty, and the entry is still there: an empty table is
  //   exactly the case where the user needs it.
  await expect(page.locator(PROJECTS_BUTTON)).toBeVisible()
  await page.locator(PROJECTS_BUTTON).click()
  await expect(page.locator(PROJECTS_DIALOG)).toBeVisible()
  await expect(page.locator(PROJECTS_EMPTY)).toBeVisible()

  await page.locator(PROJECT_NAME_INPUT).fill("CodeLab")
  await page.locator(PROJECT_ROOT_INPUT).fill("/tmp/codelab")
  await page.locator(PROJECT_CREATE).click()

  // ★ The list that is shown is the list the **server** answers with.
  await expect(page.locator(PROJECT_ROW)).toHaveCount(1)
  await expect(page.locator(PROJECT_NAME)).toHaveText("CodeLab")

  // ★ And the project that was just made is the one a new conversation
  //   belongs to — the user made it, and the next conversation is the one
  //   they are about to start.
  await expect(page.locator(PICKER)).toHaveValue("codelab")

  await page.getByRole("button", { name: "Rename CodeLab" }).click()
  await page.locator(PROJECT_RENAME_INPUT).fill("Work")
  await page.locator(PROJECT_RENAME_INPUT).press("Enter")
  await expect(page.locator(PROJECT_NAME)).toHaveText("Work")

  await page.getByRole("button", { name: "Delete Work" }).click()
  await expect(page.locator(PROJECT_ROW)).toHaveCount(0)
  await expect(page.locator(PROJECTS_EMPTY)).toBeVisible()
})

test("没有目录就建不了项目，理由写在那儿", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_NAME_INPUT).fill("Nowhere")
  // ★ The button is disabled while the root is empty: a project *is* a
  //   directory, and one without a directory is a label with nothing under
  //   it.
  await expect(page.locator(PROJECT_CREATE)).toBeDisabled()
})

test("名字可以留空 —— 目录名就是它的名字", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  // ★ The **name** is optional and the **root** is not: a project *is* a
  //   directory, and the server derives the name from it. ★ So an empty name
  //   does not stop the create — only an empty root does.
  await page.locator(PROJECT_ROOT_INPUT).fill("/tmp/openheron")
  await expect(page.locator(PROJECT_CREATE)).toBeEnabled()
  await page.locator(PROJECT_CREATE).click()

  await expect(page.locator(PROJECT_ROW)).toHaveCount(1)
  // ★ The name came from the directory, and it is the server's answer.
  await expect(page.locator(PROJECT_NAME)).toHaveText("openheron")
})

test("改名要名字，空的留在那儿，理由写在那儿", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_NAME_INPUT).fill("CodeLab")
  await page.locator(PROJECT_ROOT_INPUT).fill("/tmp/codelab")
  await page.locator(PROJECT_CREATE).click()
  await expect(page.locator(PROJECT_ROW)).toHaveCount(1)

  await page.getByRole("button", { name: "Rename CodeLab" }).click()
  await page.locator(PROJECT_RENAME_INPUT).fill("")
  await page.locator(PROJECT_RENAME_INPUT).press("Enter")

  // ★ Renaming is the one place where the name is **required**: the project
  //   already has one, and an empty name would take it away. ★ The dialog
  //   stays open and says why.
  await expect(page.locator(PROJECTS_DIALOG)).toBeVisible()
  await expect(page.locator(PROJECT_REFUSAL)).toContainText("The name must not be empty.")
  // ★ And the name is still the old one: the rename did not happen.
  await page.locator(PROJECT_RENAME_INPUT).press("Escape")
  await expect(page.locator(PROJECT_NAME)).toHaveText("CodeLab")
})

test("单项目模式没有创建项目的入口", async ({ page }) => {
  await setProjectsMode("single")
  await openApp(page)

  // ★ The process serves one directory, and a new project could never be
  //   picked — so the entry would open a list of projects the process
  //   cannot switch between. ★ It is a decoration, and it is not there.
  await expect(page.locator(PROJECTS_BUTTON)).toHaveCount(0)
})

import { expect, test } from "@playwright/test"

import {
  COMPOSER,
  FAKE_URL,
  MARKER_A,
  openApp,
  resetBackend,
  sendMessage,
  setDirPickMode,
  setProjectsMode,
} from "./fixtures"

// ★ The sidebar is a **file tree**: projects are folders, conversations are
//   the files in them.
//
// ★ The grouping is the **server's** answer: the client reads the project
//   table and the scope, and it draws what it was given. ★ These specs pin
//   the three modes — no projects, many projects, one project — because a
//   sidebar that shows a conversation in the wrong group is not a cosmetic
//   mistake: it says the work happens in a directory it does not happen in.
//
// ★ And the button under the input box has **two forms**: a switch on the
//   empty view, a label on a conversation page. ★ A conversation's project
//   is immutable, so a switch there could only mean "leave this
//   conversation" — and a button that says one thing and does another is
//   worse than no button.

const GROUP = '[data-testid="session-group"]'
const GROUP_TITLE = '[data-testid="session-group-title"]'
const GROUP_TOGGLE = '[data-testid="group-toggle"]'
const GROUP_COUNT = '[data-testid="group-count"]'
const TREE = '[data-testid="session-tree"]'
const THREAD_ITEM = '[data-testid="thread-item"]'
const THREAD_ROW = '[data-testid="thread-row"]'
const NEW_THREAD = '[data-testid="new-thread"]'
const PROJECTS_BUTTON = '[data-testid="projects-button"]'
const PROJECTS_LABEL = '[data-testid="projects-label"]'
const PROJECTS_DIALOG = '[data-testid="projects-dialog"]'
const PROJECTS_EMPTY = '[data-testid="projects-empty"]'
const PROJECT_ROW = '[data-testid="project-row"]'
const PROJECT_NAME = '[data-testid="project-name"]'
const PROJECT_NAME_INPUT = '[data-testid="project-name-input"]'
const PROJECT_ROOT_INPUT = '[data-testid="project-root-input"]'
const PROJECT_CREATE = '[data-testid="project-create"]'
const PROJECT_REFUSAL = '[data-testid="project-refusal"]'
const PROJECT_PICK = '[data-testid="project-pick-directory"]'
const PROJECT_CURRENT = '[data-testid="project-current"]'
const PROJECT_MENU_BUTTON = '[data-testid="project-menu-button"]'
const PROJECT_MENU = '[data-testid="project-menu"]'
const PROJECT_GROUP_RENAME = '[data-testid="project-group-rename"]'
const PROJECT_ROOT_DIALOG = '[data-testid="project-root-dialog"]'
const PROJECT_ROOT_EDIT = '[data-testid="project-root-edit"]'
const PROJECT_ROOT_PICK = '[data-testid="project-root-pick"]'
const PROJECT_ROOT_SAVE = '[data-testid="project-root-save"]'
const PROJECT_ROOT_REFUSAL = '[data-testid="project-root-refusal"]'

interface FakeThreadMeta {
  threadId: string
  title: string
  projectId: string
}

interface FakeProject {
  id: string
  name: string
  root: string
}

async function fakeThreads(): Promise<FakeThreadMeta[]> {
  const res = await fetch(`${FAKE_URL}/v2/sessions`)
  const body = (await res.json()) as { items: FakeThreadMeta[] }
  return body.items
}

async function fakeProjects(): Promise<FakeProject[]> {
  const res = await fetch(`${FAKE_URL}/v2/projects`)
  const body = (await res.json()) as { items: FakeProject[] }
  return body.items
}

test.beforeEach(async () => {
  await resetBackend()
})

test("没有项目时侧栏是一张平表，没有分组标题", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  // ★ There is no project to be a folder, so the conversations sit at the
  //   root of the tree — and there is **no heading**: the sidebar looks
  //   exactly as it looked before the projects landed.
  await expect(page.locator(GROUP_TITLE)).toHaveCount(0)
  await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
  await expect(page.locator(TREE).locator(THREAD_ROW)).toHaveCount(8)
})

test("多项目模式按项目分组，每个会话只出现一次", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  await expect(page.locator(GROUP)).toHaveCount(3)
  await expect(page.locator(GROUP_TITLE)).toHaveText(["Alpha", "Beta", "Default Project"])

  const alpha = page.locator('[data-group="alpha"]')
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(2)

  // ★ A conversation that belongs to no project is in its own group, and it
  //   is not in the project groups as well.
  const orphan = page.locator('[data-group="no-project"]')
  await expect(orphan.locator(THREAD_ITEM)).toHaveCount(4)

  const titles = await page.locator(THREAD_ITEM).allInnerTexts()
  expect(new Set(titles).size).toBe(titles.length)
})

test("单项目模式只有一组，作用域的会话和未选项目的会话在一起", async ({ page }) => {
  await setProjectsMode("single")
  await openApp(page)

  await expect(page.locator(GROUP)).toHaveCount(1)
  await expect(page.locator(GROUP_TITLE)).toHaveText(["Alpha"])

  // ★ The sessions of the scope project and the ones that belong to no
  //   project are in the same group — the process serves one directory, and
  //   those conversations have nowhere else to go. ★ Every conversation of
  //   the process is in it: the scope project holds one of them, and the
  //   other seven belong to no project.
  const group = page.locator(GROUP).first()
  await expect(group.locator(THREAD_ITEM)).toHaveCount(8)
})

// ★ The sidebar is a map, and it does not say where you work.
//
// ★ The picker that used to sit at the top of the sidebar is gone: it was a
//   **switch**, and a switch belongs to the moment where nothing is decided
//   yet. ★ On a conversation page the only place that names the project is
//   the label under the input box.

test("侧栏顶部不再有项目选择器", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  await expect(page.locator("aside select")).toHaveCount(0)
  // ★ The entry that is left is under the input box: it is a switch, and it
  //   is there only while nothing is decided.
  await expect(page.locator(PROJECTS_BUTTON)).toBeVisible()
})

// ★ The tree says what it is.

test("树的结构对读屏说得通", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const tree = page.getByRole("tree")
  await expect(tree).toHaveCount(1)

  // ★ A folder is a tree item that can be expanded — and the arrow says
  //   whether it is.
  const alpha = page.locator('[data-group="alpha"]')
  await expect(alpha).toHaveAttribute("role", "treeitem")
  await expect(alpha).toHaveAttribute("aria-expanded", "true")
  await expect(alpha).toHaveAttribute("aria-level", "1")

  // ★ And the conversations in it are one level deeper.
  await expect(alpha.locator(THREAD_ROW).first()).toHaveAttribute("aria-level", "2")
  await expect(alpha.getByRole("group")).toHaveCount(1)
})

test("★ 折叠：三角收起一组，计数还在", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const alpha = page.locator('[data-group="alpha"]')
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(2)

  await alpha.locator(GROUP_TOGGLE).click()

  // ★ The folder is still there — it holds something — and it says how much.
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(0)
  await expect(alpha.locator(GROUP_COUNT)).toHaveText("2")
  await expect(alpha).toHaveAttribute("aria-expanded", "false")
})

test("★ 折叠会被记住", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const alpha = page.locator('[data-group="alpha"]')
  await alpha.locator(GROUP_TOGGLE).click()
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(0)

  // ★ The fold is a **preference**: it is kept on this machine, and a
  //   reload brings it back.
  await page.reload()
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(0)
  await expect(alpha.locator(GROUP_COUNT)).toHaveText("2")
})

// ★ The button under the input box has two forms.

test("空视图上那个按钮默认是「Default Project」—— 那是默认项目根", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  // ★ "Default Project" is the fallback, and it **is** the default project
  //   root: the server accepts a conversation without a project, and it works
  //   there. ★ It is not "the first project in the table" — that one is where
  //   the user put it in the list, not a directory they picked.
  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("Default Project")
})

test("★ 对话页上那个按钮只是标签，不能切换", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  // ★ Open a conversation that belongs to Beta.
  const beta = page.locator('[data-group="beta"]')
  await beta.locator(THREAD_ITEM).first().click()

  // ★ The label names the conversation's project — and it is a label: the
  //   switch is gone, because a conversation's project cannot change.
  await expect(page.locator(PROJECTS_LABEL)).toHaveText("Beta")
  await expect(page.locator(PROJECTS_BUTTON)).toHaveCount(0)

  // ★ Clicking it does nothing at all: no dialog opens.
  await page.locator(PROJECTS_LABEL).click()
  await expect(page.locator(PROJECTS_DIALOG)).toHaveCount(0)
})

test("空视图上那个按钮可以切换", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_ROW).filter({ hasText: "Beta" }).click()

  // ★ The dialog closes — the user picked, the work is over — and the button
  //   names the project that was picked.
  await expect(page.locator(PROJECTS_DIALOG)).toHaveCount(0)
  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("Beta")

  // ★ And the next conversation is created in it.
  await sendMessage(page, `${MARKER_A} after the switch`)
  await expect
    .poll(async () => {
      const threads = await fakeThreads()
      return threads[threads.length - 1]?.projectId
    }, { timeout: 7000 })
    .toBe("beta")
})

test("★「Default Project」是列表的第一项", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()

  // ★ "Default Project" leads the list: it is "do not pick a project", not one
  //   of the projects, and last would make it look like one of the rows above.
  await expect(page.locator(PROJECT_ROW).first().locator(PROJECT_NAME)).toHaveText("Default Project")
  await page.locator(PROJECT_ROW).first().click()

  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("Default Project")
  await sendMessage(page, `${MARKER_A} without a project`)
  await expect
    .poll(async () => {
      const threads = await fakeThreads()
      return threads[threads.length - 1]?.projectId
    }, { timeout: 7000 })
    .toBe("")
})

test("当前项目在列表里标出来", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()

  // ★ The list says which one is current — the button under the input box
  //   and the list must not disagree. ★ Nothing was picked yet, so the
  //   current one is "Default Project" (the default project root).
  const none = page.locator(PROJECT_ROW).first()
  const alpha = page.locator(PROJECT_ROW).filter({ hasText: "Alpha" })
  const beta = page.locator(PROJECT_ROW).filter({ hasText: "Beta" })
  await expect(none.locator(PROJECT_NAME)).toHaveText("Default Project")
  await expect(none).toHaveAttribute("data-active", "true")
  await expect(none.locator(PROJECT_CURRENT)).toBeVisible()
  await expect(alpha).toHaveAttribute("data-active", "false")
  await expect(alpha.locator(PROJECT_CURRENT)).toHaveCount(0)
  await expect(beta).toHaveAttribute("data-active", "false")
  await expect(beta.locator(PROJECT_CURRENT)).toHaveCount(0)
})

test("★ 换项目要回空视图 —— 对话页上那个地方不提供开关", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  // ★ Open a conversation that belongs to Beta.
  const beta = page.locator('[data-group="beta"]')
  await beta.locator(THREAD_ITEM).first().click()
  await expect(page.locator(PROJECTS_LABEL)).toHaveText("Beta")

  // ★ The way to work in another project is a **new conversation**: the
  //   empty view is where the project of the next one is chosen.
  await page.locator(NEW_THREAD).click()
  await expect(page.locator(PROJECTS_BUTTON)).toBeVisible()
  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_ROW).filter({ hasText: "Alpha" }).click()
  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("Alpha")

  // ★ And the conversation that was open is still in the sidebar: its work
  //   is not lost, only left.
  await expect(page.locator(GROUP)).toHaveCount(3)
  await expect(beta.locator(THREAD_ITEM)).toHaveCount(2)
})

test("★ New thread 回到空白 —— 项目也回到「Default Project」", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  // ★ The conversation on screen belongs to Beta, and the composer says so.
  const beta = page.locator('[data-group="beta"]')
  await beta.locator(THREAD_ITEM).first().click()
  await expect(page.locator(PROJECTS_LABEL)).toHaveText("Beta")

  // ★ A new thread is not "the conversation I was in, with a fresh
  //   transcript": the project belonged to that conversation, and a
  //   conversation's project cannot change. ★ So the composer comes back
  //   with **no** project, and the user picks one on the empty view.
  await page.locator(NEW_THREAD).click()
  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("Default Project")

  // ★ And the conversation it creates really has none: the button and the
  //   server agree — the next question works in the default project root.
  await sendMessage(page, `${MARKER_A} without a project`)
  await expect
    .poll(async () => {
      const threads = await fakeThreads()
      return threads[threads.length - 1]?.projectId
    }, { timeout: 7000 })
    .toBe("")
})

// ★ The project's own row: the actions on a project that is already on the
//   map.

test("★ 项目行的菜单里能开一段新对话", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  // ★ Open a conversation that belongs to Alpha — the label names it.
  const alpha = page.locator('[data-group="alpha"]')
  await alpha.locator(THREAD_ITEM).first().click()
  await expect(page.locator(PROJECTS_LABEL)).toHaveText("Alpha")

  // ★ The menu of the project: a new conversation there. ★ The composer
  //   comes back empty and the button names the project that was picked.
  const beta = page.locator('[data-group="beta"]')
  await beta.locator(PROJECT_MENU_BUTTON).hover()
  await beta.locator(PROJECT_MENU_BUTTON).click()
  await expect(beta.locator(PROJECT_MENU)).toBeVisible()
  await beta.getByRole("menuitem", { name: "New conversation" }).click()

  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("Beta")
  await expect(page.locator(COMPOSER)).toBeVisible()

  // ★ And the conversation that was open is still in the sidebar — its work
  //   is not lost, only left.
  await expect(page.locator(GROUP)).toHaveCount(3)
  await expect(alpha.locator(THREAD_ITEM)).toHaveCount(2)
})

test("★ 项目行的菜单里能改名", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const beta = page.locator('[data-group="beta"]')
  await beta.locator(PROJECT_MENU_BUTTON).hover()
  await beta.locator(PROJECT_MENU_BUTTON).click()
  await beta.getByRole("menuitem", { name: "Rename project" }).click()

  // ★ The name is edited **in place**: the project is seen there, so it is
  //   changed there.
  await expect(beta.locator(PROJECT_GROUP_RENAME)).toBeVisible()
  await beta.locator(PROJECT_GROUP_RENAME).fill("Beta Two")
  await beta.locator(PROJECT_GROUP_RENAME).press("Enter")

  await expect(page.locator(GROUP_TITLE)).toHaveText(["Alpha", "Beta Two", "Default Project"])
  const projects = await fakeProjects()
  expect(projects.find((p) => p.id === "beta")?.name).toBe("Beta Two")
})

test("★ 改名不能是空的，理由写在那儿", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const beta = page.locator('[data-group="beta"]')
  await beta.locator(PROJECT_MENU_BUTTON).hover()
  await beta.locator(PROJECT_MENU_BUTTON).click()
  await beta.getByRole("menuitem", { name: "Rename project" }).click()

  await beta.locator(PROJECT_GROUP_RENAME).fill("")
  await beta.locator(PROJECT_GROUP_RENAME).press("Enter")

  // ★ Renaming is the one place where the name is **required**: the project
  //   already has one, and an empty name would take it away. ★ The editor
  //   stays open and says why.
  await expect(beta.locator(PROJECT_GROUP_RENAME)).toBeVisible()
  await expect(beta.locator(PROJECT_GROUP_RENAME)).toHaveAttribute("aria-invalid", "true")
  await expect(beta.locator(PROJECT_GROUP_RENAME)).toHaveValue("")
  // ★ And the rename did not happen: the table still carries the old name.
  const projects = await fakeProjects()
  expect(projects.find((p) => p.id === "beta")?.name).toBe("Beta")
})

test("★ 项目行的菜单里能改目录", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const beta = page.locator('[data-group="beta"]')
  await beta.locator(PROJECT_MENU_BUTTON).hover()
  await beta.locator(PROJECT_MENU_BUTTON).click()
  await beta.getByRole("menuitem", { name: "Change folder" }).click()

  // ★ A project **is** a directory: this says where the agent reads and
  //   writes. ★ The field opens on the path the project has.
  await expect(page.locator(PROJECT_ROOT_DIALOG)).toBeVisible()
  await expect(page.locator(PROJECT_ROOT_EDIT)).toHaveValue("/tmp/beta")
  await page.locator(PROJECT_ROOT_EDIT).fill("/tmp/moved")
  await page.locator(PROJECT_ROOT_SAVE).click()

  await expect(page.locator(PROJECT_ROOT_DIALOG)).toHaveCount(0)
  const projects = await fakeProjects()
  expect(projects.find((p) => p.id === "beta")?.root).toBe("/tmp/moved")
})

test("★ 改目录时机器上没有选择器，理由写在那儿", async ({ page }) => {
  await setProjectsMode("multi")
  await setDirPickMode("failed")
  await openApp(page)

  const beta = page.locator('[data-group="beta"]')
  await beta.locator(PROJECT_MENU_BUTTON).hover()
  await beta.locator(PROJECT_MENU_BUTTON).click()
  await beta.getByRole("menuitem", { name: "Change folder" }).click()

  await expect(page.locator(PROJECT_ROOT_DIALOG)).toBeVisible()
  await page.locator(PROJECT_ROOT_PICK).click()

  // ★ The reason is the server's own sentence, and the path that was there
  //   is still there: the dialog is a form, and a chooser that could not
  //   open is not a closed form.
  await expect(page.locator(PROJECT_ROOT_REFUSAL)).toContainText("no folder chooser")
  await expect(page.locator(PROJECT_ROOT_EDIT)).toHaveValue("/tmp/beta")
})

test("★ 项目行的菜单里能移除项目", async ({ page }) => {
  await setProjectsMode("multi")
  await openApp(page)

  const beta = page.locator('[data-group="beta"]')
  await beta.locator(PROJECT_MENU_BUTTON).hover()
  await beta.locator(PROJECT_MENU_BUTTON).click()
  await beta.getByRole("menuitem", { name: "Remove project" }).click()

  // ★ The project is gone from the map — and the conversations that belonged
  //   to it are **kept**: they fall back to no project.
  await expect(page.locator(GROUP)).toHaveCount(2)
  await expect(page.locator(GROUP_TITLE)).toHaveText(["Alpha", "Default Project"])
  const projects = await fakeProjects()
  expect(projects.find((p) => p.id === "beta")).toBeUndefined()
})

test("★ 单项目模式不给移动和移除", async ({ page }) => {
  await setProjectsMode("single")
  await openApp(page)

  // ★ The process serves **one** directory, and that directory is the scope:
  //   moving or removing it would change what the process serves.
  const alpha = page.locator('[data-group="alpha"]')
  await alpha.locator(PROJECT_MENU_BUTTON).hover()
  await alpha.locator(PROJECT_MENU_BUTTON).click()
  await expect(alpha.getByRole("menuitem", { name: "New conversation" })).toBeVisible()
  await expect(alpha.getByRole("menuitem", { name: "Change folder" })).toHaveCount(0)
  await expect(alpha.getByRole("menuitem", { name: "Remove project" })).toHaveCount(0)
})

// ★ The project table is writable from the interface.
//
// ★ Before this, the table could only be written from the command line, and
//   a multi-project process started without a directory had **no way at all**
//   to get a project: the picker was hidden because the table was empty, and
//   nothing else spoke to it. ★ The entry is below the input box, because a
//   project is what a conversation belongs to — a thing decided before the
//   first word is typed. ★ And **new** projects are made there: the actions
//   on a project that exists are on its own row in the sidebar.

test("项目可以从界面创建", async ({ page }) => {
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
  await expect(page.locator(PROJECT_ROW)).toHaveCount(2)
  await expect(page.locator(PROJECT_NAME).last()).toHaveText("CodeLab")

  // ★ And the project that was just made is the one a new conversation
  //   belongs to — the user made it, and the next conversation is the one
  //   they are about to start.
  await expect(page.locator(PROJECTS_BUTTON)).toHaveText("CodeLab")
})

test("★ 新建的项目空着也是一行 —— 地图上有它", async ({ page }) => {
  await setProjectsMode("default")
  await openApp(page)

  // ★ The table is empty, so the sidebar is a flat list of conversations.
  await expect(page.locator(GROUP)).toHaveCount(0)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_NAME_INPUT).fill("CodeLab")
  await page.locator(PROJECT_ROOT_INPUT).fill("/tmp/codelab")
  await page.locator(PROJECT_CREATE).click()

  // ★ The create keeps the dialog open: it shows the project that was just
  //   made, and that project is the current one now. ★ Close it — the
  //   sidebar is behind the overlay until it is gone.
  await expect(page.locator(PROJECTS_DIALOG)).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.locator(PROJECTS_DIALOG)).toHaveCount(0)

  // ★ Now the table carries a project, and the sidebar is a map. ★ The
  //   project is a row even though no conversation has been started in it:
  //   that row is where its actions live, and a project with no row could
  //   not be renamed, moved or removed.
  // ★ The row is found by its **key**, not by its name: the name is what the
  //   user types, and a rename would make the locator miss.
  const group = page.locator('[data-group="codelab"]')
  await expect(group).toHaveCount(1)
  await expect(group.locator(GROUP_TITLE)).toHaveText("CodeLab")

  // ★ The conversations that belong to no project are still in their own
  //   basket — the new project did not swallow them.
  await expect(page.locator('[data-group="no-project"]')).toHaveCount(1)

  // ★ Nothing to unfold, so there is no triangle: a fold of nothing is a
  //   control that does nothing.
  await expect(group.locator(GROUP_TOGGLE)).toHaveCount(0)

  // ★ And the menu is on the row: the project is managed where it is seen.
  await group.locator(PROJECT_MENU_BUTTON).hover()
  await group.locator(PROJECT_MENU_BUTTON).click()
  await expect(group.getByRole("menuitem", { name: "Rename project" })).toBeVisible()
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

  await expect(page.locator(PROJECT_ROW)).toHaveCount(2)
  // ★ The name came from the directory, and it is the server's answer.
  await expect(page.locator(PROJECT_NAME).last()).toHaveText("openheron")
})

test("单项目模式没有创建项目的入口", async ({ page }) => {
  await setProjectsMode("single")
  await openApp(page)

  // ★ The process serves one directory, and a new project could never be
  //   picked — so the entry would open a list of projects the process
  //   cannot switch between. ★ It is a decoration, and it is not there.
  await expect(page.locator(PROJECTS_BUTTON)).toHaveCount(0)
})

// ★ The folder chooser.
//
// ★ The button next to the root field opens the **server's** dialog: the
//   browser will not tell a page where a chosen directory lives, so the
//   machine the server runs on is the one that is asked.

test("文件夹按钮把选中的目录填进 root", async ({ page }) => {
  await setProjectsMode("default")
  await setDirPickMode("picked", "/tmp/picked-project")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await expect(page.locator(PROJECT_PICK)).toBeVisible()

  await page.locator(PROJECT_PICK).click()

  // ★ The path the chooser answered with is in the field, and the form is
  //   ready to create the project.
  await expect(page.locator(PROJECT_ROOT_INPUT)).toHaveValue("/tmp/picked-project")
  await page.locator(PROJECT_NAME_INPUT).fill("Picked")
  await expect(page.locator(PROJECT_CREATE)).toBeEnabled()
})

test("关掉选择器不是失败 —— 原来填的还在", async ({ page }) => {
  await setProjectsMode("default")
  await setDirPickMode("cancelled")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_ROOT_INPUT).fill("/tmp/typed-by-hand")
  await page.locator(PROJECT_PICK).click()

  // ★ "Not now" is not a failure: the field keeps what was typed, and
  //   nothing is said about it.
  await expect(page.locator(PROJECT_ROOT_INPUT)).toHaveValue("/tmp/typed-by-hand")
  await expect(page.locator(PROJECT_REFUSAL)).toHaveCount(0)
})

test("机器上没有选择器，理由写在那儿", async ({ page }) => {
  await setProjectsMode("default")
  await setDirPickMode("failed")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_PICK).click()

  // ★ The reason is the server's own sentence, and the form is still there:
  //   the path can still be typed.
  await expect(page.locator(PROJECT_REFUSAL)).toContainText("no folder chooser")
  await expect(page.locator(PROJECT_ROOT_INPUT)).toBeVisible()
})

// ★ A project name is the user's, and it can be longer than a phone is wide.
//
// ★ The line under the input box has to wrap it — the entries behind it must
//   not be pushed off the screen. ★ A name that is cut off is still a name:
//   the whole one stays in the tooltip and in the label a screen reader
//   reads.
test("★ 项目名很长时，输入框下面那一行也不溢出", async ({ page }) => {
  const PHONE = 390
  await page.setViewportSize({ width: PHONE, height: 844 })
  await setProjectsMode("default")
  await openApp(page)

  await page.locator(PROJECTS_BUTTON).click()
  await page.locator(PROJECT_NAME_INPUT).fill("A project name that is far too long for a phone")
  await page.locator(PROJECT_ROOT_INPUT).fill("/tmp/long-name")
  await page.locator(PROJECT_CREATE).click()
  await page.keyboard.press("Escape")

  const meta = page.locator('[data-testid="composer-meta"]')
  await expect(meta).toBeVisible()

  // ★ The row fits the screen: nothing hangs off the edge.
  const overflow = await meta.evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)

  // ★ And the last entry is on the screen, not past it.
  const right = await page
    .locator('[data-testid="operations-button"]')
    .evaluate((el) => el.getBoundingClientRect().right)
  expect(right).toBeLessThanOrEqual(PHONE)
})

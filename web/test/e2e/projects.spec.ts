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

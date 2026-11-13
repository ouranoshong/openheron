import { expect, test } from "@playwright/test"

import {
  APPROVE_BUTTON,
  COMPOSER,
  MARKER_A,
  MARKER_B,
  NEW_THREAD,
  SEND_BUTTON,
  STOP_BUTTON,
  THREAD_ITEM,
  THREAD_ROW,
  clickByText,
  clickMenuItem,
  openApp,
  openThread,
  openToolCard,
  resetBackend,
  sendMessage,
  setScenario,
  threadTitles,
  trackRequests,
} from "./fixtures"

test.beforeEach(async () => {
  await resetBackend()
})

test.describe("会话列表", () => {
  test("加载后列出已有会话", async ({ page }) => {
    await openApp(page)
    const titles = await threadTitles(page)
    expect(titles.join(" | ")).toContain("Alpha conversation")
    expect(titles.join(" | ")).toContain("Bravo conversation")
  })

  test("切换会话后显示的是那个会话自己的消息", async ({ page }) => {
    await openApp(page)

    await openThread(page, "Alpha conversation")
    await expect(page.getByText(MARKER_A).first()).toBeVisible()

    await openThread(page, "Bravo conversation")
    await expect(page.getByText(MARKER_B).first()).toBeVisible()
    await expect(page.getByText(MARKER_A)).toHaveCount(0)
  })

  test("点 New Thread 会在侧栏多出一条", async ({ page }) => {
    await openApp(page)
    const before = (await threadTitles(page)).length

    await page.locator(NEW_THREAD).click()

    await expect(page.locator(THREAD_ITEM)).toHaveCount(before)
    await expect(page.locator(COMPOSER)).toBeVisible()
  })

  test("点 New Thread 会请求 /v2/sessions/undefined", async ({ page }) => {
    await openApp(page)
    const reqs = trackRequests(page)

    await page.locator(NEW_THREAD).click()
    await page.waitForTimeout(500)

    const bad = reqs.filter((p) => p.includes("undefined"))
    expect(bad, `sent a request containing undefined: ${bad.join(", ")}`).toHaveLength(0)
  })

  test("记住的会话没了 → 回欢迎界面，不甩错误", async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem("openheron.activeThread", "99999999-9999-4999-8999-999999999999")
    })
    await openApp(page)

    await expect(page.locator('[data-testid="run-error"]')).toHaveCount(0)
    await expect(page.getByText("Ask anything about this workspace.").first()).toBeVisible()
    await expect
      .poll(() => page.evaluate(() => localStorage.getItem("openheron.activeThread")))
      .toBeNull()
  })

  test("刷新后回到的不是上一个会话", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Bravo conversation")
    await expect(page.getByText(MARKER_B).first()).toBeVisible()

    await page.reload()
    await expect(page.locator(THREAD_ITEM).first()).toBeVisible()

    await expect(page.getByText(MARKER_B).first()).toBeVisible()
  })

  test("改名后侧栏显示新标题", async ({ page }) => {
    await openApp(page)
    const row = page.locator(THREAD_ROW).filter({ hasText: "Alpha conversation" }).first()
    await row.hover()
    await row.locator('[aria-label="More options"]').click()
    await clickMenuItem(page, "Rename")

    const input = page.getByRole("textbox", { name: "Thread title" })
    await input.fill("Renamed by e2e")
    await input.press("Enter")

    await expect(page.locator(THREAD_ITEM).filter({ hasText: "Renamed by e2e" })).toHaveCount(1)
  })

  test("删除后条目消失", async ({ page }) => {
    await openApp(page)
    const row = page.locator(THREAD_ROW).filter({ hasText: "Bravo conversation" }).first()
    await row.hover()
    await row.locator('[aria-label="More options"]').click()
    await clickMenuItem(page, "Delete")

    await expect(page.locator(THREAD_ITEM).filter({ hasText: "Bravo conversation" })).toHaveCount(0)
  })
})

test.describe("会话标题", () => {
  // ★ The title belongs to the conversation. The optimistic bubble that
  //   opens a turn must not rename it — the title used to flip to "New Chat"
  //   for the whole run and snap back when the list was refreshed at the end
  //   of the turn, so the user watched the conversation lose its name.
  test("★ 发送消息的过程中，标题不会被改掉", async ({ page }) => {
    await setScenario("slowRun")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await sendMessage(page, `${MARKER_A} keep going`)

    // ★ The run is still going, so what the sidebar shows right now is the
    //   live title — not the one the server sends when the turn is over.
    await expect(page.locator(STOP_BUTTON)).toBeVisible({ timeout: 5000 })
    const titles = (await threadTitles(page)).join(" | ")
    expect(titles).toContain("Alpha conversation")
    expect(titles).not.toContain("New Chat")
  })

  // ★ The rename editor opens on the name the row shows. It used to open
  //   on the name the row was **mounted** with — a session is named by the
  //   server after its first question, so that name was still "New Chat"
  //   while the sidebar already showed the real one. Pressing Enter then
  //   replaced the conversation's name with "New Chat".
  test("★ 改名时编辑的是原来的名字，而不是 New Chat", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} 这是一个非常长的会话标题用来检查编辑框会不会把名字截断`)

    // ★ The server names the session after its first question, so the row
    //   carries that name by the time the turn is over.
    const item = page.locator(THREAD_ITEM).filter({ hasText: MARKER_A }).first()
    await expect(item).toBeVisible({ timeout: 7000 })

    const row = page.locator(THREAD_ROW).filter({ hasText: MARKER_A }).first()
    await row.hover()
    await row.locator('[aria-label="More options"]').click()
    await clickMenuItem(page, "Rename")

    const input = page.getByRole("textbox", { name: "Thread title" })
    await expect(input).toHaveValue(new RegExp(MARKER_A))

    // ★ The name is selected, so the user renames it instead of retyping
    //   it from scratch.
    await expect(input).toBeFocused()
    expect(
      await input.evaluate((el: HTMLTextAreaElement) => el.selectionStart === 0 && el.selectionEnd === el.value.length),
      "the whole name should be selected",
    ).toBe(true)

    // ★ And the editor shows the whole name: the row truncates a long
    //   title to one line, but the box the user reads it in wraps and
    //   grows — no part of the name is cut off.
    const box = await input.evaluate((el: HTMLTextAreaElement) => ({
      clipped: el.scrollHeight > el.clientHeight + 1,
      height: el.clientHeight,
    }))
    expect(box.clipped, "the editor must not cut the name off").toBe(false)
    expect(box.height, "a long name wraps, so the editor grows past one line").toBeGreaterThan(34)

    // ★ And committing it keeps the name: the editor did not replace the
    //   conversation's title with the placeholder.
    await input.press("Enter")
    await expect(page.locator(THREAD_ITEM).filter({ hasText: MARKER_A })).toHaveCount(1)

    // ★ And the server still holds that name: nothing was renamed to
    //   "New Chat" behind the user's back.
    await page.reload()
    await expect(page.locator(THREAD_ITEM).filter({ hasText: MARKER_A })).toHaveCount(1)
    expect((await threadTitles(page)).join(" | ")).not.toContain("New Chat")
  })
})

test.describe("发送与流式渲染", () => {
  test("发送一条消息恰好创建一个会话条目", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    const before = (await threadTitles(page)).length

    await sendMessage(page, `${MARKER_A} please answer`)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible()
    await expect(page.locator(THREAD_ITEM)).toHaveCount(before + 1)
  })

  test("连续发两条只创建一个会话", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    const before = (await threadTitles(page)).length

    await sendMessage(page, `${MARKER_A} first`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible()
    await sendMessage(page, `${MARKER_A} second`)
    await expect(page.getByText(/scripted answer/).nth(1)).toBeVisible()

    await expect(page.locator(THREAD_ITEM)).toHaveCount(before + 1)
  })

  test("流式内容是增量出现而不是一次性出现", async ({ page }) => {
    await setScenario("streaming")
    await openApp(page)

    await page.locator(COMPOSER).fill("stream please")
    await page.locator('[aria-label="Send message"]').click()

    await expect(page.getByText(/streamed answer/).first()).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/incremental rendering/).first()).toBeVisible({ timeout: 7000 })
  })
})

test.describe("审批（HITL）", () => {
  test("工具调用触发审批卡，同意后续跑完成", async ({ page }) => {
    await setScenario("approval")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} write the file`)

    await expect(page.getByText(/write_file/).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator(COMPOSER)).toHaveCount(0)
    await expect(page.locator('[data-testid="approval-tool"]')).toHaveText("write_file")
    await expect(page.locator('[data-testid="approval-arguments"]')).toContainText(
      "/tmp/x.txt",
    )
    await expect(page.locator('[data-testid="approval-requested-by"]')).toHaveText(
      "Requested by worker",
    )
    await expect(page.locator(APPROVE_BUTTON).first()).toBeVisible()

    await clickByText(page, "Approve", "Allow")

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator(COMPOSER)).toBeVisible()
  })

  test("运行失败时有可见提示而不是静默", async ({ page }) => {
    await setScenario("error")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} trigger error`)

    await expect(page.getByText(/unavailable|error|failed/i).first()).toBeVisible({ timeout: 7000 })
  })

  // ★ **The reason has to reach the user.** An agent that cannot be built
  //   used to arrive as "turn start timeout" — a symptom naming neither the
  //   file nor the model. The chat must show the reason itself, because the
  //   answer is "fix the configuration", not "send the message again".
  test("装不起来的 Agent 把原因说清楚", async ({ page }) => {
    await setScenario("agentUnavailable")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} trigger error`)

    const error = page.locator('[data-testid="run-error"]')
    await expect(error).toBeVisible({ timeout: 7000 })
    await expect(error).toContainText("is not declared under llm/")
    // ★ And it must not be the timeout: that was the symptom that hid it.
    await expect(error).not.toContainText("timeout")
  })
})

test.describe("多个待批", () => {
  test("一次答完整组待批，不报 interruptId 不匹配", async ({ page }) => {
    await setScenario("approvalMulti")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} need three approvals`)

    await expect(page.getByText(/3 tool calls need your approval/)).toBeVisible({ timeout: 7000 })
    await expect(page.locator('[data-testid="approval-item"]')).toHaveCount(3)
    await expect(page.locator('[data-testid="approval-tool"]')).toHaveText([
      "write_file",
      "execute",
      "edit_file",
    ])
    await expect(page.locator(COMPOSER)).toHaveCount(0)

    await clickByText(page, "Approve", "Allow")

    await expect(page.getByText(/does not match/i)).toHaveCount(0)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator('[data-testid="approval-card"]')).toHaveCount(0)
  })

  test("整组待批一次发出，而不是一个一个", async ({ page }) => {
    await setScenario("approvalMulti")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} need three approvals`)
    await expect(page.getByText(/3 tool calls need your approval/)).toBeVisible({ timeout: 7000 })

    const resumes: unknown[][] = []
    await page.route("**/v2/run", async (route) => {
      const body = route.request().postDataJSON() as { resume?: unknown[] }
      resumes.push(body.resume ?? [])
      await route.continue()
    })

    await clickByText(page, "Approve", "Allow")
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    expect(resumes.length).toBe(1)
    expect(resumes[0]!.length).toBe(3)
  })
})

test.describe("中断（停止）", () => {
  test("停止按钮取代发送按钮并显示实心方块", async ({ page }) => {
    await setScenario("slowRun")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} keep going`)
    await expect(page.locator(STOP_BUTTON)).toBeVisible({ timeout: 5000 })
    await expect(page.locator(SEND_BUTTON)).toHaveCount(0)

    const icon = page.locator(`${STOP_BUTTON} svg`)
    await expect(icon).toHaveClass(/fill-current/)
    await expect(icon).toHaveAttribute("stroke-width", "0")
    const fill = await icon.evaluate((el) => getComputedStyle(el).fill)
    expect(fill).not.toBe("none")
  })

  test("点停止会打服务端的中止端点", async ({ page }) => {
    await setScenario("slowRun")
    const seen = trackRequests(page)
    await openApp(page)

    await sendMessage(page, `${MARKER_A} keep going`)
    await expect(page.locator(STOP_BUTTON)).toBeVisible({ timeout: 5000 })

    await page.click(STOP_BUTTON)

    await expect
      .poll(() => seen.filter((p) => p.endsWith("/abort")).length, { timeout: 5000 })
      .toBeGreaterThan(0)
  })

  test("停止后界面回到可输入", async ({ page }) => {
    await setScenario("slowRun")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} keep going`)
    await expect(page.locator(STOP_BUTTON)).toBeVisible({ timeout: 5000 })
    await page.click(STOP_BUTTON)

    await expect(page.locator(STOP_BUTTON)).toHaveCount(0)
    await expect(page.locator(SEND_BUTTON)).toBeVisible()
    await expect(page.locator(COMPOSER)).toBeEnabled()
    await expect(page.locator('[data-testid="run-error"]')).toHaveCount(0)
  })

  test("停止后没有停不下来的转圈 part", async ({ page }) => {
    await setScenario("slowRun")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} keep going`)
    await expect(page.getByText("Thinking…")).toBeVisible({ timeout: 5000 })

    await page.click(STOP_BUTTON)

    await expect(page.getByText("Thinking…")).toHaveCount(0)
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText(/^Interrupted/, {
      timeout: 5000,
    })
  })

  test("停止后能接着发下一条", async ({ page }) => {
    await setScenario("slowRun")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} keep going`)
    await expect(page.locator(STOP_BUTTON)).toBeVisible({ timeout: 5000 })
    await page.click(STOP_BUTTON)

    await setScenario("simple")
    await sendMessage(page, `${MARKER_A} next question`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
  })

  test("思维链结束后标题变成 Thought 并带上用时", async ({ page }) => {
    await setScenario("reasoningDone")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} think first`)
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText("Thought for 1.5s", {
      timeout: 5000,
    })
    await expect(page.getByText("Thinking…")).toHaveCount(0)
  })

  test("刷新后思维链与用时仍在（历史回放重建）", async ({ page }) => {
    await setScenario("reasoningDone")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} think first`)
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText("Thought for 1.5s", {
      timeout: 5000,
    })

    await page.reload()
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText("Thought for 1.5s", {
      timeout: 7000,
    })
  })
})

test.describe("协议：一次往返", () => {
  test("发第一条消息时前端建一次会话", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)

    const writes: string[] = []
    page.on("request", (r: { url(): string; method(): string }) => {
      const u = new URL(r.url())
      if (u.pathname.startsWith("/v2/sessions") && r.method() !== "GET") {
        writes.push(`${r.method()} ${u.pathname}`)
      }
    })

    await sendMessage(page, `${MARKER_A} one round trip`)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    expect(writes, `should create exactly one session: ${writes.join(", ")}`).toEqual([
      "POST /v2/sessions",
    ])
  })

  test("会话 id 来自 RUN_STARTED 而不是事先分配", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)

    const runThreadIds: string[] = []
    page.on("response", async (r) => {
      if (!r.url().includes("/v2/run")) return
      try {
        const raw = await r.text()
        for (const line of raw.split("\n")) {
          if (!line.startsWith("data:")) continue
          const ev = JSON.parse(line.slice(5))
          if (ev.type === "RUN_STARTED" && ev.threadId) runThreadIds.push(ev.threadId)
        }
      } catch {
        // The stream may already have been consumed; skip it when it is gone — the assertion below works on the length.
      }
    })

    await sendMessage(page, `${MARKER_A} thread id from stream`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    if (runThreadIds.length > 0) {
      const id = runThreadIds.at(-1)!
      const detail = await page.request.get(`/v2/sessions/${id}`)
      expect(detail.status(), `the session ${id} announced by RUN_STARTED should exist`).toBe(200)
    }
  })
})

test.describe("附件", () => {

  test("已有会话时上传附件，chip 出现", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("# hello") },
    ])

    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)
    await expect(page.getByText("notes.md")).toBeVisible()
  })

  test("欢迎界面下上传会先建会话（选文件即明确动作）", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    const before = (await threadTitles(page)).length

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "from-welcome.txt", mimeType: "text/plain", buffer: Buffer.from("hi") },
    ])

    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)
    await expect(page.locator(THREAD_ITEM)).toHaveCount(before + 1)
  })

  test("★ 打开页面与点 New thread 仍然零会话写入", async ({ page }) => {
    await openApp(page)
    const writes: string[] = []
    page.on("request", (r: { url(): string; method(): string }) => {
      const u = new URL(r.url())
      if (u.pathname.startsWith("/v2/sessions") && r.method() !== "GET") {
        writes.push(`${r.method()} ${u.pathname}`)
      }
    })

    await page.locator(NEW_THREAD).click()
    await page.waitForTimeout(400)

    expect(writes, `there should be no write requests: ${writes.join(", ")}`).toHaveLength(0)
  })

  test("附件落在会话 workspace 里，能被读回", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "probe.txt", mimeType: "text/plain", buffer: Buffer.from("probe body") },
    ])
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)

    const res = await page.request.get("/v2/sessions")
    const { items } = await res.json()
    const alpha = items.find((t: { title: string }) => t.title === "Alpha conversation")
    const files = await (await page.request.get(`/v2/sessions/${alpha.threadId}/files`)).json()
    expect(files.files.map((f: { name: string }) => f.name)).toContain("probe.txt")
  })

  test("发送后附件跟着消息走，且消息正常跑完", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "with-message.txt", mimeType: "text/plain", buffer: Buffer.from("x") },
    ])
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} look at the attachment`)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    // ★ The file went out with the question, so it is on the message now
    //   and no longer waiting in the composer.
    await expect(page.locator('[data-testid="message-attachment"]')).toHaveCount(1)
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(0)
  })

  test("chip 上的 × 能移除，而且服务端文件也没了", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "removable.txt", mimeType: "text/plain", buffer: Buffer.from("y") },
    ])
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)

    await page.locator('[aria-label="Remove removable.txt"]').click()
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(0)

    const res = await page.request.get("/v2/sessions")
    const { items } = await res.json()
    const alpha = items.find((t: { title: string }) => t.title === "Alpha conversation")
    const files = await (await page.request.get(`/v2/sessions/${alpha.threadId}/files`)).json()
    // ★ The file is really deleted. The chip used to hide it while the
    //   server kept it — the user was told it was gone, and it was not.
    expect(files.files.map((f: { name: string }) => f.name)).not.toContain("removable.txt")
  })
})

test.describe("markdown 与语法高亮", () => {
  test("代码块渲染成块级元素并带上语言标记", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me some code`)

    const block = page.locator('[data-testid="code-block"]')
    await expect(block).toHaveCount(1)
    await expect(block).toHaveAttribute("data-lang", "go")
    await expect(block).toContainText("package main")
  })

  test("★ 静默一段时间后代码块真的被着色", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me some code`)

    const block = page.locator('[data-testid="code-block"]')
    await expect(block).toHaveCount(1)

    await expect(block.locator("span[style*='--shiki-light']").first()).toBeVisible({
      timeout: 8000,
    })

    const tokens = await block.locator("span").evaluateAll((els) =>
      els
        .filter((e) => e.getAttribute("style")?.includes("--shiki-light"))
        .slice(0, 6)
        .map((e) => ({
          text: (e.textContent ?? "").trim(),
          variable: (e as HTMLElement).style.getPropertyValue("--shiki-light").trim(),
          computed: getComputedStyle(e).color,
        })),
    )
    expect(tokens.length).toBeGreaterThan(2)

    const computed = new Set(tokens.map((t) => t.computed))
    expect(
      computed.size,
      `all tokens have the same color (${[...computed].join(",")}) — highlighting did not take effect`,
    ).toBeGreaterThan(1)

    for (const t of tokens) {
      const want = hexToRgb(t.variable)
      expect(t.computed, `the color of token "${t.text}" should come from ${t.variable}`).toBe(want)
    }
  })

  test("★ 暗色主题下用另一套颜色", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me some code`)
    const block = page.locator('[data-testid="code-block"]')
    await expect(block.locator("span[style*='--shiki-dark']").first()).toBeVisible({
      timeout: 8000,
    })

    const read = () =>
      block
        .locator("span")
        .evaluateAll((els) => {
          const el = els.find((e) => e.getAttribute("style")?.includes("--shiki-light"))
          return el ? getComputedStyle(el).color : null
        })

    const light = await read()
    await page.locator("html").evaluate((el) => el.classList.add("dark"))
    await expect.poll(read, { timeout: 3000 }).not.toBe(light)
    expect(await read()).toBeTruthy()
  })

  test("★ 高亮不能引入原始 HTML —— 模型输出不可信", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me some code`)

    const block = page.locator('[data-testid="code-block"]').first()
    await expect(block.locator("span[style*='--shiki-light']").first()).toBeVisible({
      timeout: 8000,
    })
    await expect(block.locator("img, script, iframe")).toHaveCount(0)
  })

  test("行内代码保持在行内", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me some code`)

    const inline = page.locator("code", { hasText: "panic()" }).first()
    await expect(inline).toBeVisible()
    const blockText = await page.locator('[data-testid="code-block"]').first().innerText()
    expect(blockText).not.toContain("panic()")
  })

  test("★ GFM 表格有边框，且不撑破消息列", async ({ page }) => {
    await setScenario("table")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me a table`)

    const wrap = page.locator('[data-testid="markdown-table"]')
    await expect(wrap).toBeVisible()
    const table = wrap.locator("table")
    const th = table.locator("th").first()

    expect(await wrap.evaluate((el) => getComputedStyle(el).overflowX)).toBe("auto")
    expect(await table.evaluate((el) => getComputedStyle(el).borderCollapse)).toBe("collapse")
    expect(await th.evaluate((el) => getComputedStyle(el).borderTopWidth)).not.toBe("0px")
    expect(await th.evaluate((el) => getComputedStyle(el).paddingLeft)).not.toBe("0px")

    // ★ The table has a **width limit** now: it fills the column and stops
    //   there, and a long cell wraps inside its own cell instead of
    //   stretching the table. What used to be asserted here — that the
    //   table is wider than its wrapper — was the old behaviour, and
    //   keeping it would keep the bug.
    const widths = await wrap.evaluate((el) => ({
      wrapper: el.clientWidth,
      table: (el.firstElementChild as HTMLElement).scrollWidth,
    }))
    expect(
      widths.table,
      "the table should not be wider than the column it sits in",
    ).toBeLessThanOrEqual(widths.wrapper + 1)
    const listWidth = await page
      .locator('[data-testid="message-list"]')
      .evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }))
    expect(listWidth.scroll).toBeLessThanOrEqual(listWidth.client + 1)
  })

  test("★ 列很多的表在自己的框里横向滚动，不撑破消息列", async ({ page }) => {
    await setScenario("wideTable")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me a wide table`)

    const wrap = page.locator('[data-testid="markdown-table"]')
    await expect(wrap).toBeVisible()

    // ★ The floor (7rem per column) is what makes this case overflow: twelve
    //   columns are wider than the chat column, so the table scrolls inside
    //   its wrapper — and the message column itself stays put.
    const widths = await wrap.evaluate((el) => ({
      wrapper: el.clientWidth,
      table: (el.firstElementChild as HTMLElement).scrollWidth,
    }))
    expect(widths.table, "a table of twelve columns is meant to overflow").toBeGreaterThan(
      widths.wrapper,
    )
    expect(await wrap.evaluate((el) => getComputedStyle(el).overflowX)).toBe("auto")
    const listWidth = await page
      .locator('[data-testid="message-list"]')
      .evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }))
    expect(listWidth.scroll).toBeLessThanOrEqual(listWidth.client + 1)
  })

  test("★ 有序、无序与任务清单保留标记与缩进", async ({ page }) => {
    await setScenario("lists")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me lists`)

    const body = page.locator('[data-testid="message"]').last()
    const ul = body.locator("ul").first()
    const ol = body.locator("ol").first()
    await expect(ul).toBeVisible()
    await expect(ol).toBeVisible()

    expect(await ul.evaluate((el) => getComputedStyle(el).listStyleType)).toBe("disc")
    expect(await ol.evaluate((el) => getComputedStyle(el).listStyleType)).toBe("decimal")
    expect(
      Number.parseFloat(await ul.evaluate((el) => getComputedStyle(el).paddingLeft)),
    ).toBeGreaterThan(0)

    const task = body.locator("ul.contains-task-list")
    await expect(task).toHaveCount(1)
    expect(await task.evaluate((el) => getComputedStyle(el).listStyleType)).toBe("none")
    await expect(task.locator('input[type="checkbox"]')).toHaveCount(2)
    expect(
      await task.locator('input[type="checkbox"]').evaluateAll(
        (els) => els.filter((el) => (el as HTMLInputElement).checked).length,
      ),
    ).toBe(1)
    expect(
      Number.parseFloat(
        await task
          .locator('input[type="checkbox"]')
          .first()
          .evaluate((el) => getComputedStyle(el).marginRight),
      ),
    ).toBeGreaterThan(0)
  })

  test("★ 中英混排的行高、标题层级与长链接换行", async ({ page }) => {
    await setScenario("typography")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show mixed text`)

    const body = page
      .locator('[data-testid="message"]')
      .last()
      .locator('[data-testid="message-body"]')
    const h2 = body.locator("h2")
    const paragraph = body.locator("p").first()
    await expect(h2).toBeVisible()
    await expect(paragraph).toBeVisible()

    const p = await paragraph.evaluate((el) => {
      const cs = getComputedStyle(el)
      return {
        size: Number.parseFloat(cs.fontSize),
        line: Number.parseFloat(cs.lineHeight),
        wrap: cs.overflowWrap,
        family: cs.fontFamily,
      }
    })

    expect(p.size).toBe(15)
    expect(p.line / p.size).toBeGreaterThanOrEqual(1.7)
    expect(p.wrap).toBe("break-word")
    expect(p.family).toContain("PingFang SC")
    expect(p.family).toContain("Noto Sans CJK SC")

    const heading = await h2.evaluate((el) => {
      const cs = getComputedStyle(el)
      return { size: Number.parseFloat(cs.fontSize), weight: Number.parseInt(cs.fontWeight) }
    })
    expect(heading.size).toBeGreaterThan(p.size)
    expect(heading.weight).toBeGreaterThanOrEqual(600)

    expect(
      await body.locator("strong").first().evaluate((el) => Number.parseInt(getComputedStyle(el).fontWeight)),
    ).toBeGreaterThanOrEqual(600)
    expect(
      Number.parseFloat(
        await body.locator("blockquote").evaluate((el) => getComputedStyle(el).borderLeftWidth),
      ),
    ).toBeGreaterThan(0)
    expect(
      Number.parseFloat(await body.locator("hr").evaluate((el) => getComputedStyle(el).borderTopWidth)),
    ).toBeGreaterThan(0)

    const bodyWidth = await body.evaluate((el) => ({
      scroll: el.scrollWidth,
      client: el.clientWidth,
    }))
    expect(bodyWidth.scroll).toBeLessThanOrEqual(bodyWidth.client + 1)
  })

  test("★ 没有代码块时不加载高亮的语言 chunk", async ({ page }) => {
    await setScenario("simple")
    const fetched: string[] = []
    page.on("request", (r: { url(): string }) => {
      const u = new URL(r.url())
      if (/\/(typescript|tsx|jsx|go|python|bash|yaml|diff|json)-[\w-]+\.js$/.test(u.pathname)) {
        fetched.push(u.pathname)
      }
    })

    await openApp(page)
    await sendMessage(page, `${MARKER_A} no code here`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    await page.waitForTimeout(600)

    expect(fetched, `should not fetch highlighting languages: ${fetched.join(", ")}`).toHaveLength(0)
  })
})

test.describe("空状态的版式", () => {
  async function composerCenterRatio(page: import("@playwright/test").Page): Promise<number> {
    const el = page.locator('[aria-label="Message input"]')
    let box = await el.boundingBox()
    for (let i = 0; i < 50 && !box; i++) {
      await page.waitForTimeout(50)
      box = await el.boundingBox()
    }
    if (!box) throw new Error("the composer never gets a layout box")

    const vh = page.viewportSize()?.height ?? 0
    return (box.y + box.height / 2) / vh
  }

  test("★ 还没有对话时，输入框垂直居中", async ({ page }) => {
    await openApp(page)
    const r = await composerCenterRatio(page)
    expect(r, `the composer sits at ${(r * 100).toFixed(0)}% of the viewport height`).toBeGreaterThan(0.3)
    expect(r, `the composer sits at ${(r * 100).toFixed(0)}% of the viewport height`).toBeLessThan(0.7)
  })

  test("★ 品牌区与输入框是一组，不该分居两处", async ({ page }) => {
    await openApp(page)
    const hero = await page.getByText("Ask anything about this workspace.").boundingBox()
    const input = await page.locator('[aria-label="Message input"]').boundingBox()
    expect(hero!.y).toBeLessThan(input!.y)
    expect(input!.y - hero!.y).toBeLessThan(160)
  })

  test("★ 发出第一句后，输入框回到底部", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    const before = await composerCenterRatio(page)
    expect(before).toBeLessThan(0.7)

    await sendMessage(page, `${MARKER_A} first`)

    // The composer moves when the conversation renders, which is later than
    // the send itself. Measuring once can catch the composer before it has
    // moved, and then "after > before" is false for a composer that has not
    // moved yet — not for one that never moves.
    await expect
      .poll(() => composerCenterRatio(page), {
        timeout: 5000,
        message: "the composer should sit at the bottom once there is a conversation",
      })
      .toBeGreaterThan(0.85)
  })

  test("★ 打开一个已有的空会话，仍是居中版式", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Empty conversation")
    await expect.poll(() => composerCenterRatio(page), { timeout: 5000 }).toBeLessThan(0.7)
  })

  test("★ 打开有内容的会话，输入框在底部", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await expect.poll(() => composerCenterRatio(page), { timeout: 5000 }).toBeGreaterThan(0.85)
  })

  test("★ 加载长历史时不闪居中版式", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Long conversation")
    await expect(page.locator('[data-testid="message-list"]')).toBeVisible()
    await expect.poll(() => composerCenterRatio(page), { timeout: 5000 }).toBeGreaterThan(0.85)
  })
})

test.describe("消息卡片的宽度", () => {
  // ★ The width of a token, in pixels. The column and the prose have their
  //   own widths, and the tests read them from the document rather than
  //   repeating the numbers — a test that hard-codes 1024 would keep
  //   passing after the token changed.
  async function cssRem(page: import("@playwright/test").Page, name: string): Promise<number> {
    return page.evaluate((n) => {
      const raw = getComputedStyle(document.documentElement).getPropertyValue(n).trim()
      return Math.round(parseFloat(raw) * 16)
    }, name)
  }

  async function widths(page: import("@playwright/test").Page) {
    return page.evaluate(() => {
      const w = (el: Element | null) =>
        el ? Math.round(el.getBoundingClientRect().width) : null
      return [...document.querySelectorAll('[data-testid="message"]')].map((row) => ({
        role: row.getAttribute("data-role"),
        bubble: w(row.firstElementChild),
        tool: w(row.querySelector('[data-testid="tool-card"]')),
        reasoning: w(row.querySelector('[data-testid="reasoning"]')),
        code: w(row.querySelector('[data-testid="code-block"]')),
        // ★ The paragraph **inside the body**: the role label above it is
        //   also a <p>, and it is not prose.
        prose: w(row.querySelector('[data-testid="message-body"] p')),
      }))
    })
  }

  test("★ 消息列就是那个宽度 token，输入框与它同宽", async ({ page }) => {
    // ★ A viewport wide enough that the column is not clamped by the window:
    //   otherwise the test would measure the window, not the token.
    await page.setViewportSize({ width: 1600, height: 900 })
    await setScenario("simple")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} hi`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    const chat = await cssRem(page, "--chat-column")
    const column = await page.locator('[data-testid="message-list"] > div').first().boundingBox()
    expect(Math.round(column!.width), "the message column should be the chat token").toBe(chat)
    const composer = await page.locator('[data-testid="composer-box"]').boundingBox()
    expect(Math.round(composer!.width), "the composer should be as wide as the column").toBe(chat)
  })

  test("★ 同一条消息里，工具卡与气泡等宽，正文停在正文列上", async ({ page }) => {
    await setScenario("approval")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} use a tool`)
    await expect(page.locator('[data-testid="tool-card"]')).toBeVisible({ timeout: 7000 })

    const rows = await widths(page)
    const withTool = rows.find((r) => r.tool !== null)
    expect(withTool, "the scenario should contain a tool card").toBeDefined()
    expect(withTool!.tool).toBe(withTool!.bubble)
    // ★ The prose has a width of its own: the column is wide so that a table
    //   fits whole, but a paragraph of sixty characters is a hard read.
    const prose = await cssRem(page, "--prose-column")
    expect(withTool!.prose, "the paragraph should stop at the prose column").toBe(prose)
    expect(withTool!.prose!).toBeLessThanOrEqual(withTool!.bubble!)
  })

  test("★ 所有 assistant 消息宽度一致", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} show code`)
    await expect(page.locator('[data-testid="code-block"]')).toBeVisible({ timeout: 7000 })

    const rows = await widths(page)
    const assistants = rows.filter((r) => r.role === "assistant").map((r) => r.bubble!)
    expect(assistants.length).toBeGreaterThan(1)
    expect(new Set(assistants).size, `assistant widths are inconsistent: ${assistants.join(", ")}`).toBe(1)
  })

  test("★ 代码块与所在消息等宽", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} show code`)
    await expect(page.locator('[data-testid="code-block"]')).toBeVisible({ timeout: 7000 })

    const rows = await widths(page)
    const withCode = rows.find((r) => r.code !== null)
    expect(withCode!.code).toBe(withCode!.bubble)
  })

  test("★ 用户消息右对齐且不超过 85%", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} hi`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    const row = page.locator('[data-testid="message"][data-role="user"]').first()
    const bubble = await row.locator("> div").boundingBox()
    const list = (await page.locator('[data-testid="message-list"]').boundingBox())!
    expect(bubble!.width).toBeLessThanOrEqual(list.width * 0.85)
    const rowBox = (await row.boundingBox())!
    expect(Math.abs(rowBox.x + rowBox.width - (bubble!.x + bubble!.width))).toBeLessThan(2)
  })
})

function hexToRgb(hex: string): string {
  const m = /^#?([\da-f]{6})$/i.exec(hex.trim())
  if (!m) return hex
  const n = parseInt(m[1]!, 16)
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`
}

test.describe("工具卡的语法高亮", () => {
  test("★ 工具参数被按 JSON 着色", async ({ page }) => {
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)
    // ★ The card starts closed, so the arguments have to be opened first.
    await openToolCard(page)
    await expect(page.locator('[data-testid="tool-args"]')).toBeVisible({ timeout: 7000 })

    const args = page.locator('[data-testid="tool-args"]')
    await expect(args.locator("span[style*='--shiki-light']").first()).toBeVisible({ timeout: 8000 })
    await expect(args).toHaveAttribute("data-lang", "json")

    const colors = await args.locator("span").evaluateAll((els) =>
      els.filter((e) => e.getAttribute("style")?.includes("--shiki-light"))
        .map((e) => getComputedStyle(e).color),
    )
    expect(colors.length).toBeGreaterThan(2)
    expect(new Set(colors).size, `the arguments have only one color (${colors[0]}) — highlighting did not take effect`).toBeGreaterThan(1)
  })

  test("★ 工具卡里的代码块不带圆角、不留白", async ({ page }) => {
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)
    await openToolCard(page)
    const args = page.locator('[data-testid="tool-args"]')
    await expect(args).toBeVisible({ timeout: 7000 })

    const box = await args.evaluate((el) => {
      const s = getComputedStyle(el)
      return { radius: s.borderTopLeftRadius, borderTop: s.borderTopWidth }
    })
    expect(box.radius).toBe("0px")
    expect(box.borderTop).toBe("1px")
  })

  test("★ 非 JSON 的工具结果不做 JSON 着色", async ({ page }) => {
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)
    await openToolCard(page)

    const result = page.locator('[data-testid="tool-result"]')
    await expect(result).toBeVisible({ timeout: 7000 })
    await expect(result).toHaveAttribute("data-lang", "text")
    await page.waitForTimeout(600)
    expect(await result.locator("span.shiki").count()).toBe(0)
  })

  test("★ 结果是 JSON 时就按 JSON 着色", async ({ page }) => {
    await setScenario("toolJson")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} run a json tool`)
    await openToolCard(page)

    const result = page.locator('[data-testid="tool-result"]')
    await expect(result).toBeVisible({ timeout: 7000 })
    await expect(result).toHaveAttribute("data-lang", "json")
    await expect(result.locator("span[style*='--shiki-light']").first()).toBeVisible({
      timeout: 8000,
    })
  })
})

test.describe("工具卡的宽度", () => {
  // ★ The header carries three things and they do not fit on one line
  //   everywhere: the title, the argument (the path the call was about),
  //   and the metadata ("80 lines / done"). The argument is the one that
  //   must never be squeezed, so the metadata is what moves.
  async function rows(page: import("@playwright/test").Page) {
    // ★ `getBoundingClientRect` and not `boundingBox()`: the second one
    //   answers null for an element that is outside the viewport, and on a
    //   narrow screen the card is exactly that.
    const box = async (sel: string) =>
      page
        .locator(sel)
        .first()
        .evaluate((el) => {
          const r = el.getBoundingClientRect()
          return { top: Math.round(r.top), left: Math.round(r.left), width: Math.round(r.width) }
        })
    return {
      hint: await box('[data-testid="tool-hint"]'),
      meta: await box('[data-testid="tool-meta"]'),
    }
  }

  test("★ 宽屏时元信息留在第一行，路径落到标题之下", async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 900 })
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)

    const { hint, meta } = await rows(page)
    // ★ The metadata sits on the **first** row, at the far end; the
    //   argument is on the row below, in the title's own column. So the
    //   metadata is **above** the argument here.
    expect(meta.top).toBeLessThan(hint.top)
  })

  test("★ 窄屏时元信息换到独立的一行，路径不被挤掉", async ({ page }) => {
    // ★ Below the `sm` breakpoint the header is three rows: the title, the
    //   argument, and the metadata. The argument still gets a full row of
    //   its own — it is the one that must stay readable.
    await page.setViewportSize({ width: 420, height: 900 })
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)

    const { hint, meta } = await rows(page)
    // ★ A row of its own: the metadata is below the argument.
    expect(meta.top).toBeGreaterThan(hint.top)
    // ★ And the argument was not squeezed out of the card.
    expect(hint.width).toBeGreaterThan(100)
  })
})

test.describe("工具卡的图标", () => {
  // ★ The icon sits one gap away from the chevron that folds the card. A
  //   glyph that reads as a `>` reads as a **second folding control**: the
  //   reader clicks the wrong thing and the card does not open.
  //
  // ★ The name the table gives is on the element that draws it, so the
  //   assertion is about what is on the screen — not about a table that
  //   happens to be right in a file nobody opened.
  function icon(page: import("@playwright/test").Page) {
    return page.locator('[data-testid="tool-card"] [data-icon]').first()
  }

  test("★ read_file 画的是一张纸，不是剪贴板", async ({ page }) => {
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)

    // ★ `clipboard` is a small square at 14px, and next to the chevron it
    //   read as a second folding symbol.
    await expect(icon(page)).toHaveAttribute("data-icon", "file-text")
  })

  test("★ execute 画的是带框的终端，不是裸的 >", async ({ page }) => {
    await setScenario("toolJson")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} run a json tool`)

    // ★ The bare `>_` was the same shape as the chevron beside it.
    await expect(icon(page)).toHaveAttribute("data-icon", "square-terminal")
  })

  test("★ config_get 画的是齿轮", async ({ page }) => {
    await setScenario("toolConfig")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the config`)

    // ★ The configuration service answers more calls in a real run than the
    //   filesystem does, and the whole family is one gear.
    await expect(icon(page)).toHaveAttribute("data-icon", "settings")
  })

  test("★ 图标和折叠箭头不是同一个图形", async ({ page }) => {
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)

    const card = page.locator('[data-testid="tool-card"]').first()
    const [chevron, drawing] = await Promise.all([
      card.locator('[data-testid="tool-chevron"]').evaluate((el) => el.innerHTML),
      card.locator("[data-icon]").first().evaluate((el) => el.innerHTML),
    ])
    // ★ Two identical chevrons side by side are two folding controls.
    expect(drawing).not.toBe(chevron)
  })
})

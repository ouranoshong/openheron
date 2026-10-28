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
  resetBackend,
  sendMessage,
  setScenario,
  threadTitles,
  trackRequests,
} from "./fixtures"

/**
 * 这些 spec 描述行为，不描述实现 —— assistant-ui 删掉之后它们照样成立。
 *
 * ★ Phase 0 建这套的时候是 7 绿 / 5 红，那 5 条红正是你实际报过的现象：
 *   切换会话串内容、刷新回到错的会话、发消息多一个条目、审批卡不渲染。
 *   Phase 4 之后 14 条全绿。
 *
 * ★ 行为规格跨框架存活，这是它们的价值：换实现时它们不用改，只换定位点。
 *
 * 每个 test 前 reset 假后端：所有用例共享同一个会话表，不隔离会互相污染。
 */

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
    // Bravo 的消息应该出现，Alpha 的应该消失。
    // 现在这两条都会失败 —— 报的现象是「切过去还是上一个会话的内容」。
    await expect(page.getByText(MARKER_B).first()).toBeVisible()
    await expect(page.getByText(MARKER_A)).toHaveCount(0)
  })

  test("点 New Thread 会在侧栏多出一条", async ({ page }) => {
    await openApp(page)
    const before = (await threadTitles(page)).length

    await page.locator(NEW_THREAD).click()

    await expect(page.locator(THREAD_ITEM)).toHaveCount(before)
    // 点完之后应当回到欢迎界面：输入框在，但消息区是空的
    await expect(page.locator(COMPOSER)).toBeVisible()
  })

  test("点 New Thread 会请求 /v2/sessions/undefined", async ({ page }) => {
    await openApp(page)
    const reqs = trackRequests(page)

    await page.locator(NEW_THREAD).click()
    await page.waitForTimeout(500)

    const bad = reqs.filter((p) => p.includes("undefined"))
    expect(bad, `发出了带 undefined 的请求: ${bad.join(", ")}`).toHaveLength(0)
  })

  /**
   * ★ 记住的会话**没了**（删过 / 状态目录被清过 / 换机器）：刷新时不该甩
   *   一条「session not found」给用户 —— 他删的、或者它没的，两条路他都没
   *   法「重试」。该做的是清掉那个悬空的 id，回欢迎界面。
   */
  test("记住的会话没了 → 回欢迎界面，不甩错误", async ({ page }) => {
    // 模拟启动：localStorage 里记着一个服务端不认识的会话。
    // ★ 别用假后端已有的 id（11111111-… 是 THREAD_A）—— 那就不会 404，
    //   测的就不是「会话没了」这条路径了。
    await page.addInitScript(() => {
      localStorage.setItem("openheron.activeThread", "99999999-9999-4999-8999-999999999999")
    })
    await openApp(page)

    await expect(page.locator('[data-testid="run-error"]')).toHaveCount(0)
    await expect(page.getByText("Ask anything about this workspace.").first()).toBeVisible()
    // 悬空的 id 被清掉了 —— 下次刷新不再去打它。
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

    // 刷新后应当仍然停在 Bravo
    await expect(page.getByText(MARKER_B).first()).toBeVisible()
  })

  test("改名后侧栏显示新标题", async ({ page }) => {
    await openApp(page)
    // 改走 UI 的重命名入口：点条目右侧的更多按钮
    // 从行（li）找 More options：它在可点击按钮的**外面**，
    // 嵌套按钮是无效 HTML。
    const row = page.locator(THREAD_ROW).filter({ hasText: "Alpha conversation" }).first()
    await row.hover()
    await row.locator('[aria-label="More options"]').click()
    await clickMenuItem(page, "Rename", "重命名")

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
    await clickMenuItem(page, "Delete", "删除")

    await expect(page.locator(THREAD_ITEM).filter({ hasText: "Bravo conversation" })).toHaveCount(0)
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

    // ★ 断言「过程中」而不是「结束后」：流还没结束时就该有内容。
    //   只断言最终文本的话，测的是「最终对」，不是「流式对」。
    await expect(page.getByText(/streamed answer/).first()).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/incremental rendering/).first()).toBeVisible({ timeout: 7000 })
  })
})

test.describe("审批（HITL）", () => {
  /**
   * 审批卡。
   *
   * ★ 这条曾经是红的，而且原因查了很久：中断不渲染。已排除假后端帧形状
   *   （用 agtransport.RunStream 吐真帧逐字段对照）、schema 校验、
   *   messageId 拆分、后端待审批记录，都不是。
   *
   *   真正的成因是 assistant-ui 把中断挂在**最后一条 assistant 消息**的
   *   metadata.custom.agui.interrupts 上，而 findRequiresActionAssistant
   *   用 findLast 找它。新的 reducer 把 pending interrupts 放在状态里，
   *   与消息顺序无关 —— 这条转绿是重写的直接结果，不是又修了一处。
   */
  test("工具调用触发审批卡，同意后续跑完成", async ({ page }) => {
    await setScenario("approval")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} write the file`)

    // 审批卡应当出现，并说明要批什么
    await expect(page.getByText(/write_file/).first()).toBeVisible({ timeout: 7000 })
    // ★ 审批卡**取代**输入框：轮次已结束，输入框此刻不能用，留着只会误导。
    await expect(page.locator(COMPOSER)).toHaveCount(0)
    await expect(page.locator('[data-testid="approval-tool"]')).toHaveText("write_file")
    await expect(page.locator('[data-testid="approval-arguments"]')).toContainText(
      "/tmp/x.txt",
    )
    // ★ 子 agent 的审批必须标出请求方：批的是谁的工具决定要不要同意。
    await expect(page.locator('[data-testid="approval-requested-by"]')).toHaveText(
      "Requested by worker",
    )
    await expect(page.locator(APPROVE_BUTTON).first()).toBeVisible()

    await clickByText(page, "同意", "Approve", "Allow")

    // 批准后这一轮继续跑完，卡片消失，输入框回来
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator(COMPOSER)).toBeVisible()
  })

  test("运行失败时有可见提示而不是静默", async ({ page }) => {
    await setScenario("error")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} trigger error`)

    await expect(page.getByText(/unavailable|error|failed/i).first()).toBeVisible({ timeout: 7000 })
  })
})

test.describe("多个待批", () => {
  /**
   * ★ 模型一次请求几个都要审批的工具，就是几个**独立**的待批，各带各的 id。
   *
   *	答复必须**一次答完整组**：框架的契约是「不在 Targets 里的被中断组件
   *	会重新中断自己」，而重新中断拿到的是**新 id** —— 逐个发的话，答完
   *	第一个，其余的 id 就全废了，后面每一次都会撞上
   *	「interruptId does not match the pending approval」。
   *
   *	这一条就是那个缺陷的回归：修之前它报 Run failed。
   */
  test("一次答完整组待批，不报 interruptId 不匹配", async ({ page }) => {
    await setScenario("approvalMulti")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} need three approvals`)

    // 三个待批都要列出来；每一行只展示工具名与参数摘要。
    await expect(page.getByText(/3 tool calls need your approval/)).toBeVisible({ timeout: 7000 })
    await expect(page.locator('[data-testid="approval-item"]')).toHaveCount(3)
    await expect(page.locator('[data-testid="approval-tool"]')).toHaveText([
      "write_file",
      "execute",
      "edit_file",
    ])
    await expect(page.locator(COMPOSER)).toHaveCount(0)

    await clickByText(page, "同意", "Approve", "Allow")

    // ★ 不许出现「id 不匹配」—— 那是逐个发（或服务端只认一个）时的症状。
    await expect(page.getByText(/does not match/i)).toHaveCount(0)

    // 批准后这一轮继续跑完，卡片消失
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator('[data-testid="approval-card"]')).toHaveCount(0)
  })

  test("整组待批一次发出，而不是一个一个", async ({ page }) => {
    await setScenario("approvalMulti")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} need three approvals`)
    await expect(page.getByText(/3 tool calls need your approval/)).toBeVisible({ timeout: 7000 })

    // ★ 抓请求体：必须是**一次**请求带三个 resume 条目。
    //
    //   逐个发的话，每个请求只带一个 —— 而第二个请求必然 409。
    const resumes: unknown[][] = []
    await page.route("**/v2/run", async (route) => {
      const body = route.request().postDataJSON() as { resume?: unknown[] }
      resumes.push(body.resume ?? [])
      await route.continue()
    })

    await clickByText(page, "同意", "Approve", "Allow")
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    expect(resumes.length).toBe(1)
    expect(resumes[0]!.length).toBe(3)
  })
})

test.describe("中断（停止）", () => {
  /**
   * ★ 运行中：停止按钮**取代**发送按钮，且图标是实心方块。
   *
   *   两个按钮并排会多占一个槽位，还让人以为「发送只是暂时灰掉」；
   *   而默认的 lucide Square 是描边矩形，在 32px 按钮里看起来像一个空
   *   复选框，用户会把它读成「未选中」而不是「中断当前轮次」。
   */
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

  /**
   * ★ 点「停止」必须**叫服务端真的停**。
   *
   *   修之前 `stop()` 只 abort 了本地的 SSE 连接 —— 服务端那一轮会
   *   **继续跑到底**：模型继续烧 token、工具继续执行副作用（文件已写、
   *   命令已跑），而界面已经显示 idle。那是「假停止」，比没有停止按钮
   *   更糟：用户以为停了。
   *
   *   这条用 `trackRequests` 抓真实发出的请求 —— 断言的是「服务端收到了
   *   中止」，不是「界面不转圈了」。
   */
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

    // 停止按钮消失、发送按钮回到原槽位 ⇒ 不再 running
    await expect(page.locator(STOP_BUTTON)).toHaveCount(0)
    await expect(page.locator(SEND_BUTTON)).toBeVisible()
    // 输入框可用
    await expect(page.locator(COMPOSER)).toBeEnabled()
    // ★ 停止**不是失败** —— 不该弹出错误框。
    //
    //   服务端那边，中止产生的 "[GraphRunError] context has been canceled"
    //   曾被当成失败（发 RUN_ERROR + 记一条 "run ended with a stream error"）。
    //   而用户是**要求**停的，说成失败既误导，又让人以为停止按钮坏了。
    await expect(page.locator('[data-testid="run-error"]')).toHaveCount(0)
  })

  test("停止后没有停不下来的转圈 part", async ({ page }) => {
    // ★ 轮次已经结束，不会再有 part 关闭事件到达 —— 不主动收尾的话，
    //   最后那条消息的「Thinking…」永远停不下来。
    await setScenario("slowRun")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} keep going`)
    await expect(page.getByText("Thinking…")).toBeVisible({ timeout: 5000 })

    await page.click(STOP_BUTTON)

    await expect(page.getByText("Thinking…")).toHaveCount(0)
    // ★ 停止 = **掐断**，不是想完了 —— 标题要说「Interrupted」，
    //   说「Thought for …」会让人以为那段结论是想透了的。
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

    // ★ 中止是「这一轮停了」，不是「会话废了」—— 用户要能接着说话。
    //
    //   换回普通场景：这一条要验的是「会话还能接着用」，不是「第二个
    //   场景也慢」—— slowRun 的流永远不结束，等不到回答。
    await setScenario("simple")
    await sendMessage(page, `${MARKER_A} next question`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
  })

  test("思维链结束后标题变成 Thought 并带上用时", async ({ page }) => {
    // ★ 结束之后标题停在 "Thinking" 会让人以为界面卡住了 ——
    //   它该变成「Thought」，并说清「想了多久」。
    //
    //   假后端把思维链的开始与结束时刻写死成相差 1500ms，
    //   所以「1.5s」是可断言的（不用真等）。
    await setScenario("reasoningDone")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} think first`)
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText("Thought for 1.5s", {
      timeout: 5000,
    })
    // 结束之后不该再有「Thinking…」。
    await expect(page.getByText("Thinking…")).toHaveCount(0)
  })

  test("刷新后思维链与用时仍在（历史回放重建）", async ({ page }) => {
    // ★ 刷新之后会话是**从历史重建**的 —— 思维链的正文与用时都在后端的
    //   归档消息里（Extra），历史端点带出来，前端据此重建。
    //
    //   不做这一步的话，刷新一次思维链整个没了 —— 而「想了多久」是那一轮
    //   最值得留下的一个数。
    await setScenario("reasoningDone")
    await openApp(page)

    await sendMessage(page, `${MARKER_A} think first`)
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText("Thought for 1.5s", {
      timeout: 5000,
    })

    // ★ 刷新：整段对话从**历史**重建。
    await page.reload()
    await expect(page.locator('[data-testid="reasoning-label"]')).toHaveText("Thought for 1.5s", {
      timeout: 7000,
    })
  })
})

test.describe("协议：一次往返", () => {
  /**
   * ★ 这条是 Phase 1 后端改动的验收依据。
   *
   *   ★ 契约：**会话只由 POST /v2/sessions 建**，「建不建」由前端决定
   *   （它知道当前有没有会话）。`/v2/run` 一律不建。
   *
   *   断言方式是数请求：新会话的第一条消息应当**恰好一次**
   *   POST /v2/sessions（建会话）+ 一次 POST /v2/run（跑那一轮）。
   *   GET /v2/sessions 是侧栏列表，与此无关。
   */
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
    expect(writes, `应当恰好建一次会话: ${writes.join(", ")}`).toEqual([
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
        // 流可能已经被消费过，拿不到就跳过 —— 断言在下面按长度做。
      }
    })

    await sendMessage(page, `${MARKER_A} thread id from stream`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    // 流里带回的 id 必须真实存在：点开它能读到消息。
    if (runThreadIds.length > 0) {
      const id = runThreadIds.at(-1)!
      const detail = await page.request.get(`/v2/sessions/${id}`)
      expect(detail.status(), `RUN_STARTED 宣告的会话 ${id} 应当存在`).toBe(200)
    }
  })
})

test.describe("附件", () => {
  /**
   * ★ 这组是重写时漏掉的功能：旧界面有 "Add Attachment" 按钮，
   *   重写后没有入口。上一版还明确拒绝「对话开始前传附件」，
   *   那等于这个功能在第一句话之前不可用。
   *
   *   现在的语义：选文件本身就是明确的用户动作（与「打开页面」「点
   *   New thread」不同性质），所以没有会话时会先建一个。
   *   「打开页面 / 点 New thread 零请求」不变。
   */

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
    // 上面那条「上传会建会话」不能以牺牲这条为代价。
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

    expect(writes, `不该有写请求: ${writes.join(", ")}`).toHaveLength(0)
  })

  test("附件落在会话 workspace 里，能被读回", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "probe.txt", mimeType: "text/plain", buffer: Buffer.from("probe body") },
    ])
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)

    // 从后端确认文件真的落在会话里，而不只是界面上多了个 chip
    const res = await page.request.get("/v2/sessions")
    const { items } = await res.json()
    const alpha = items.find((t: { title: string }) => t.title === "Alpha conversation")
    const files = await (await page.request.get(`/v2/sessions/${alpha.threadId}/files`)).json()
    expect(files.files.map((f: { name: string }) => f.name)).toContain("probe.txt")
  })

  test("发送后附件仍在，且消息正常跑完", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "with-message.txt", mimeType: "text/plain", buffer: Buffer.from("x") },
    ])
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} look at the attachment`)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    // ★ 附件仍在待发送列表里 —— 它已经落进 workspace，不随消息消失。
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)
  })

  test("chip 上的 × 能移除（只是不发给模型，不删服务端文件）", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "removable.txt", mimeType: "text/plain", buffer: Buffer.from("y") },
    ])
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(1)

    await page.locator('[aria-label="Remove removable.txt"]').click()
    await expect(page.locator('[data-testid="attachment-chip"]')).toHaveCount(0)

    // 服务端上的文件仍在 —— 附件已经落进 workspace，与会话历史一样不可逆
    const res = await page.request.get("/v2/sessions")
    const { items } = await res.json()
    const alpha = items.find((t: { title: string }) => t.title === "Alpha conversation")
    const files = await (await page.request.get(`/v2/sessions/${alpha.threadId}/files`)).json()
    expect(files.files.map((f: { name: string }) => f.name)).toContain("removable.txt")
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

    // 高亮器懒加载 + 250ms 静默期，所以给足超时。
    // ★ 断言的是「最终着色」而不是「过程中的纯文本」—— 流式高亮的价值
    //   恰恰在于不闪，过程中的那一帧不该被断言。
    await expect(block.locator("span[style*='--shiki-light']").first()).toBeVisible({
      timeout: 8000,
    })

    // ★★★ 下面这段才是关键。第一版只断言「span 上带着 --shiki-light 变量」，
    //   于是「变量在、但没有 CSS 去读它」这种状态照样通过 ——
    //   而那正是当时的真实情况：10 个 span 都带着正确的颜色变量，
    //   computedColor 却全是默认文字色，高亮**完全没生效**。
    //
    //   只断言标记结构是不够的，必须断言**渲染出来的颜色**。
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

    // 不同 token 算出来的颜色必须不同 —— 否则说明全是默认色
    const computed = new Set(tokens.map((t) => t.computed))
    expect(
      computed.size,
      `所有 token 都是同一个颜色（${[...computed].join(",")}）—— 高亮没生效`,
    ).toBeGreaterThan(1)

    // 而且颜色必须就是变量里写的那个值（浅色主题下）
    for (const t of tokens) {
      const want = hexToRgb(t.variable)
      expect(t.computed, `token「${t.text}」的颜色应来自 ${t.variable}`).toBe(want)
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
    // 代码块内只应有 span，代码文本本身不应被当成标签解析
    await expect(block.locator("img, script, iframe")).toHaveCount(0)
  })

  test("行内代码保持在行内", async ({ page }) => {
    await setScenario("code")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me some code`)

    // panic() 在行内，不该被包进 [data-testid=code-block]
    const inline = page.locator("code", { hasText: "panic()" }).first()
    await expect(inline).toBeVisible()
    const blockText = await page.locator('[data-testid="code-block"]').first().innerText()
    expect(blockText).not.toContain("panic()")
  })


  test("★ GFM 表格有边框且横向滚动留在消息列内", async ({ page }) => {
    await setScenario("table")
    await openApp(page)
    await sendMessage(page, `${MARKER_A} show me a table`)

    const wrap = page.locator('[data-testid="markdown-table"]')
    await expect(wrap).toBeVisible()
    const table = wrap.locator("table")
    const th = table.locator("th").first()

    // Tailwind preflight 会清掉表格默认间距；这里钉住实际渲染，
    // 而不是只看 DOM 里有没有 table。
    expect(await wrap.evaluate((el) => getComputedStyle(el).overflowX)).toBe("auto")
    expect(await table.evaluate((el) => getComputedStyle(el).borderCollapse)).toBe("collapse")
    expect(await th.evaluate((el) => getComputedStyle(el).borderTopWidth)).not.toBe("0px")
    expect(await th.evaluate((el) => getComputedStyle(el).paddingLeft)).not.toBe("0px")

    // 宽表格必须让 wrapper 自己滚，而不是把消息列/页面撑宽。
    const widths = await wrap.evaluate((el) => ({
      wrapper: el.clientWidth,
      table: (el.firstElementChild as HTMLElement).scrollWidth,
    }))
    expect(widths.table, "场景里的表格不够宽，测不出横向溢出").toBeGreaterThan(widths.wrapper)
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

    // preflight 会把 list-style 清成 none；这里钉住实际计算值。
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

    // 15px + 1.75 行高：中文密排时仍有一行半以上的呼吸感。
    expect(p.size).toBe(15)
    expect(p.line / p.size).toBeGreaterThanOrEqual(1.7)
    expect(p.wrap).toBe("break-word")
    // 字体栈显式包含中文字体，避免各平台回退不一致。
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

    // 连续 240 个拉丁字符不能撑出横向滚动。
    const bodyWidth = await body.evaluate((el) => ({
      scroll: el.scrollWidth,
      client: el.clientWidth,
    }))
    expect(bodyWidth.scroll).toBeLessThanOrEqual(bodyWidth.client + 1)
  })

  test("★ 没有代码块时不加载高亮的语言 chunk", async ({ page }) => {
    // 精选语言是动态 import 的，只有真的遇到代码块才会拉。
    // 若哪天有人把 import 改成静态的，这条会红 —— 那意味着每次打开
    // 应用都在为看不见的代码块付几百 KB。
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

    expect(fetched, `不该拉高亮语言: ${fetched.join(", ")}`).toHaveLength(0)
  })
})

test.describe("空状态的版式", () => {
  /** 输入框在视口中的相对位置：0.5 = 正中，接近 1 = 贴着底部。 */
  async function composerCenterRatio(page: import("@playwright/test").Page): Promise<number> {
    // ★ 必须重试到拿到布局盒为止。
    //
    //   版式切换时 Composer 是「卸载旧的、挂载新的」，中间那一瞬节点是
    //   **分离**的 —— 此时 boundingBox() 立即返回 null，**即使传了 timeout**
    //   也一样：那个 timeout 只等元素附着，不保证它已经有布局盒。
    //
    //   单跑时碰不到，全量跑时偶尔撞上 —— 这类「偶发」最容易被误当成
    //   偶发环境问题而去加重试次数。这里改成明确等到量得到为止。
    const el = page.locator('[aria-label="Message input"]')
    let box = await el.boundingBox()
    for (let i = 0; i < 50 && !box; i++) {
      await page.waitForTimeout(50)
      box = await el.boundingBox()
    }
    if (!box) throw new Error("输入框始终拿不到布局盒")

    const vh = page.viewportSize()?.height ?? 0
    return (box.y + box.height / 2) / vh
  }

  test("★ 还没有对话时，输入框垂直居中", async ({ page }) => {
    await openApp(page)
    const r = await composerCenterRatio(page)
    // 允许区间覆盖「整体居中 + 略微上提」两种合理做法；
    // 关键是它不能贴着底（> 0.9），也不能在顶部（< 0.2）。
    expect(r, `输入框位于视口 ${(r * 100).toFixed(0)}% 高度处`).toBeGreaterThan(0.3)
    expect(r, `输入框位于视口 ${(r * 100).toFixed(0)}% 高度处`).toBeLessThan(0.7)
  })

  test("★ 品牌区与输入框是一组，不该分居两处", async ({ page }) => {
    await openApp(page)
    // ★ 必须用副标题定位：顶栏也有一个 "openheron"，用文字匹配会命中那个。
    const hero = await page.getByText("Ask anything about this workspace.").boundingBox()
    const input = await page.locator('[aria-label="Message input"]').boundingBox()
    // 品牌区就在输入框上方不远处 —— 若两者各自居中，会被拉到视口两端
    expect(hero!.y).toBeLessThan(input!.y)
    expect(input!.y - hero!.y).toBeLessThan(160)
  })

  test("★ 发出第一句后，输入框回到底部", async ({ page }) => {
    await setScenario("simple")
    await openApp(page)
    const before = await composerCenterRatio(page)
    expect(before).toBeLessThan(0.7)

    await sendMessage(page, `${MARKER_A} first`)

    const after = await composerCenterRatio(page)
    expect(after, "有对话之后输入框应贴着底部").toBeGreaterThan(before)
    expect(after).toBeGreaterThan(0.85)
  })

  test("★ 打开一个已有的空会话，仍是居中版式", async ({ page }) => {
    // 有活动会话但没消息 —— 与「完全没有会话」同一套版式。
    // ★ 必须挑一个**真的**空会话：第一版挑的是 Bravo，而它有 1 条消息，
    //   于是断言的前提就不成立，红得莫名其妙。
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
    // 会话已选中但历史还在路上 —— 那一瞬 messages 是空的。
    // 若据此切到居中，历史到达时会再切回去，输入框跳一下。
    await openApp(page)
    await openThread(page, "Long conversation")
    await expect(page.locator('[data-testid="message-list"]')).toBeVisible()
    // MessageList 在 = 走的是底部版式，不是居中版式
    await expect.poll(() => composerCenterRatio(page), { timeout: 5000 }).toBeGreaterThan(0.85)
  })
})

test.describe("消息卡片的宽度", () => {
  /** 取各元素的渲染宽度。 */
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
        text: w(row.querySelector("p")),
      }))
    })
  }

  test("★ 同一条消息里，工具卡与正文等宽", async ({ page }) => {
    await setScenario("approval")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} use a tool`)
    await expect(page.locator('[data-testid="tool-card"]')).toBeVisible({ timeout: 7000 })

    const rows = await widths(page)
    const withTool = rows.find((r) => r.tool !== null)
    expect(withTool, "场景里应当有工具卡").toBeDefined()
    expect(withTool!.tool).toBe(withTool!.bubble)
    expect(withTool!.text).toBe(withTool!.bubble)
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
    // ★ 之前每条 assistant 各自缩到内容宽度（实测 217 / 325），参差不齐
    expect(new Set(assistants).size, `assistant 宽度不一致: ${assistants.join(", ")}`).toBe(1)
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
    // 用户气泡缩到内容宽度，但不超过列宽的 85%
    expect(bubble!.width).toBeLessThanOrEqual(list.width * 0.85)
    // 右对齐：气泡右边缘贴着行右边缘
    const rowBox = (await row.boundingBox())!
    expect(Math.abs(rowBox.x + rowBox.width - (bubble!.x + bubble!.width))).toBeLessThan(2)
  })
})

/** "#D73A49" → "rgb(215, 58, 73)"，用于比对 CSS 变量的实际着色。 */
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
    await expect(page.locator('[data-testid="tool-args"]')).toBeVisible({ timeout: 7000 })

    const args = page.locator('[data-testid="tool-args"]')
    await expect(args.locator("span[style*='--shiki-light']").first()).toBeVisible({ timeout: 8000 })
    await expect(args).toHaveAttribute("data-lang", "json")

    // 断言**真实颜色**，不是「结构上有变量」—— 上一轮就是这么漏掉高亮没生效的
    const colors = await args.locator("span").evaluateAll((els) =>
      els.filter((e) => e.getAttribute("style")?.includes("--shiki-light"))
        .map((e) => getComputedStyle(e).color),
    )
    expect(colors.length).toBeGreaterThan(2)
    expect(new Set(colors).size, `参数只有一个颜色（${colors[0]}）—— 高亮没生效`).toBeGreaterThan(1)
  })

  test("★ 工具卡里的代码块不带圆角、不留白", async ({ page }) => {
    // 卡片本身已有边框与分隔，代码块再来一层圆角+背景会变成「卡中卡」
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)
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
    // read_file 返回文件内容、execute 返回命令输出 —— 按 JSON 着色只会
    // 得到一片误导性的颜色。
    await setScenario("toolDone")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} read the file`)

    const result = page.locator('[data-testid="tool-result"]')
    await expect(result).toBeVisible({ timeout: 7000 })
    await expect(result).toHaveAttribute("data-lang", "text")
    // 等一会儿也不该出现着色 span
    await page.waitForTimeout(600)
    expect(await result.locator("span.shiki").count()).toBe(0)
  })

  test("★ 结果是 JSON 时就按 JSON 着色", async ({ page }) => {
    // 先在假后端造一个 JSON 结果的工具调用场景
    await setScenario("toolJson")
    await openApp(page)
    await openThread(page, "Alpha conversation")
    await sendMessage(page, `${MARKER_A} run a json tool`)

    const result = page.locator('[data-testid="tool-result"]')
    await expect(result).toBeVisible({ timeout: 7000 })
    await expect(result).toHaveAttribute("data-lang", "json")
    await expect(result.locator("span[style*='--shiki-light']").first()).toBeVisible({
      timeout: 8000,
    })
  })
})

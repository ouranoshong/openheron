/**
 * 动作：改状态的那些函数。
 *
 * ★ 一个 zustand store 的动作都在这里，`useStore` 也在这里创建 ——
 *   状态与动作分开后，「有哪些动作」不必翻 455 行的字面量才知道。
 */
import { create } from "zustand"

import { ApiError, api, runStream, type RunHandle, type ThreadMeta } from "@/lib/api"
import { readActiveThreadId, writeActiveThreadId } from "@/lib/active-thread"
import { closeAllOpenParts, type AgEvent } from "@/lib/events"
import { emptyConversation, type ConversationState, type Message, type TurnStatus } from "@/lib/types"

import { getCurrentRunKey, handleEvent, promote, setCurrentRunKey, withoutKey } from "./events"
import { seedFromHistory } from "./history"
import { MAIN_AGENT, type SessionEntry, type StoreState } from "./state"

/**
 * isThreadNotFound 认出「这个会话不存在」。
 *
 * ★ 走 code 而不是 error 文本：文案会变，code 不会。
 */
function isThreadNotFound(e: unknown): boolean {
  return e instanceof ApiError && e.code === "thread_not_found"
}

let run: RunHandle | null = null

export const useStore = create<StoreState>((set, get) => ({
  sessions: {},
  order: [],
  attachments: {},
  uploading: false,
  activeId: readActiveThreadId() ?? null,
  pendingAgentId: null,
  loadingList: false,
  agentFilter: null,

  setAgentFilter(agent) {
    // ★ 引用稳定：同一个值不重复 set —— 否则每次点同一个 chip 都触发
    //   一轮无谓的渲染。
    if (get().agentFilter === agent) return
    set({ agentFilter: agent })
  },

  async refreshList() {
    set({ loadingList: true, listError: undefined })
    try {
      const { items } = await api.listThreads()
      const prev = get().sessions
      // ★ 从零重建，而不是在旧表上打补丁。
      //   打补丁的话，服务端那边已经删掉的会话会永远留在侧栏里 ——
      //   表只增不减，条目越用越多。
      const sessions: Record<string, SessionEntry> = {}
      const order: string[] = []
      for (const meta of items) {
        order.push(meta.threadId)
        // 但已加载的对话状态要带过来：刷新列表不该把打开的会话内容清空。
        const old = prev[meta.threadId]
        sessions[meta.threadId] = {
          meta,
          convo: old?.convo,
          loaded: old?.loaded ?? false,
        }
      }
      set({ sessions, order, loadingList: false })

      // ★ 服务端**一个会话都没有** ⇒ localStorage 里记着的那个 id 一定是
      //   悬空的（会话被删了、状态目录被清过、换了机器）。
      //
      //   不清掉的后果：每次刷新都去打那个不存在的会话，拿到 404，
      //   界面还甩一条「session not found」—— 而用户什么都没做错。
      if (items.length === 0) {
        writeActiveThreadId("")
        set((s) => (s.activeId ? { activeId: null } : {}))
      }
    } catch (e) {
      set({ loadingList: false, listError: (e as Error).message })
    }
  },

  async openSession(id) {
    if (get().activeId === id && get().sessions[id]?.loaded) return
    writeActiveThreadId(id)
    set({ activeId: id })

    const entry = get().sessions[id]
    if (entry?.loaded) return

    // 先清空再加载：否则界面会短暂显示上一个会话的消息 —— 那正是
    // 「切换会话后显示的还是上一个会话的内容」。
    //
    // ★ 表里没有这条也照做：localStorage 里存着的会话可能已经被服务端
    //   删掉了（换机器、清理磁盘）。这时 `s.sessions[id]` 是 undefined，
    //   直接解引用会崩掉整个切换动作。
    set((s) => ({
      sessions: {
        ...s.sessions,
        [id]: {
          meta: s.sessions[id]?.meta ?? stubMeta(id),
          convo: emptyConversation(),
          loaded: false,
        },
      },
    }))

    try {
      const view = await api.getMessages(id)
      set((s) => ({
        sessions: {
          ...s.sessions,
          [id]: {
            meta: { ...(s.sessions[id]?.meta ?? stubMeta(id)), state: view.state },
            convo: seedFromHistory(view.messages, view.pendingApprovals, view.usage),
            loaded: true,
          },
        },
      }))
    } catch (e) {
      // ★ 会话**没了**（被删了 / 状态目录被清过 / 换机器）：清掉记住的 id，
      //   回到欢迎界面。
      //
      //   「刷新回到上一个对话」的前提是那个对话还在；它不在了，这条规则
      //   就不适用 —— 而把 404 原样摔给用户毫无用处：他删的，或者它没的，
      //   两条路他都没法「重试」。
      if (isThreadNotFound(e)) {
        writeActiveThreadId("")
        set((s) => ({
          sessions: withoutKey(s.sessions, id),
          activeId: s.activeId === id ? null : s.activeId,
        }))
        return
      }
      set((s) => ({
        sessions: {
          ...s.sessions,
          [id]: { meta: s.sessions[id]?.meta ?? stubMeta(id), convo: emptyConversation(), loaded: true },
        },
      }))
      get().loadError(id, (e as Error).message)
    }
  },

  startNewThread() {
    // ★ 只是回到欢迎界面，不碰服务端也不碰 localStorage。
    //   清 localStorage 的话刷新页面就回不到上一个对话了。
    set({ activeId: null })
  },

  /**
   * switchAgent 把活动 agent 落到服务端（已有会话）或暂存（欢迎页）。
   *
   * ★ 不做乐观更新：切换会被 409 拒绝（运行中 / 待审批）。先写界面再回滚
   *   的话，用户会看到 task 闪一下再变回 main，而拒绝原因还没地方显示。
   */
  async switchAgent(agentId) {
    const id = get().activeId
    if (!id) {
      set({ pendingAgentId: agentId })
      return
    }
    const meta = await api.switchAgent(id, agentId)
    set((s) => {
      const entry = s.sessions[id]
      if (!entry) return {}
      return {
        sessions: { ...s.sessions, [id]: { ...entry, meta: { ...entry.meta, ...meta } } },
      }
    })
  },

  async renameSession(id, title) {
    const prev = get().sessions[id]?.meta.title
    // 乐观更新：改名是本地可见的，等网络回来会闪一下。
    set((s) => ({
      sessions: {
        ...s.sessions,
        [id]: { ...s.sessions[id]!, meta: { ...s.sessions[id]!.meta, title } },
      },
    }))
    try {
      await api.renameThread(id, title)
    } catch (e) {
      if (prev !== undefined) {
        set((s) => ({
          sessions: {
            ...s.sessions,
            [id]: { ...s.sessions[id]!, meta: { ...s.sessions[id]!.meta, title: prev } },
          },
        }))
      }
      throw e
    }
  },

  async deleteSession(id) {
    const snapshot = get()
    set((s) => {
      const sessions = { ...s.sessions }
      delete sessions[id]
      return {
        sessions,
        order: s.order.filter((x) => x !== id),
        // 删掉的正是在看的那个 → 回到欢迎界面。
        activeId: s.activeId === id ? null : s.activeId,
      }
    })
    if (snapshot.activeId === id) writeActiveThreadId("")
    try {
      await api.deleteThread(id)
    } catch (e) {
      await get().refreshList()
      throw e
    }
  },

  /**
   * addFiles 上传附件到当前会话的 workspace。
   *
   * ★ 没有会话时**先建一个**。
   *
   *   上一版直接拒绝：「No session yet — send a message before attaching
   *   files」。但那等于这个功能在对话开始前不可用 —— 而选文件本身就是
   *   明确的用户动作，与「打开页面」「点 New thread」不同性质。
   *
   *   代价：用户选了文件又放弃，会留下一个只含文件的空会话。它不是垃圾
   *   —— workspace 里有内容，且侧栏点进去能看到。相较功能完全不可用，
   *   这个代价可以接受。
   *
   *   「打开页面 / 点 New thread 零请求」这条不变：只有真的选了文件才会
   *   建会话。
   */
  async addFiles(files) {
    if (files.length === 0 || get().uploading) return
    set({ uploading: true })
    try {
      const id = await ensureSession()
      const res = await api.uploadFiles(id, files)
      set((s) => ({
        attachments: {
          ...s.attachments,
          [id]: [...(s.attachments[id] ?? []), ...res.files],
        },
      }))
    } catch (e) {
      const target = get().activeId
      if (target) get().loadError(target, (e as Error).message)
      throw e
    } finally {
      set({ uploading: false })
    }
  },

  /**
   * removeFile 从待发送列表里去掉一个附件。
   *
   * ★ 只是「这一轮不发它」，不删服务端上的文件 —— 附件已经落进 workspace，
   *   和会话历史一样是不可逆的一部分。compose 阶段的移除不需要回滚服务端。
   */
  async removeFile(sessionId, name) {
    set((s) => ({
      attachments: {
        ...s.attachments,
        [sessionId]: (s.attachments[sessionId] ?? []).filter((f) => f.name !== name),
      },
    }))
  },

  async send(text) {
    const trimmed = text.trim()
    if (!trimmed) return
    if (run) return

    const { activeId, sessions, pendingAgentId } = get()
    // 新会话：先在表里占位，拿到服务端 id 之后再并进去。
    const localKey = activeId ?? `pending:${Date.now()}`
    // ★ 根 agent 归服务端定：只有用户**明确选过**才把它交上去，没选就让
    //   服务端按默认根（main）定。
    //
    //   前端替服务端猜一个（写死 main）等于把「默认根」按死在界面上 ——
    //   服务端改口时它也不算数。
    const requested = activeId
      ? sessions[activeId]?.meta.agentId || MAIN_AGENT
      : (pendingAgentId ?? undefined)
    // 占位元数据要有个名字可显示：没选时先按 main 摆着，服务端回包后
    // 会换成它真正的根。
    const agentId = requested ?? MAIN_AGENT
    const placeholder: SessionEntry = {
      meta: {
        threadId: localKey,
        title: "New Chat",
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        messageCount: 0,
        state: "running",
        hasPendingApproval: false,
        agentId,
      },
      convo: appendLocalUserMessage(sessions[localKey]?.convo ?? emptyConversation(), trimmed),
      loaded: true,
    }
    set((s) => ({
      sessions: { ...s.sessions, [localKey]: placeholder },
      order: s.order.includes(localKey) ? s.order : [localKey, ...s.order],
    }))
    setCurrentRunKey(localKey)

    // ★ **会话由前端决定建不建**：服务端只认「有没有 threadId」，而那是
    //   **结果**不是**决定** —— 「建不建」这件事得有归属，归属在知道
    //   当前有没有会话的那一端，也就是这里。
    //
    //   有会话 → 复用；没有 → 先 POST /v2/sessions 换一个 id 再跑。
    //   顺带把暂存的 agent 落盘（建会话时定根，省掉一次 PATCH）。
    let threadId = activeId
    if (!threadId) {
      let created: ThreadMeta
      try {
        created = await api.createThread(requested)
      } catch (e) {
        // 建会话失败 → 界面说清楚，别留一个假的会话条目。
        run = null
        setCurrentRunKey(null)
        get().loadError(localKey, `create session failed: ${(e as Error).message}`)
        return
      }
      threadId = created.threadId
      // 用真实 id 换掉占位 —— 之后的事件都写进真键。
      //
      // ★ 连根 agent 一起换：服务端定的根才是这个会话的根，前端猜的那个
      //   只是占位（没选时先摆 main，服务端给的才算数）。
      promote(set, get, localKey, threadId, created.agentId)
      setCurrentRunKey(threadId)
    }

    run = runStream(
      {
        threadId,
        query: trimmed,
      },
      {
        onEvent: (ev) => handleEvent(set, get, localKey, ev as AgEvent),
        onError: (message) => {
          run = null
          const k = getCurrentRunKey() ?? localKey
          setCurrentRunKey(null)
          get().loadError(k, message)
        },
        onDone: () => {
          run = null
          setCurrentRunKey(null)
          void get().refreshList()
        },
      },
    )
  },

  /**
   * answer 把一组待批的答复**一次**发出去。
   *
   * ★ 必须是**一次**。
   *
   *	服务端要的是「一次答完整组」：框架的契约是「不在 Targets 里的被
   *	中断组件会重新中断自己」，而重新中断拿到的是**新的 id**（框架每次
   *	都发新的 uuid）。所以逐个发的话，答完第一个，其余的 id 就全废了
   *	—— 后面每一次都会撞上「interruptId does not match the pending
   *	approval」。
   *
   *	协议两端本来就是为「一次答多个」设计的：AG-UI 的 resume 是数组，
   *	框架的 ResumeParams.Targets 是 map。
   */
  async answer(interrupts, approved, reason) {
    if (run) return
    const id = get().activeId
    if (!id) return
    if (interrupts.length === 0) return

    setCurrentRunKey(id)
    set((st) => {
      const entry = st.sessions[id]
      if (!entry?.convo?.retry) return {}
      return {
        sessions: {
          ...st.sessions,
          [id]: { ...entry, convo: { ...entry.convo!, retry: undefined } },
        },
      }
    })
    run = runStream(
      {
        threadId: id,
        resume: interrupts.map((i) => ({ interruptId: i.id, approved, reason })),
      },
      {
        onEvent: (ev) => handleEvent(set, get, id, ev as AgEvent),
        onError: (message) => {
          run = null
          setCurrentRunKey(null)
          get().loadError(id, message)
        },
        onDone: () => {
          run = null
          setCurrentRunKey(null)
          void get().refreshList()
        },
      },
    )
  },

  /**
   * stop 中止当前轮次。
   *
   * ★ 必须**叫服务端真的停**。
   *
   *	只断开本地 SSE 只是让浏览器不再收 —— 服务端那一轮会**继续跑到底**：
   *	模型继续烧 token、工具继续执行副作用（文件已写、命令已跑），
   *	而界面已经显示 idle。那是「假停止」，比没有停止按钮更糟：
   *	用户以为停了。
   *
   *	服务端的中止语义见 api-design.md §8.5：`Abort` **留** checkpoint
   *	（副作用可能已经发生，删掉就等于假装什么都没发生）；要「干净中止」
   *	得走 `Cleanup`，那是另一个入口。
   *
   * ★ 本地先断、界面立刻回可输入状态 —— 不能等服务端的往返
   *   （`Abort` 里要 `loop.Wait()`，可能有一小段时间）。
   */
  async stop() {
    const id = get().activeId
    if (!id) return

    run?.abort()
    run = null
    setCurrentRunKey(null)
    set((s) => {
      const entry = s.sessions[id]
      if (!entry) return s
      const convo = entry.convo ?? emptyConversation()
      return {
        sessions: {
          ...s.sessions,
          [id]: {
            ...entry,
            // ★ 收尾所有还开着的 part：轮次已经结束，不会再有 part 关闭
            //   事件到达 —— 不收的话最后那条消息的转圈永远停不下来。
            //
            //   interrupted：用户按了停止 = 思维链**被掐断**，不是想完了 ——
            //   标题要说「Interrupted after 2.1s」而不是「Thought for 2.1s」。
            convo: {
              ...closeAllOpenParts(convo, { interrupted: true }),
              status: "idle" as TurnStatus,
            },
          },
        },
      }
    })

    try {
      await api.abortRun(id)
    } catch (e) {
      // ★ 中止失败要**出声**。否则界面显示 idle，而那一轮其实还在跑 ——
      //   那正是这个函数要消灭的那种「看起来停了其实没停」。
      get().loadError(id, `abort failed: ${(e as Error).message}`)
    }
  },

  applyEvent(id, ev) {
    handleEvent(set, get, id, ev)
  },

  loadError(id, message) {
    set((s) => {
      const entry = s.sessions[id]
      if (!entry) return s
      return {
        sessions: {
          ...s.sessions,
          [id]: {
            ...entry,
            convo: { ...(entry.convo ?? emptyConversation()), status: "error", error: { code: "unknown", message } },
          },
        },
      }
    })
  },
}))

/**
 * ensureSession 返回当前会话 id，没有就建一个。
 *
 * ★ 单独一个函数而不是 store action：附件上传需要**服务端**的 id，
 *   而 `pending:<ts>` 那个本地占位上传不了 —— 得先换成真的。
 *
 * ★ 为什么附件要顺带建会话：上一版直接拒绝（「No session yet — send a
 *   message before attaching files」），那等于这个功能在对话开始前不可用。
 *   选文件本身就是明确的用户动作，与「打开页面」「点 New thread」不同性质。
 *
 *   代价：选了文件又放弃会留下一个只含文件的会话。它不是垃圾 ——
 *   workspace 里有内容，点进去能看到。相较功能完全不可用可以接受。
 *
 *   「打开页面 / 点 New thread 零请求」不变：只有真选了文件才建会话。
 */
async function ensureSession(): Promise<string> {
  const current = useStore.getState().activeId
  if (current !== null) return current

  // ★ 带上用户暂存的根 agent（没选过就不带，交给服务端的默认根 main）。
  //   漏掉它等于把用户刚做的选择扔掉 —— 会话的根在创建时就定了。
  const created = await api.createThread(useStore.getState().pendingAgentId ?? undefined)
  const id = created.threadId
  useStore.setState((s) => ({
    sessions: { ...s.sessions, [id]: { meta: created, convo: emptyConversation(), loaded: true } },
    order: [id, ...s.order],
    activeId: id,
  }))
  writeActiveThreadId(id)
  return id
}

/** stubMeta 给表里还没有的会话一条占位元数据。 */
function stubMeta(id: string): ThreadMeta {
  return {
    threadId: id,
    title: "New Chat",
    createdAt: "",
    updatedAt: "",
    messageCount: 0,
    state: "idle",
    hasPendingApproval: false,
    agentId: MAIN_AGENT,
  }
}

/** 本地先插一条 user 消息，让输入框立刻清空、内容立刻可见。 */
function appendLocalUserMessage(convo: ConversationState, text: string): ConversationState {
  const msg: Message = {
    id: `local:${Date.now()}`,
    role: "user",
    parts: [{ kind: "text", id: "t", text, complete: true }],
  }
  return { ...convo, messages: [...convo.messages, msg], status: "running", error: undefined }
}

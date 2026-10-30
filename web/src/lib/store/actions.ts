import { create } from "zustand"

import {
  ApiError,
  api,
  runStream,
  type CompactionResult,
  type RunHandle,
  type RunsView,
  type ThreadMeta,
} from "@/lib/api"
import { readActiveThreadId, writeActiveThreadId } from "@/lib/active-thread"
import { closeAllOpenParts, type AgEvent } from "@/lib/events"
import { emptyConversation, type ConversationState, type Message, type TurnStatus } from "@/lib/types"

import { getCurrentRunKey, handleEvent, promote, setCurrentRunKey, withoutKey } from "./events"
import { seedFromHistory } from "./history"
import { MAIN_AGENT, type SessionEntry, type StoreState } from "./state"

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
    if (get().agentFilter === agent) return
    set({ agentFilter: agent })
  },

  async refreshList() {
    set({ loadingList: true, listError: undefined })
    try {
      const { items } = await api.listThreads()
      const prev = get().sessions
      const sessions: Record<string, SessionEntry> = {}
      const order: string[] = []
      for (const meta of items) {
        order.push(meta.threadId)
        const old = prev[meta.threadId]
        sessions[meta.threadId] = {
          meta,
          convo: old?.convo,
          loaded: old?.loaded ?? false,
        }
      }
      set({ sessions, order, loadingList: false })

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
            convo: seedFromHistory(view.messages, view.pendingApprovals, view.usage, view.state),
            loaded: true,
          },
        },
      }))
    } catch (e) {
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
    set({ activeId: null })
  },

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
    const localKey = activeId ?? `pending:${Date.now()}`
    const requested = activeId
      ? sessions[activeId]?.meta.agentId || MAIN_AGENT
      : (pendingAgentId ?? undefined)
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

    let threadId = activeId
    if (!threadId) {
      let created: ThreadMeta
      try {
        created = await api.createThread(requested)
      } catch (e) {
        run = null
        setCurrentRunKey(null)
        get().loadError(localKey, `create session failed: ${(e as Error).message}`)
        return
      }
      threadId = created.threadId
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
        onError: (message, code) => {
          run = null
          const k = getCurrentRunKey() ?? localKey
          setCurrentRunKey(null)
          if (code === "session_busy") {
            // ★ The session has a live turn that this surface does not own.
            //   The message never reached the server, so the optimistic
            //   bubble must go back — otherwise the transcript claims a
            //   message that was never sent.
            get().markBusy(k, message)
            return
          }
          // ★ The connection died, not the run: the server archives the
          //   turn, not this stream. So ask the server whether the run is
          //   still going and come back to it if it is.
          void get().reattach(k, message)
        },
        onDone: () => {
          run = null
          setCurrentRunKey(null)
          // ★ The stream ended without a terminal frame: the connection
          //   died, not the run. The server archives the turn, not the
          //   stream — so come back to the run instead of losing the
          //   answer. A run that finished (or failed) left the
          //   conversation out of "running", and those need no rescue.
          if (get().sessions[threadId]?.convo?.status === "running") {
            void get().reattach(threadId, "the connection dropped before the run finished")
            return
          }
          void get().refreshList()
        },
      },
    )
  },

  // reattach comes back to a run after the connection dropped.
  //
  // ★ The run outlives the connection: the server archives the turn, not
  //   this stream. So a broken stream does not mean a lost turn — it means
  //   this surface can no longer see it. The scan says whether the run is
  //   still going, and that answer decides what happens next:
  //
  //   - still going → attach to it, and the rest of the turn renders;
  //   - over        → the transcript is the truth, so load it;
  //   - unknown     → report the error, because there is nothing better
  //     to say.
  async reattach(id: string, message: string) {
    const entry = get().sessions[id]
    const runId = entry?.convo?.runId
    if (!runId) {
      get().loadError(id, message)
      return
    }

    let runs: RunsView
    try {
      runs = await api.listRuns(id)
    } catch {
      get().loadError(id, message)
      return
    }
    const live = runs.items.find(
      (r) => r.runId === runId && (r.state === "running" || r.state === "starting"),
    )
    if (!live) {
      // ★ The run is over. The transcript holds the finished turn, and it
      //   is the only truth about what the session contains.
      await get().reloadHistory(id)
      return
    }

    setCurrentRunKey(id)
    run = runStream(
      { threadId: id, runId },
      {
        onEvent: (ev) => get().applyEvent(id, ev as AgEvent),
        onError: (msg, code) => {
          run = null
          setCurrentRunKey(null)
          if (code === "run_not_live") {
            // ★ The run ended between the scan and the attach. The
            //   transcript has the answer.
            void get().reloadHistory(id)
            return
          }
          get().loadError(id, msg)
        },
        onDone: () => {
          run = null
          setCurrentRunKey(null)
          void get().refreshList()
        },
      },
    )
  },

  // reloadHistory re-reads the session's transcript.
  //
  // ★ It is what a client does when it learns a run is over: the
  //   transcript holds the finished turn, so loading it is the honest way
  //   to end the story — and it beats reporting a failure the user cannot
  //   act on.
  async reloadHistory(id: string) {
    try {
      const view = await api.getMessages(id)
      set((s) => ({
        sessions: {
          ...s.sessions,
          [id]: {
            meta: { ...(s.sessions[id]?.meta ?? stubMeta(id)), state: view.state },
            convo: seedFromHistory(view.messages, view.pendingApprovals, view.usage, view.state),
            loaded: true,
          },
        },
      }))
    } catch {
      // The history is unavailable; the conversation keeps what it has.
    }
  },

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
            convo: {
              ...closeAllOpenParts(convo, { interrupted: true }),
              status: "idle" as TurnStatus,
              // ★ The turn this surface did not own is now stopped, so the
              //   session is free again and the busy line must go.
              busy: false,
            },
          },
        },
      }
    })

    try {
      await api.abortRun(id)
    } catch (e) {
      get().loadError(id, `abort failed: ${(e as Error).message}`)
    }
  },

  async compact() {
    const id = get().activeId
    if (!id) return

    set((s) => {
      const entry = s.sessions[id]
      if (!entry) return s
      const convo = entry.convo ?? emptyConversation()
      return {
        sessions: {
          ...s.sessions,
          [id]: {
            ...entry,
            convo: { ...convo, notice: "Compacting the conversation…" },
          },
        },
      }
    })

    let res: CompactionResult
    try {
      res = await api.compact(id)
    } catch (e) {
      get().clearNotice(id)
      get().loadError(id, `compact failed: ${(e as Error).message}`)
      return
    }
    get().clearNotice(id)

    // The boundary goes in through the same path as the live one: the
    // divider is the divider, however the compaction was asked for.
    get().applyEvent(id, {
      type: "CUSTOM",
      name: "context_compacted",
      value: {
        agent: get().sessions[id]?.meta.agentId,
        at: res.at,
        superseded: res.superseded,
        savedTokens: res.savedTokens,
      },
    })

    // The occupancy is now the size of the summary, not the size of the
    // request that filled the context. The meter says "≈" for exactly this
    // kind of number.
    set((s) => {
      const entry = s.sessions[id]
      const convo = entry?.convo
      const agent = entry?.meta.agentId
      const usage = convo?.context?.[agent]
      if (!entry || !convo || !usage) return s
      return {
        sessions: {
          ...s.sessions,
          [id]: {
            ...entry,
            convo: {
              ...convo,
              context: {
                ...convo.context,
                [agent]: { ...usage, usedTokens: res.usedTokens },
              },
            },
          },
        },
      }
    })
  },

  clearNotice(id) {
    set((s) => {
      const entry = s.sessions[id]
      if (!entry?.convo?.notice) return s
      return {
        sessions: {
          ...s.sessions,
          [id]: {
            ...entry,
            convo: { ...entry.convo, notice: undefined },
          },
        },
      }
    })
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

  // markBusy records that the session has a live turn this surface does not
  // own.
  //
  // ★ The optimistic user message is rolled back: the server refused the
  //   run, so the message was never sent, and leaving it on screen would
  //   make the transcript claim something that did not happen.
  markBusy(id, message) {
    set((s) => {
      const entry = s.sessions[id]
      if (!entry) return s
      const convo = entry.convo ?? emptyConversation()
      return {
        sessions: {
          ...s.sessions,
          [id]: {
            ...entry,
            convo: {
              ...convo,
              messages: convo.messages.filter((m) => !m.id.startsWith(LOCAL_MESSAGE_PREFIX)),
              status: "running",
              busy: true,
              error: { code: "session_busy", message },
            },
          },
        },
      }
    })
  },
}))

async function ensureSession(): Promise<string> {
  const current = useStore.getState().activeId
  if (current !== null) return current

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

// LOCAL_MESSAGE_PREFIX marks a user bubble that the client added on its own,
// before the server accepted it.
//
// ★ The prefix is what makes a rollback possible: a refused run must take
//   the optimistic bubble back with it, and only the client's own bubbles
//   can be identified that way.
const LOCAL_MESSAGE_PREFIX = "local:"

function appendLocalUserMessage(convo: ConversationState, text: string): ConversationState {
  const msg: Message = {
    id: `${LOCAL_MESSAGE_PREFIX}${Date.now()}`,
    role: "user",
    parts: [{ kind: "text", id: "t", text, complete: true }],
  }
  return { ...convo, messages: [...convo.messages, msg], status: "running", error: undefined }
}

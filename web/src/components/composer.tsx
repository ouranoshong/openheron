import { Loader2Icon, PaperclipIcon, XIcon } from "lucide-react"
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"

import { AgentPicker } from "@/components/agent-picker"
import { SkillPicker } from "@/components/skill-picker"
import { ComposerMeta } from "@/components/composer-meta"
import { ComposerPopup, type PopupItem } from "@/components/composer-popup"
import { StopButton } from "@/components/message-list"
import { api, type ProjectFile, type SkillView, type SlashCommand } from "@/lib/api"
import { selectableAgents } from "@/lib/agent-info"
import {
  actionCommandFor,
  agentCommandFor,
  filterCommands,
  loadCommands,
  skillCommandFor,
} from "@/lib/commands"
import { loadSkills } from "@/lib/skill-info"
import {
  applyTrigger,
  findTrigger,
  mentionInsertion,
  removeTrigger,
  type TriggerToken,
} from "@/lib/composer-triggers"
import { useSettings } from "@/lib/settings-store"
import {
  selectActiveAgentId,
  selectAttachments,
  selectStatus,
  useStore,
} from "@/lib/store"
import { useAgentBadge } from "@/lib/use-agent-badge"

interface MentionState {
  /** Query the cached items belong to; null = nothing loaded yet. */
  query: string | null
  items: ProjectFile[]
  error?: string
}

/**
 * 输入区。
 *
 * ★ Enter 发送、Shift+Enter 换行 —— 聊天界面的通用约定，不用问用户。
 *
 * ★ 高度自动增长但封顶：不限高的话粘贴一大段会把整屏挤走。
 *
 * ★ 有审批挂起时禁发：AG-UI 的语义是「有待答复的中断时不能开新一轮」，
 *   而服务端 Resume 路径是按 interruptId 找原会话的。放着不管的表现是
 *   点发送没反应，用户完全不知道原因。
 *
 * ★ 附件只是「提前把文件放进会话 workspace」，消息体里不带它们 ——
 *   模型通过工具读那个目录（见 openheronrt/loop.go 把 sess.Workspace()
 *   喂进 prompt 的那处）。所以发送时不需要做任何附件相关的处理。
 *
 * ★ @ 与 / 的解析都在 lib/composer-triggers.ts 与 lib/commands.ts，
 *   这里只把状态接到纯函数上。组件里不再藏一套 caret 规则。
 */
/**
 * Composer 输入区。
 *
 * `inset` 只管外边距：底部版式要 `px-4 pb-3`，而**居中**版式由外层
 * 负责留白与定宽 —— 两边各留一份会让居中的那一组整体偏下。
 */
export function Composer({ inset = true }: { inset?: boolean }) {
  const send = useStore((s) => s.send)
  const status = useStore(selectStatus)
  const busy = status === "running"
  const blocked = status === "awaiting-approval"

  const [text, setText] = useState("")
  const [trigger, setTrigger] = useState<TriggerToken | null>(null)
  const [mention, setMention] = useState<MentionState>({ query: null, items: [] })
  const [commands, setCommands] = useState<SlashCommand[] | null>(null)
  const [active, setActive] = useState(0)
  // Agent 选择器的本地状态：它只属于输入区，不进 store —— 与 settings 那种
  // 「打开后占据整个界面」的状态不同。
  const [picker, setPicker] = useState<{ open: boolean; filter?: string; error?: string }>({
    open: false,
  })
  // Skill 选择器的本地状态：与 agent 选择器一样只属于输入区 ✅。
  const [skillPicker, setSkillPicker] = useState<{ open: boolean; filter?: string; error?: string }>({
    open: false,
  })
  // skill 清单：选择器要它，「名字对不上」时也要它来判断。
  //
  // ★ 挂在组件上而不是 store：它是**只读**的配置视图，与 agent 拓扑一个
  //   道理（见 lib/skill-info.ts 的缓存说明）。
  const [skills, setSkills] = useState<SkillView[]>([])
  useEffect(() => {
    let alive = true
    void loadSkills()
      .then((list) => {
        if (alive) setSkills(list)
      })
      .catch(() => {
        // 清单拿不到不是错误：选择器会显示「没有匹配的 skill」✅。
      })
    return () => {
      alive = false
    }
  }, [])

  const fileRef = useRef<HTMLInputElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)
  const rowRef = useRef<HTMLDivElement>(null)
  const composingRef = useRef(false)

  // ★ 走 store 的 selector：内联写 `?? []` 每次渲染都造一个新数组，
  //   zustand 判定快照一直在变 → 无限重渲染 → 白屏。
  const attachments = useStore(selectAttachments)
  const activeId = useStore((s) => s.activeId)
  const uploading = useStore((s) => s.uploading)
  const settingsOpen = useSettings((s) => s.open)
  const activeAgentId = useStore(selectActiveAgentId)
  const agent = useAgentBadge(settingsOpen, activeAgentId)
  const agentChoices = agent.view ? selectableAgents(agent.view) : []
  const switchBlocked = busy || blocked
  const focusAfterSettings = useRef(false)
  // pickerWasOpen 记住选择器「开过」——关掉的那一刻 open 已经是 false，
  // 只看它会漏掉「开 → 关」这个跃迁。理由见下面的 effect。
  const pickerWasOpen = useRef(false)
  const addFiles = useStore((s) => s.addFiles)
  const removeFile = useStore((s) => s.removeFile)

  // 10 个视觉行封顶：不做的话粘贴长文会把整屏挤走。line-height 走
  // computed style，字号/主题变了不用同步改常量。
  const resize = useCallback(() => {
    const el = taRef.current
    if (!el) return
    const cs = getComputedStyle(el)
    const lineHeight = parseFloat(cs.lineHeight) || 20
    const padding = (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0)
    const maxHeight = lineHeight * 10 + padding
    el.style.height = "auto"
    el.style.height = `${Math.min(el.scrollHeight, maxHeight)}px`
    el.style.overflowY = el.scrollHeight > maxHeight ? "auto" : "hidden"
  }, [])

  useLayoutEffect(() => {
    resize()
  }, [text, resize])

  // 只按宽度变化重算：高度变化也会触发 observer，不设防会自激。
  useEffect(() => {
    const row = rowRef.current
    if (!row) return
    let lastWidth = 0
    const ro = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width ?? 0
      if (width === lastWidth) return
      lastWidth = width
      resize()
    })
    ro.observe(row)
    return () => ro.disconnect()
  }, [resize])

  const closeTrigger = () => {
    setTrigger(null)
    setActive(0)
    setMention({ query: null, items: [] })
  }

  const syncTrigger = (value: string, caret: number) => {
    if (composingRef.current) return
    const next = findTrigger(value, caret)
    setTrigger((prev) => {
      if (prev === null && next === null) return prev
      if (
        prev &&
        next &&
        prev.kind === next.kind &&
        prev.start === next.start &&
        prev.end === next.end &&
        prev.query === next.query
      ) {
        return prev
      }
      return next
    })
    // A new query (or a moved caret) always starts at the first option.
    setActive(0)
  }

  const triggerKind = trigger?.kind
  const mentionQuery = trigger?.kind === "mention" ? trigger.query : null

  // Commands are static; fetch once per tab and cache in lib/commands.ts.
  useEffect(() => {
    if (triggerKind !== "command") return
    let alive = true
    void loadCommands()
      .then((list) => {
        if (alive) setCommands(list)
      })
      .catch(() => {
        if (alive) setCommands([])
      })
    return () => {
      alive = false
    }
  }, [triggerKind])

  // Debounce file search; abort the previous request so a fast typist cannot
  // receive an older result after a newer one.
  useEffect(() => {
    if (mentionQuery === null) return
    const controller = new AbortController()
    let alive = true
    const timer = window.setTimeout(() => {
      api
        .listProjectFiles(mentionQuery, controller.signal)
        .then((res) => {
          if (alive) setMention({ query: mentionQuery, items: res.files })
        })
        .catch((err: unknown) => {
          if (!alive) return
          setMention({
            query: mentionQuery,
            items: [],
            error: err instanceof Error ? err.message : "Failed to load files",
          })
        })
    }, 120)
    return () => {
      alive = false
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [mentionQuery])

  // Opening settings from the composer should return focus to the composer
  // when the dialog closes.
  //
  // ★ 从**侧栏**打开时没有这层处理：那边也没有 `DialogTrigger`（设置对话框
  //   由 store 控制，不挂在触发按钮上），所以 Radix 无处可还原，焦点会落到
  //   <body>。那是个独立的缺口 —— 修它要先决定「从侧栏开、⌘, 开、composer
  //   开」三种情况下焦点各回哪儿，不是这里顺手能定的。
  useEffect(() => {
    if (settingsOpen || !focusAfterSettings.current) return
    focusAfterSettings.current = false
    requestAnimationFrame(() => {
      const el = taRef.current
      if (!el) return
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
    })
  }, [settingsOpen])

  // ★ 选择器关掉后把焦点交回输入框。
  //
  //	Radix Dialog 的焦点恢复依赖「打开它的那个 trigger」—— 而选择器是
  //	**程序化**打开的（setPicker({open:true})），没有 trigger。所以 Radix
  //	无处可还原，焦点落到 <body>：用户接着敲字会发现什么都没进去，
  //	而命令早就执行完了 —— 看起来像「命令没生效」。
  //
  //	用 ref 记住「开过」而不是只看 open 的真假：关掉的那一刻 open 已经是
  //	false 了，只看它会漏掉「开 → 关」这个跃迁。
  useEffect(() => {
    if (picker.open) {
      pickerWasOpen.current = true
      return
    }
    if (!pickerWasOpen.current) return
    pickerWasOpen.current = false
    const focusComposer = () => {
      // ★ 用 taRef 就够：选择器是 composer 的子节点，composer 卸载时这个
      //   effect 根本不会跑（picker 也不会「关掉」—— 它跟着一起没了）。
      //   所以这里不存在「ref 指向已移除的节点」的情况。
      const el = taRef.current
      if (!el) return
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
    }
    // ★ 先**同步**恢复：选择器一消失就该把焦点交回去 ——
    //   否则「关掉选择器后接着敲字」有一个一帧的窗口，敲进去的字会丢。
    focusComposer()
    // 再补一帧：Radix 的 FocusScope 在卸载时可能把焦点收回去，
    //   而它比 effect 晚 —— 所以同一件事做两次，第二次保证结果。
    requestAnimationFrame(focusComposer)
  }, [picker.open])

  const runAction = (action: NonNullable<SlashCommand["action"]>) => {
    switch (action) {
      case "new_thread": {
        useStore.getState().startNewThread()
        // Shell swaps the centered welcome composer for the bottom one, so
        // this component may unmount before the next paint. Query the live
        // textarea instead of taRef, which would still point at the removed
        // node and silently focus nothing.
        requestAnimationFrame(() => {
          const el = document.querySelector<HTMLTextAreaElement>('[data-testid="composer-textarea"]')
          if (!el) return
          el.focus()
          el.setSelectionRange(el.value.length, el.value.length)
        })
        break
      }
      case "stop_run":
        // ★ 异步：它要等服务端真的把那一轮停掉。
        void useStore.getState().stop()
        break
      case "attach_files":
        fileRef.current?.click()
        break
      case "open_settings":
        focusAfterSettings.current = true
        void useSettings.getState().openSettings()
        break
      case "open_agents":
        setPicker({ open: true })
        break
    }
  }

  const mentionCurrent = trigger?.kind === "mention" ? mention.query === trigger.query : false
  const mentionItems = mentionCurrent ? mention.items : []
  const commandItems =
    trigger?.kind === "command"
      ? filterCommands(commands ?? [], trigger.query, { status, uploading })
      : []

  const popupItems: PopupItem[] = []
  if (trigger?.kind === "mention") {
    for (const file of mentionItems) {
      const slash = file.path.lastIndexOf("/")
      popupItems.push({
        id: file.path,
        label: slash >= 0 ? file.path.slice(slash + 1) : file.path,
        description: slash >= 0 ? file.path.slice(0, slash) : undefined,
      })
    }
  } else if (trigger?.kind === "command") {
    for (const cmd of commandItems) {
      popupItems.push({
        id: cmd.name,
        label: `/${cmd.name}`,
        hint: cmd.argumentHint,
        description: cmd.description,
      })
    }
  }

  const activeIndex = popupItems.length === 0 ? 0 : Math.min(active, popupItems.length - 1)

  const commitCompletion = (next: { text: string; caret: number }) => {
    closeTrigger()
    setText(next.text)
    requestAnimationFrame(() => {
      taRef.current?.focus()
      taRef.current?.setSelectionRange(next.caret, next.caret)
    })
  }

  // Tab (and a mouse click) only completes the token; it never runs an action
  // or sends a prompt. Execution stays on Enter, so a stray Tab cannot switch
  // threads or start a run.
  const fill = (index: number) => {
    if (!trigger) return

    if (trigger.kind === "mention") {
      const file = mentionItems[index]
      if (!file) return
      commitCompletion(applyTrigger(text, trigger, mentionInsertion(file.path)))
      return
    }

    const cmd = commandItems[index]
    if (!cmd) return
    commitCompletion(applyTrigger(text, trigger, `/${cmd.name}`))
  }

  const runActionCommand = (cmd: SlashCommand) => {
    if (!trigger || cmd.kind !== "action" || !cmd.action) return
    const next = removeTrigger(text, trigger)
    closeTrigger()
    setText(next)
    runAction(cmd.action)
  }

  const execute = (index: number) => {
    if (!trigger) return

    if (trigger.kind === "mention") {
      fill(index)
      return
    }

    const cmd = commandItems[index]
    if (!cmd) return
    if (cmd.kind === "action") {
      runActionCommand(cmd)
      return
    }

    // A prompt command executes with no arguments. Use Tab to complete it
    // first when there is an argument to type.
    if (busy || blocked) return
    closeTrigger()
    setText("")
    void send(`/${cmd.name}`)
  }

  // Mouse selection keeps the old behavior: actions run, prompt commands
  // complete so arguments can be typed before Enter.
  const activate = (index: number) => {
    if (!trigger) return
    if (trigger.kind === "mention") {
      fill(index)
      return
    }
    const cmd = commandItems[index]
    if (!cmd) return
    if (cmd.kind === "action") {
      runActionCommand(cmd)
      return
    }
    fill(index)
  }

  const submit = async () => {
    const t = text.trim()
    if (!t || busy || blocked) return

    if (t.startsWith("/")) {
      // Retry a failed manifest load on submit; a transient failure should not
      // permanently disable action commands for this tab.
      const cmds = commands && commands.length > 0 ? commands : await loadCommands().catch(() => [])

      // ★ /skill 必须先于 actionCommandFor：后者只认「整条就是一个命令名」，
      //   而 /skill refactor 带参数，不能被当成普通消息发出去。
      const skillCmd = skillCommandFor(t, cmds)
      if (skillCmd) {
        closeTrigger()
        if (skillCmd.mode === "run" && skillCmd.name) {
          const known = skills.some((sk) => sk.name === skillCmd.name)
          if (known) {
            // ★ 原样发出去：服务端把 `/skill <name> [任务]` 展开成
            //   「用这个 skill」的话（api/commands.go 的模板）—— 前端不
            //   自己拼 prompt，那份文本是命令表的地盘。
            void send(t)
          } else {
            // 未知名字：打开选择器并用它过滤，而不是发出去或者报一个
            // 「命令不存在」（与 /agents 同一套处理）。
            setSkillPicker({ open: true, filter: skillCmd.name })
          }
          return
        }
        setSkillPicker({ open: true })
        return
      }

      // ★ /agents 必须先于 actionCommandFor：后者只认「整条就是一个命令名」，
      //   而 /agents task 带参数，不能被当成普通消息发出去。
      const agentCmd = agentCommandFor(t, cmds)
      if (agentCmd) {
        setText("")
        closeTrigger()
        if (agentCmd.mode === "switch" && agentCmd.agentId) {
          const known = agentChoices.some((a) => a.id === agentCmd.agentId)
          if (known) {
            try {
              await useStore.getState().switchAgent(agentCmd.agentId)
            } catch (e) {
              // 被拒（运行中 / 待审批）时把原因放进选择器：静默失败会让
              // 用户以为命令写错了。
              setPicker({ open: true, filter: agentCmd.agentId, error: (e as Error).message })
            }
          } else {
            // 未知名字：按 §13.6 打开选择器并用它过滤，而不是发出去
            // 或者报一个「命令不存在」。
            setPicker({ open: true, filter: agentCmd.agentId })
          }
          return
        }
        setPicker({ open: true })
        return
      }

      const action = actionCommandFor(t, cmds)
      if (action?.action) {
        setText("")
        closeTrigger()
        runAction(action.action)
        return
      }
    }

    setText("")
    closeTrigger()
    void send(t)
  }

  const popupLoading =
    trigger?.kind === "mention"
      ? !mentionCurrent
      : trigger?.kind === "command"
        ? commands === null
        : false
  const popupError =
    trigger?.kind === "mention"
      ? mentionCurrent
        ? mention.error
        : undefined
      : trigger?.kind === "command" && commands?.length === 0
        ? "No commands available"
        : undefined

  return (
    <div className={inset ? "shrink-0 px-4 pb-3" : undefined}>
      {attachments.length > 0 && (
        <ul data-testid="attachments" className="mx-auto mb-1.5 flex max-w-3xl flex-wrap gap-1.5">
          {attachments.map((f) => (
            <li
              key={f.path}
              data-testid="attachment-chip"
              className="bg-muted flex items-center gap-1.5 rounded-md px-2 py-1 text-xs"
            >
              <PaperclipIcon className="size-3 shrink-0 opacity-70" />
              <span className="max-w-40 truncate">{f.name}</span>
              <span className="text-muted-foreground opacity-70">{formatSize(f.size)}</span>
              <button
                type="button"
                aria-label={`Remove ${f.name}`}
                onClick={() => activeId && void removeFile(activeId, f.name)}
                className="hover:bg-accent grid size-4 shrink-0 place-items-center rounded"
              >
                <XIcon className="size-3" />
              </button>
            </li>
          ))}
        </ul>
      )}

      <div className="border-input focus-within:border-ring focus-within:ring-ring/40 mx-auto flex max-w-3xl items-end gap-2 rounded-2xl border px-3 py-2 focus-within:ring-3">
        <input
          ref={fileRef}
          type="file"
          multiple
          className="hidden"
          aria-label="Attach files"
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            e.target.value = "" // 允许连续选同一个文件
            if (files.length > 0) void addFiles(files).catch(() => {})
          }}
        />
        <button
          type="button"
          aria-label="Add attachment"
          disabled={uploading}
          onClick={() => fileRef.current?.click()}
          className="hover:bg-muted grid size-8 shrink-0 place-items-center rounded-lg disabled:opacity-50"
        >
          {uploading ? <Loader2Icon className="size-4 animate-spin" /> : <PaperclipIcon className="size-4" />}
        </button>

        <div ref={rowRef} className="relative min-w-0 flex-1">
          <textarea
            ref={taRef}
            aria-label="Message input"
            data-testid="composer-textarea"
            aria-autocomplete="list"
            aria-expanded={trigger !== null}
            aria-controls={trigger ? "composer-popup" : undefined}
            aria-activedescendant={
              trigger && popupItems[activeIndex]
                ? `composer-option-${encodeURIComponent(popupItems[activeIndex].id)}`
                : undefined
            }
            autoFocus
            value={text}
            disabled={blocked}
            placeholder={blocked ? "Answer the approval above to continue" : "Send a message..."}
            rows={1}
            onChange={(e) => {
              const value = e.target.value
              setText(value)
              if (!composingRef.current) syncTrigger(value, e.target.selectionStart)
            }}
            onSelect={(e) => {
              if (!composingRef.current) syncTrigger(e.currentTarget.value, e.currentTarget.selectionStart)
            }}
            onCompositionStart={() => {
              composingRef.current = true
            }}
            onCompositionEnd={(e) => {
              composingRef.current = false
              syncTrigger(e.currentTarget.value, e.currentTarget.selectionStart)
            }}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing || composingRef.current) return

              if (trigger) {
                if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                  e.preventDefault()
                  if (popupItems.length > 0) {
                    setActive((i) => (i + (e.key === "ArrowDown" ? 1 : popupItems.length - 1)) % popupItems.length)
                  }
                  return
                }
                if (e.key === "Escape") {
                  e.preventDefault()
                  closeTrigger()
                  return
                }
                if (e.key === "Tab") {
                  if (popupItems.length > 0) {
                    e.preventDefault()
                    fill(activeIndex)
                    return
                  }
                }
                if (e.key === "Enter" && !e.shiftKey) {
                  if (popupItems.length > 0) {
                    e.preventDefault()
                    execute(activeIndex)
                    return
                  }
                  // Do not send a bare "/" or "@" while the first fetch is
                  // still in flight — the user is waiting for a menu, not
                  // trying to submit that character.
                  if (popupLoading) {
                    e.preventDefault()
                    return
                  }
                  closeTrigger()
                }
              }

              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault()
                void submit()
              }
            }}
            className="block min-h-6 w-full resize-none bg-transparent py-1 text-sm outline-none placeholder:text-muted-foreground disabled:opacity-60"
          />
          {trigger && (
            <ComposerPopup
              items={popupItems}
              active={activeIndex}
              loading={popupLoading}
              emptyLabel={trigger.kind === "mention" ? "No matching files" : "No matching commands"}
              error={popupError}
              onPick={activate}
              onHover={setActive}
            />
          )}
        </div>
        {/*
          ★ 运行中只留一个动作按钮：**停止按钮取代发送按钮**，而不是并排。
            并排时用户会看到「停止 + 一个灰掉的发送」，既多占一个槽位，
            也让「现在只能停、不能发」这件事不够明确。
        */}
        {busy ? (
          <StopButton />
        ) : (
          <button
            type="button"
            aria-label="Send message"
            disabled={!text.trim() || busy || blocked}
            onClick={() => void submit()}
            className="bg-primary text-primary-foreground grid size-8 shrink-0 place-items-center rounded-lg disabled:opacity-40"
          >
            ↑
          </button>
        )}
      </div>

      <ComposerMeta badge={agent.badge} loading={agent.loading} error={agent.error} />

      {skillPicker.open ? (
        <SkillPicker
          open
          onClose={() => setSkillPicker({ open: false })}
          skills={skills}
          filter={skillPicker.filter}
          error={skillPicker.error}
          onPick={(sk) => {
            // ★ 选中只**回填命令文本**，不直接发送 —— 用户多半还要补一句
            //   任务；「发出去就等于请模型去调 skill 工具」由服务端展开
            //   命令时完成 ✅。
            setSkillPicker({ open: false })
            setText(`/skill ${sk.name} `)
            taRef.current?.focus()
          }}
        />
      ) : null}

      {picker.open ? (
        <AgentPicker
          open
          onClose={() => setPicker({ open: false })}
          agents={agentChoices}
          currentId={activeAgentId}
          busy={switchBlocked}
          filter={picker.filter}
          error={picker.error}
          onPick={async (id) => {
            try {
              await useStore.getState().switchAgent(id)
              setPicker({ open: false })
            } catch (e) {
              setPicker((p) => ({ ...p, error: (e as Error).message }))
            }
          }}
        />
      ) : null}
    </div>
  )
}

function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

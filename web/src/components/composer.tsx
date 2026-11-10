import { Loader2Icon, PaperclipIcon } from "lucide-react"
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"

import { AgentPicker } from "@/components/agent-picker"
import { AttachmentChip } from "@/components/attachment-chip"
import { MCPPanel } from "@/components/mcp-panel"
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
  selectActiveContext,
  selectAttachments,
  selectBusyElsewhere,
  selectNotice,
  selectStatus,
  useStore,
} from "@/lib/store"
import { useAgentBadge } from "@/lib/use-agent-badge"
import { cn } from "@/lib/utils"

interface MentionState {
  /** Query the cached items belong to; null = nothing loaded yet. */
  query: string | null
  items: ProjectFile[]
  error?: string
}

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
  const [picker, setPicker] = useState<{ open: boolean; filter?: string; error?: string }>({
    open: false,
  })
  const [mcpPanel, setMcpPanel] = useState(false)
  const [skillPicker, setSkillPicker] = useState<{ open: boolean; filter?: string; error?: string }>({
    open: false,
  })
  const [skills, setSkills] = useState<SkillView[]>([])
  // dragging says a file is hovering over the composer.
  //
  // ★ It is a state and not a style alone: the drop handler has to know
  //   that a drag is in flight, and the highlight has to go away when the
  //   drag leaves — a stuck ring would tell the user the composer is
  //   waiting for a file that is not coming.
  const [dragging, setDragging] = useState(false)
  useEffect(() => {
    let alive = true
    void loadSkills()
      .then((list) => {
        if (alive) setSkills(list)
      })
      .catch(() => {
      })
    return () => {
      alive = false
    }
  }, [])

  const fileRef = useRef<HTMLInputElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)
  const rowRef = useRef<HTMLDivElement>(null)
  const composingRef = useRef(false)

  const attachments = useStore(selectAttachments)
  const activeId = useStore((s) => s.activeId)
  const uploading = useStore((s) => s.uploading)
  const settingsOpen = useSettings((s) => s.open)
  const activeAgentId = useStore(selectActiveAgentId)
  const agent = useAgentBadge(settingsOpen, activeAgentId)
  const context = useStore(selectActiveContext)
  const contextUsage = agent.badge ? context?.[agent.badge.id] : undefined
  const notice = useStore(selectNotice)
  // busy says the session has a live turn that this surface does not own.
  const busyElsewhere = useStore(selectBusyElsewhere)
  const agentChoices = agent.view ? selectableAgents(agent.view) : []
  const switchBlocked = busy || blocked
  const focusAfterSettings = useRef(false)
  const pickerWasOpen = useRef(false)
  const mcpWasOpen = useRef(false)
  const addFiles = useStore((s) => s.addFiles)
  const removeFile = useStore((s) => s.removeFile)

  // acceptFiles hands a list of files to the store.
  //
  // ★ The three ways in — the picker, a drop, a paste — end here, so the
  //   name rule lives in one place: a pasted screenshot may arrive with no
  //   name of its own, and the server stores a file under its name.
  const acceptFiles = (list: ArrayLike<File> | null | undefined) => {
    const files = Array.from(list ?? []).map(withName)
    if (files.length === 0) return
    void addFiles(files).catch(() => {})
  }

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

  useEffect(() => {
    if (mcpPanel) {
      mcpWasOpen.current = true
      return
    }
    if (!mcpWasOpen.current) return
    mcpWasOpen.current = false
    const focusComposer = () => {
      const el = taRef.current
      if (!el) return
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
    }
    focusComposer()
    requestAnimationFrame(focusComposer)
  }, [mcpPanel])

  useEffect(() => {
    if (picker.open) {
      pickerWasOpen.current = true
      return
    }
    if (!pickerWasOpen.current) return
    pickerWasOpen.current = false
    const focusComposer = () => {
      const el = taRef.current
      if (!el) return
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
    }
    focusComposer()
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
      case "open_mcps":
        // ★ The panel reads the runtime's state; it does not write a
        //   message into the transcript. A status readout is not a turn.
        setMcpPanel(true)
        break
      case "compact":
        // The compaction runs on the server: it summarizes the history and
        // writes the new boundary. The divider appears when it is done.
        void useStore.getState().compact()
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

      const skillCmd = skillCommandFor(t, cmds)
      if (skillCmd) {
        closeTrigger()
        if (skillCmd.mode === "run" && skillCmd.name) {
          const known = skills.some((sk) => sk.name === skillCmd.name)
          if (known) {
            void send(t)
          } else {
            setSkillPicker({ open: true, filter: skillCmd.name })
          }
          return
        }
        setSkillPicker({ open: true })
        return
      }

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
              setPicker({ open: true, filter: agentCmd.agentId, error: (e as Error).message })
            }
          } else {
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
        <ul data-testid="attachments" className="mx-auto mb-1.5 flex w-full max-w-[var(--chat-column)] flex-wrap gap-1.5">
          {attachments.map((f) => (
            <li key={f.path}>
              <AttachmentChip
                name={f.name}
                size={f.size}
                path={f.path}
                mime={f.mime}
                sessionId={activeId ?? undefined}
                testId="attachment-chip"
                onRemove={() => {
                  if (!activeId) return
                  // ★ The file is really deleted on the server — the chip
                  //   used to hide it while the server kept it, and the
                  //   user was told it was gone.
                  void removeFile(activeId, f.path).catch(() => {})
                }}
              />
            </li>
          ))}
        </ul>
      )}

      <div
        data-testid="composer-box"
        onDragOver={(e) => {
          // ★ The default action of a drag-over is to refuse the drop, so
          //   the handler has to allow it — without this the drop event
          //   never fires at all.
          e.preventDefault()
          setDragging(true)
        }}
        onDragLeave={(e) => {
          // ★ Moving between the composer and its own children fires a
          //   leave; only a leave that leaves the whole box ends the drag.
          if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
          setDragging(false)
        }}
        onDrop={(e) => {
          e.preventDefault()
          setDragging(false)
          acceptFiles(e.dataTransfer.files)
        }}
        className={cn(
          "border-input focus-within:border-ring focus-within:ring-ring/40 mx-auto flex w-full max-w-[var(--chat-column)] items-end gap-2 rounded-2xl border px-3 py-2 focus-within:ring-3",
          dragging && "border-ring ring-ring/40 ring-3",
        )}
      >
        <input
          ref={fileRef}
          type="file"
          multiple
          className="hidden"
          aria-label="Attach files"
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            e.target.value = ""
            acceptFiles(files)
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
            onPaste={(e) => {
              // ★ A pasted screenshot is a file, and the file is what the
              //   user meant to send — not text. The default paste would
              //   put the image's name into the message instead.
              const items = Array.from(e.clipboardData?.items ?? [])
              const files = items
                .filter((it) => it.kind === "file")
                .map((it) => it.getAsFile())
                .filter((f): f is File => f !== null)
              if (files.length === 0) return
              e.preventDefault()
              acceptFiles(files)
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

      <ComposerMeta
        badge={agent.badge}
        loading={agent.loading}
        error={agent.error}
        context={contextUsage}
        notice={notice}
        busy={busyElsewhere}
      />

      {skillPicker.open ? (
        <SkillPicker
          open
          onClose={() => setSkillPicker({ open: false })}
          skills={skills}
          filter={skillPicker.filter}
          error={skillPicker.error}
          onPick={(sk) => {
            setSkillPicker({ open: false })
            setText(`/skill ${sk.name} `)
            taRef.current?.focus()
          }}
        />
      ) : null}

      {mcpPanel ? (
        <MCPPanel open onClose={() => setMcpPanel(false)} agentId={activeAgentId} />
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

// withName gives a file a name when it has none.
//
// ★ A pasted screenshot arrives from the clipboard with no name of its own
//   in some browsers, and the server stores a file under its name — so the
//   name has to exist before the upload, or the upload is refused.
function withName(file: File): File {
  if (file.name) return file
  const mime = file.type || "application/octet-stream"
  const ext = mime.split("/")[1]?.split("+")[0] || "bin"
  return new File([file], `pasted.${ext}`, { type: mime })
}

import { MoreHorizontalIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { useShallow } from "zustand/react/shallow"

import { SettingsEntry } from "@/components/settings/settings-entry"
import { Button } from "@/components/ui/button"
import { selectSessionList, useStore } from "@/lib/store"
import type { SessionEntry } from "@/lib/store"
import { cn } from "@/lib/utils"

export function Sidebar() {
  const sessions = useStore(useShallow(selectSessionList))
  const activeId = useStore((s) => s.activeId)
  const loading = useStore((s) => s.loadingList)
  const open = useStore((s) => s.openSession)
  const startNew = useStore((s) => s.startNewThread)

  return (
    <aside data-testid="sidebar" className="border-e flex w-64 shrink-0 flex-col">
      <div className="p-2">
        <Button
          data-testid="new-thread"
          variant="outline"
          className="w-full justify-start gap-2"
          onClick={() => startNew()}
        >
          <PlusIcon className="size-4" />
          New thread
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        {loading && sessions.length === 0 ? (
          <p className="text-muted-foreground p-2 text-xs">Loading…</p>
        ) : sessions.length === 0 ? (
          <p className="text-muted-foreground p-2 text-xs">No conversations yet.</p>
        ) : (
          <ul className="space-y-0.5">
            {sessions.map((s) => (
              <SessionRow
                key={s.meta.threadId}
                entry={s}
                active={s.meta.threadId === activeId}
                onOpen={() => void open(s.meta.threadId)}
              />
            ))}
          </ul>
        )}
      </div>
      <SettingsEntry className="border-border border-t px-2 py-1.5" />
    </aside>
  )
}

function SessionRow({
  entry,
  active,
  onOpen,
}: {
  entry: SessionEntry
  active: boolean
  onOpen(): void
}) {
  const rename = useStore((s) => s.renameSession)
  const remove = useStore((s) => s.deleteSession)
  const [menuOpen, setMenuOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(entry.meta.title)
  const wrapRef = useRef<HTMLLIElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener("mousedown", onDown)
    return () => document.removeEventListener("mousedown", onDown)
  }, [menuOpen])

  const commit = async () => {
    const t = draft.trim()
    setEditing(false)
    setMenuOpen(false)
    if (!t || t === entry.meta.title) return
    await rename(entry.meta.threadId, t).catch(() => {
      setDraft(entry.meta.title)
    })
  }

  if (editing) {
    return (
      <li ref={wrapRef} className="px-1 py-0.5">
        <input
          autoFocus
          aria-label="Thread title"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => void commit()}
          onKeyDown={(e) => {
            if (e.key === "Enter") void commit()
            if (e.key === "Escape") {
              setDraft(entry.meta.title)
              setEditing(false)
            }
          }}
          className="border-ring bg-background w-full rounded-md border px-2 py-1 text-sm outline-none focus-visible:ring-2"
        />
      </li>
    )
  }

  return (
    <li ref={wrapRef} data-testid="thread-row" className="group relative">
      <button
        type="button"
        data-testid="thread-item"
        data-active={active || undefined}
        onClick={onOpen}
        className={cn(
          "hover:bg-muted flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-start text-sm",
          active && "bg-muted font-medium",
        )}
      >
        <span className="min-w-0 flex-1 truncate">{entry.meta.title || "New Chat"}</span>
        {entry.meta.hasPendingApproval && (
          <span
            data-testid="pending-dot"
            title="Waiting for approval"
            aria-label="Waiting for approval"
            className="bg-warning size-1.5 shrink-0 rounded-full"
          />
        )}
      </button>

      <button
        type="button"
        aria-label="More options"
        onClick={() => setMenuOpen((v) => !v)}
        className="hover:bg-muted absolute end-1 top-1/2 grid size-6 -translate-y-1/2 place-items-center rounded-md opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
      >
        <MoreHorizontalIcon className="size-4" />
      </button>

      {menuOpen && (
        <div
          role="menu"
          data-testid="thread-menu"
          className="bg-popover absolute end-1 top-full z-10 mt-0.5 w-36 rounded-md border p-0.5 shadow-md"
        >
          <MenuItem
            icon={<PencilIcon className="size-3.5" />}
            label="Rename"
            onClick={() => {
              setMenuOpen(false)
              setEditing(true)
            }}
          />
          <MenuItem
            icon={<Trash2Icon className="size-3.5" />}
            label="Delete"
            danger
            onClick={() => {
              setMenuOpen(false)
              void remove(entry.meta.threadId).catch(() => {})
            }}
          />
        </div>
      )}
    </li>
  )
}

function MenuItem({
  icon,
  label,
  onClick,
  danger,
}: {
  icon: React.ReactNode
  label: string
  onClick(): void
  danger?: boolean
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={cn(
        "hover:bg-muted flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-start text-sm",
        danger && "text-destructive",
      )}
    >
      {icon}
      {label}
    </button>
  )
}

import {
  ChevronDownIcon,
  ChevronRightIcon,
  FolderIcon,
  FolderOpenIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react"
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { useShallow } from "zustand/react/shallow"

import { ProjectRootDialog } from "@/components/projects-dialog"
import { SettingsEntry } from "@/components/settings/settings-entry"
import { Button } from "@/components/ui/button"
import { foldGroups, groupSessions, projectRenameError } from "@/lib/projects"
import type { ProjectSummary, SessionGroup } from "@/lib/projects"
import { selectSessionList, useStore } from "@/lib/store"
import type { SessionEntry } from "@/lib/store"
import { cn } from "@/lib/utils"

// ★ The folded groups are remembered on this machine.
//
// ★ They are a **preference**, not a fact: the server never hears of them,
//   and the conversations they hide are still there. ★ The key holds the
//   **group keys** — the "no project" group is folded like any other.
const FOLDED_KEY = "openheron.sidebar.folded"

function readFolded(): Set<string> {
  try {
    const raw = localStorage.getItem(FOLDED_KEY)
    if (!raw) return new Set()
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return new Set()
    return new Set(parsed.filter((k): k is string => typeof k === "string"))
  } catch {
    // ★ A browser that refuses storage still gets a working sidebar.
    return new Set()
  }
}

function writeFolded(folded: ReadonlySet<string>): void {
  try {
    localStorage.setItem(FOLDED_KEY, JSON.stringify([...folded]))
  } catch {
    // ★ Not being able to remember is not a reason to break the fold.
  }
}

export function Sidebar({ className }: { className?: string }) {
  const sessions = useStore(useShallow(selectSessionList))
  const projects = useStore((s) => s.projects)
  const scope = useStore((s) => s.projectScope)
  const activeId = useStore((s) => s.activeId)
  const loading = useStore((s) => s.loadingList)
  const open = useStore((s) => s.openSession)
  const startNew = useStore((s) => s.startNewThread)

  // ★ The grouping is a **pure function** over three answers the server
  //   gave: the sessions, the projects, and the scope. ★ The client does
  //   not decide anything here — it does not guess which project a session
  //   belongs to, and it does not decide how many groups there are.
  const groups = useMemo(
    () => groupSessions(sessions, projects, scope),
    [sessions, projects, scope],
  )

  // ★ The flat list — no heading — is the case where there is nothing to
  //   group by: no projects in the table, so every conversation belongs to
  //   no project. ★ The table is the answer, not the groups: a table with
  //   projects in it is a map, even when no conversation has been started in
  //   one of them yet.
  const flat = scope === null && projects.length === 0

  // ★ The fold is the client's own business: the server never hears of it.
  const [folded, setFolded] = useState<Set<string>>(readFolded)
  useEffect(() => {
    writeFolded(folded)
  }, [folded])

  const shown = useMemo(() => foldGroups(groups, folded), [groups, folded])

  const toggle = (key: string) => {
    setFolded((prev) => {
      // ★ The keys of groups that are gone are dropped here: the list of
      //   groups is known at this moment, and a deleted project must not
      //   leave its key behind for good.
      const live = new Set(groups.map((g) => g.key))
      const next = new Set([...prev].filter((k) => live.has(k)))
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }

  return (
    <aside
      data-testid="sidebar"
      // ★ The width is a token: the docked column and the drawer have to
      //   be the same width, and a width written in two places is two
      //   widths that drift.
      className={cn("border-e flex w-[var(--sidebar-width)] shrink-0 flex-col", className)}
    >
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
        ) : flat ? (
          sessions.length === 0 ? (
            <p className="text-muted-foreground p-2 text-xs">No conversations yet.</p>
          ) : (
            // ★ No project to be a folder: the conversations sit at the root of
            //   the tree, and there is no heading to draw. ★ The sidebar looks
            //   exactly as it looked before the projects landed.
            <ul role="tree" aria-label="Conversations" data-testid="session-tree" className="space-y-0.5">
              {groups[0]!.sessions.map((s) => (
                <SessionRow
                  key={s.meta.threadId}
                  entry={s}
                  level={1}
                  active={s.meta.threadId === activeId}
                  onOpen={() => void open(s.meta.threadId)}
                />
              ))}
            </ul>
          )
        ) : (
          <ul role="tree" aria-label="Conversations" data-testid="session-tree" className="space-y-0.5">
            {shown.map((group) => (
              <li
                key={group.key}
                role="treeitem"
                // ★ A folder with nothing in it is not expandable, and it says
                //   so: the attribute is left off rather than lying with a
                //   "false" that promises a child list.
                aria-expanded={group.sessions.length > 0 ? !group.folded : undefined}
                aria-level={1}
                aria-label={group.title}
                data-testid="session-group"
                data-group={group.key}
                data-folded={group.folded ? "true" : "false"}
              >
                <ProjectGroup
                  group={group}
                  project={projects.find((p) => p.id === group.key) ?? null}
                  scoped={scope !== null}
                  activeId={activeId}
                  onToggle={() => toggle(group.key)}
                  onOpen={(id) => void open(id)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
      <SettingsEntry className="border-border border-t px-2 py-1.5" />
    </aside>
  )
}

/**
 * ProjectGroup is one folder of the tree: its heading, and the
 * conversations in it.
 *
 * ★ The heading is a **folder row**: a disclosure triangle, a folder, the
 *   name and the row's own menu. ★ The triangle is the fold — it is the one
 *   control that is always there, because it says a **state**, not an action.
 *
 * ★ The menu holds the actions on the project. ★ It is here and not in the
 *   dialog under the input box, because the project is *seen* here: an
 *   action that is far from the thing it acts on is an action nobody takes.
 */
function ProjectGroup({
  group,
  project,
  scoped,
  activeId,
  onToggle,
  onOpen,
}: {
  group: SessionGroup<SessionEntry>
  project: ProjectSummary | null
  scoped: boolean
  activeId: string | null
  onToggle(): void
  onOpen(id: string): void
}) {
  const startThreadInProject = useStore((s) => s.startThreadInProject)
  const renameProject = useStore((s) => s.renameProject)
  const deleteProject = useStore((s) => s.deleteProject)
  const [menuOpen, setMenuOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(group.title)
  const [nameError, setNameError] = useState<string | undefined>()
  const [rootOpen, setRootOpen] = useState(false)
  // ★ The key of the folder dialog's **opening**: it changes when the dialog
  //   opens, so the dialog is mounted afresh and its field starts on the
  //   path the project has. ★ It does not change when the dialog closes —
  //   the close animation belongs to the mounted dialog.
  const [rootOpenKey, setRootOpenKey] = useState(0)
  const headRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const onDown = (e: MouseEvent) => {
      if (!headRef.current?.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener("mousedown", onDown)
    return () => document.removeEventListener("mousedown", onDown)
  }, [menuOpen])

  useEffect(() => {
    if (!editing) return
    const el = inputRef.current
    if (!el) return
    el.focus()
    el.select()
  }, [editing])

  // ★ The draft is seeded from the name the heading shows **when the editor
  //   opens** — the table is the server's answer, and it may have changed
  //   since the row was drawn.
  const startEditing = () => {
    setMenuOpen(false)
    setDraft(group.title)
    setNameError(undefined)
    setEditing(true)
  }

  const commit = async () => {
    const name = draft.replace(/[\r\n]+/g, " ").trim()
    // ★ An empty name is refused: the project already has one, and an empty
    //   name would take it away. ★ The rule is the client's own — it is the
    //   same one the server keeps.
    const error = projectRenameError(name)
    if (error !== undefined) {
      setNameError(error)
      return
    }
    setNameError(undefined)
    setEditing(false)
    if (name === group.title || !project) return
    await renameProject(project.id, name).catch(() => {})
  }

  const remove = async () => {
    setMenuOpen(false)
    if (!project) return
    await deleteProject(project.id).catch(() => {})
  }

  const folded = group.folded === true
  // ★ The count is the **server's** answer for a project, and the client's
  //   own for the group that has none.
  const count = project ? project.sessionCount : group.sessions.length
  // ★ The folder is open when there is something inside it: ★ an empty
  //   folder is a closed folder, and a folder that is folded away is closed
  //   too — the icon says the state, the way the triangle does.
  const openFolder = !folded && group.sessions.length > 0

  return (
    <>
      <div
        ref={headRef}
        data-testid="session-group-title"
        className="text-muted-foreground group hover:bg-muted relative flex items-center gap-1 rounded-md px-2 pt-2 pb-1 text-xs font-medium"
      >
        {group.sessions.length > 0 ? (
          <button
            type="button"
            data-testid="group-toggle"
            aria-expanded={!folded}
            aria-label={`${folded ? "Expand" : "Collapse"} ${group.title}`}
            onClick={onToggle}
            className="hover:bg-muted grid size-4 shrink-0 place-items-center rounded-sm"
          >
            {folded ? (
              <ChevronRightIcon aria-hidden="true" className="size-3.5" />
            ) : (
              <ChevronDownIcon aria-hidden="true" className="size-3.5" />
            )}
          </button>
        ) : (
          // ★ An empty folder has nothing to unfold: the triangle is the
          //   fold, and folding nothing is a control that does nothing. ★ The
          //   spacer keeps the names lined up with the folders that do have
          //   conversations under them.
          <span aria-hidden="true" className="size-4 shrink-0" />
        )}

        {openFolder ? (
          <FolderOpenIcon aria-hidden="true" className="size-3.5 shrink-0" />
        ) : (
          <FolderIcon aria-hidden="true" className="size-3.5 shrink-0" />
        )}

        {editing ? (
          <input
            ref={inputRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => void commit()}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                void commit()
              }
              if (e.key === "Escape") {
                setDraft(group.title)
                setEditing(false)
              }
            }}
            aria-label={`Name of ${group.title}`}
            aria-invalid={nameError !== undefined}
            title={nameError}
            data-testid="project-group-rename"
            className="border-ring bg-background min-w-0 flex-1 rounded-md border px-1 py-0.5 text-xs outline-none focus-visible:ring-2"
          />
        ) : (
          <span className="min-w-0 flex-1 truncate">{group.title}</span>
        )}

        {folded && (
          <span data-testid="group-count" className="shrink-0 text-[11px] tabular-nums">
            {count}
          </span>
        )}

        {project && (
          <div className="relative shrink-0">
            <button
              type="button"
              aria-label={`Options for ${group.title}`}
              data-testid="project-menu-button"
              onClick={() => setMenuOpen((v) => !v)}
              className="hover:bg-muted grid size-5 place-items-center rounded-md opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
            >
              <MoreHorizontalIcon aria-hidden="true" className="size-3.5" />
            </button>

            {menuOpen && (
              <div
                role="menu"
                data-testid="project-menu"
                className="bg-popover absolute end-1 top-full z-10 mt-0.5 w-44 rounded-md border p-0.5 shadow-md"
              >
                <MenuItem
                  icon={<PlusIcon className="size-3.5" />}
                  label="New conversation"
                  onClick={() => {
                    setMenuOpen(false)
                    startThreadInProject(group.key)
                  }}
                />
                <MenuItem
                  icon={<PencilIcon className="size-3.5" />}
                  label="Rename project"
                  onClick={startEditing}
                />
                {/*
                  ★ The process serves **one** directory in the single-project
                  //   mode, and that directory is the scope: moving or
                  //   removing it would change what the process serves, so
                  //   those two actions are not offered there.
                */}
                {!scoped && (
                  <MenuItem
                    icon={<FolderIcon className="size-3.5" />}
                    label="Change folder"
                    onClick={() => {
                      setMenuOpen(false)
                      // ★ The key changes with the opening: the dialog is
                      //   mounted afresh, and its field starts on the path
                      //   the project has.
                      setRootOpenKey((k) => k + 1)
                      setRootOpen(true)
                    }}
                  />
                )}
                {!scoped && (
                  <MenuItem
                    icon={<Trash2Icon className="size-3.5" />}
                    label="Remove project"
                    danger
                    onClick={() => void remove()}
                  />
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {!folded && (
        <ul role="group" className="space-y-0.5 ps-4">
          {group.sessions.map((s) => (
            <SessionRow
              key={s.meta.threadId}
              entry={s}
              level={2}
              active={s.meta.threadId === activeId}
              onOpen={() => onOpen(s.meta.threadId)}
            />
          ))}
        </ul>
      )}

      {project && (
        <ProjectRootDialog
          // ★ The dialog mounts afresh for every opening, so the field starts
          //   on the path the project has — ★ the editor shows what is there,
          //   not what was typed the last time it was open.
          key={rootOpenKey}
          project={project}
          open={rootOpen}
          onOpenChange={setRootOpen}
        />
      )}
    </>
  )
}

function SessionRow({
  entry,
  level,
  active,
  onOpen,
}: {
  entry: SessionEntry
  level: number
  active: boolean
  onOpen(): void
}) {
  const rename = useStore((s) => s.renameSession)
  const remove = useStore((s) => s.deleteSession)
  const [menuOpen, setMenuOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(entry.meta.title)
  const wrapRef = useRef<HTMLLIElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener("mousedown", onDown)
    return () => document.removeEventListener("mousedown", onDown)
  }, [menuOpen])

  // ★ The editor shows the whole name. The row truncates a long title to
  //   one line — but the editor is where the user reads the name before
  //   they change it, and a half-visible name is a name they cannot check.
  //   So the box wraps and grows to fit the text instead of hiding the
  //   tail of it.
  //
  // ★ The height is the **border box**: Tailwind makes every element
  //   border-box, so scrollHeight alone would clip the last line by the
  //   border — and a clipped descender is a clipped name.
  useLayoutEffect(() => {
    const el = inputRef.current
    if (!el) return
    el.style.height = "auto"
    const style = window.getComputedStyle(el)
    const border =
      parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth)
    el.style.height = `${el.scrollHeight + border}px`
  }, [draft, editing])

  // ★ The editor opens on the whole name: the user renames it, they do
  //   not retype it from scratch.
  useEffect(() => {
    if (!editing) return
    const el = inputRef.current
    if (!el) return
    el.focus()
    el.select()
  }, [editing])

  // ★ The draft is seeded from the name the row shows **when the editor
  //   opens**, not from the name the row was mounted with. A session is
  //   named by the server after its first question, so the mounted name
  //   was still the placeholder — the editor opened on "New Chat" while
  //   the sidebar already showed the real one, and pressing Enter
  //   replaced the conversation's name with "New Chat".
  const startEditing = () => {
    setMenuOpen(false)
    setDraft(entry.meta.title)
    setEditing(true)
  }

  const commit = async () => {
    // ★ The title is one line: the sidebar shows one line, and a pasted
    //   name may carry a newline of its own.
    const t = draft.replace(/[\r\n]+/g, " ").trim()
    setEditing(false)
    setMenuOpen(false)
    if (!t || t === entry.meta.title) return
    await rename(entry.meta.threadId, t).catch(() => {
      // ★ The rename failed and the store rolled the title back: the
      //   editor opens on that name again, not on the placeholder.
      setDraft(entry.meta.title)
    })
  }

  if (editing) {
    return (
      <li ref={wrapRef} className="px-1 py-0.5">
        <textarea
          ref={inputRef}
          autoFocus
          aria-label="Thread title"
          rows={1}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => void commit()}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              // ★ The title is one line. A textarea would otherwise put a
              //   newline in it, and the sidebar shows one line.
              e.preventDefault()
              void commit()
            }
            if (e.key === "Escape") {
              setDraft(entry.meta.title)
              setEditing(false)
            }
          }}
          className="border-ring bg-background w-full resize-none overflow-hidden rounded-md border px-2 py-1 text-sm outline-none focus-visible:ring-2"
        />
      </li>
    )
  }

  return (
    <li
      ref={wrapRef}
      role="treeitem"
      aria-level={level}
      aria-label={entry.meta.title || "New Chat"}
      data-testid="thread-row"
      className="group relative"
    >
      <button
        type="button"
        data-testid="thread-item"
        data-active={active || undefined}
        onClick={onOpen}
        className={cn(
          // ★ The row reserves room for its own menu on a small screen:
          //   there the menu is always there, and a title that runs under it
          //   is a title nobody can read to the end.
          "hover:bg-muted flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-start text-sm max-md:pe-8",
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
        // ★ The opacity lives in the stylesheet, not here: on a touch screen
        //   the button has to be there — `group-hover` compiles into
        //   `@media (hover: hover)` and never fires without a pointer.
        className="hover:bg-muted thread-row-menu absolute end-1 top-1/2 grid size-8 -translate-y-1/2 place-items-center rounded-md md:size-6"
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
            onClick={startEditing}
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

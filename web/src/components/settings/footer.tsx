import { useMemo } from "react"

import { useSettings, selectCanSave, selectServerIssues } from "@/lib/settings-store"
import { groupIssues, issueLabel, type Issue } from "@/lib/settings-validate"
import { Button } from "@/components/ui/button"

export function SettingsFooter() {
  const saveState = useSettings((s) => s.saveState)
  const applied = useSettings((s) => s.applied)
  const saveError = useSettings((s) => s.saveError)
  const issues = useSettings((s) => s.issues)
  const serverIssues = useSettings(useServerIssues())
  const canSave = useSettings(canSaveSel)
  const save = useSettings((s) => s.save)
  const remove = useSettings((s) => s.remove)
  const dirty = useSettings((s) => s.draft !== s.saved)
  const selected = useSettings((s) => s.selected)
  const exists = useSettings((s) => s.exists)
  const select = useSettings((s) => s.select)
  const items = useSettings((s) => s.manifest?.items)

  // ★ The set is built here, not in a selector: a selector that returns a
  //   fresh Set on every call reports "changed" every time, and the store
  //   would re-render forever.
  const known = useMemo(() => new Set((items ?? []).map((i) => i.path)), [items])

  const busy = saveState === "saving" || saveState === "validating"

  return (
    <div className="flex shrink-0 items-center gap-3 border-t pt-3">
      <div className="min-w-0 flex-1 text-xs" data-testid="settings-status">
        <Status
          saveState={saveState}
          applied={applied}
          saveError={saveError}
          issues={issues}
          serverIssues={serverIssues}
          dirty={dirty}
          selected={selected}
          known={known}
          onOpen={(path) => void select(path)}
        />
      </div>

      <Button
        type="button"
        variant="ghost"
        className="text-xs"
        disabled={!selected || !exists || busy}
        onClick={() => void remove()}
        data-testid="settings-delete"
      >
        Delete
      </Button>
      <Button
        type="button"
        size="sm"
        disabled={!canSave}
        onClick={() => void save()}
        data-testid="settings-save"
      >
        {saveState === "saving" ? "Saving…" : saveState === "validating" ? "Validating…" : "Save"}
      </Button>
    </div>
  )
}

function Status({
  saveState,
  applied,
  saveError,
  issues,
  serverIssues,
  dirty,
  selected,
  known,
  onOpen,
}: {
  saveState: string
  applied: boolean
  saveError?: string
  issues: Issue[]
  serverIssues: string[]
  dirty: boolean
  selected?: string
  known: ReadonlySet<string>
  onOpen: (path: string) => void
}) {
  if (saveState === "validating") return <span className="text-muted-foreground">Validating…</span>
  if (saveState === "saving") return <span className="text-muted-foreground">Saving…</span>

  if (serverIssues.length > 0) {
    return (
      <ul className="text-destructive flex flex-col gap-0.5">
        {serverIssues.map((m, i) => (
          <li key={i}>{m}</li>
        ))}
      </ul>
    )
  }

  // ★ Three places an issue can belong to, and they are shown apart. The
  //   file being edited is the user's file; a problem in another file is
  //   that file's, and saying so under its own name is the difference
  //   between a message the user can act on and one they cannot.
  const groups = groupIssues(issues, selected)
  const errors = [
    ...groups.own.filter(isError),
    ...groups.tree.filter(isError),
    ...groups.elsewhere.filter(isError),
  ]
  const hints = [...groups.own.filter(isHint), ...groups.elsewhere.filter(isHint)]

  if (errors.length > 0 || hints.length > 0) {
    return (
      <ul className="flex flex-col gap-0.5">
        {errors.map((i, idx) => (
          <li key={`e${idx}`} className="text-destructive">
            <IssueLine issue={i} selected={selected} known={known} onOpen={onOpen} />
          </li>
        ))}
        {hints.map((i, idx) => (
          <li key={`h${idx}`} className="text-muted-foreground">
            <IssueLine issue={i} selected={selected} known={known} onOpen={onOpen} />
          </li>
        ))}
      </ul>
    )
  }
  if (saveState === "error" && saveError) {
    return <span className="text-destructive whitespace-pre-line">{saveError}</span>
  }
  if (saveState === "conflict") {
    return <span className="text-warning">This file changed on disk — reload or overwrite.</span>
  }
  if (saveState === "saved") {
    return (
      <span className={applied ? "text-muted-foreground" : "text-warning"}>
        {applied ? "Saved · applied" : "Saved · restart openheron to apply"}
      </span>
    )
  }
  if (dirty) return <span className="text-muted-foreground">Unsaved changes</span>
  return <span className="text-muted-foreground">Valid</span>
}

// ★ A problem in another file is a link: the file it is in is one click
//   away, and the click is what turns "somebody else's file is broken"
//   into something the user can go and fix.
function IssueLine({
  issue,
  selected,
  known,
  onOpen,
}: {
  issue: Issue
  selected?: string
  known: ReadonlySet<string>
  onOpen: (path: string) => void
}) {
  const label = issueLabel(issue, selected)
  const path = issue.path
  if (!path || path === selected || !known.has(path)) {
    return <>{label}</>
  }
  return (
    <button
      type="button"
      className="hover:underline text-left"
      onClick={() => onOpen(path)}
      title={`Open ${path}`}
    >
      {label}
    </button>
  )
}

function isError(i: Issue): boolean {
  return (i.severity ?? "error") === "error"
}

function isHint(i: Issue): boolean {
  return i.severity === "hint"
}

import { useShallow } from "zustand/react/shallow"
function useServerIssues() {
  return useShallow(selectServerIssues)
}
function canSaveSel(s: Parameters<typeof selectCanSave>[0]) {
  return selectCanSave(s)
}

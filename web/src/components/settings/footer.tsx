
import { useSettings, selectCanSave, selectServerIssues } from "@/lib/settings-store"
import { issueLine, type Issue } from "@/lib/settings-validate"
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
}: {
  saveState: string
  applied: boolean
  saveError?: string
  issues: Issue[]
  serverIssues: string[]
  dirty: boolean
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
  if (issues.length > 0) {
    const errors = issues.filter((i) => (i.severity ?? "error") === "error")
    const hints = issues.filter((i) => i.severity === "hint")
    return (
      <ul className="flex flex-col gap-0.5">
        {errors.map((i, idx) => (
          <li key={`e${idx}`} className="text-destructive">
            {issueLine(i)}
          </li>
        ))}
        {hints.map((i, idx) => (
          <li key={`h${idx}`} className="text-muted-foreground">
            {issueLine(i)}
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

import { useShallow } from "zustand/react/shallow"
function useServerIssues() {
  return useShallow(selectServerIssues)
}
function canSaveSel(s: Parameters<typeof selectCanSave>[0]) {
  return selectCanSave(s)
}
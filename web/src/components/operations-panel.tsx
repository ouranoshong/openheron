import { HistoryIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ApiError, type OperationView } from "@/lib/api"
import { authorizationMode, authorizationOption } from "@/lib/authorization"
import { formatSize } from "@/lib/format-size"
import { EMPTY_OPERATIONS, selectOperations, useStore } from "@/lib/store"

/**
 * OperationsPanel is the session's operation log — the timeline of what
 * happened to the conversation.
 *
 * ★ The button and the dialog live in **one wrapper**: React aligns children
 *   by index, so an entry that shifts is an entry that is unmounted and
 *   mounted again — and the dialog that lives in it closes when it is.
 *
 * ★ The log is the **server's** record. The panel reads it when it opens and
 *   shows what came back; it never writes a line of its own.
 */
export function OperationsPanel() {
  const activeId = useStore((s) => s.activeId)
  const loadOperations = useStore((s) => s.loadOperations)
  // ★ The selector hands back the array the store already holds, so the
  //   reference stays stable across renders.
  const operations = useStore((s) => (activeId ? selectOperations(s, activeId) : EMPTY_OPERATIONS))

  const [open, setOpen] = useState(false)
  const [status, setStatus] = useState<"idle" | "loading" | "ready" | "error">("idle")
  const [error, setError] = useState<string | undefined>()

  // ★ The log is read when the panel opens — and again when the session on
  //   screen changes, because the log belongs to that session. ★ The read
  //   goes through the store, so the lines the panel shows are the ones the
  //   store holds.
  useEffect(() => {
    if (!open || !activeId) return
    let alive = true
    void loadOperations(activeId)
      .then(() => {
        if (!alive) return
        setStatus("ready")
      })
      .catch((e: unknown) => {
        if (!alive) return
        // ★ The reason is the server's own sentence: the client keeps no
        //   second copy of the rules that refuse a read.
        setStatus("error")
        setError(e instanceof ApiError ? e.message : ((e as Error).message ?? String(e)))
      })
    return () => {
      alive = false
    }
  }, [open, activeId, loadOperations])

  return (
    // ★ One wrapper around the button and the dialog. ★ The dialog's state
    //   lives inside it, so it keeps its index: React aligns children by
    //   index, and a dialog that shifts is a dialog that is unmounted and
    //   mounted again — which closes it.
    <span data-testid="operations-panel" className="inline-flex items-center gap-1">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="text-muted-foreground hover:text-foreground h-6 gap-1 px-1.5 text-[11px]"
        onClick={() => {
          // ★ The read starts with the click, not with the effect: the
          //   panel says it is loading from the first frame.
          setStatus("loading")
          setError(undefined)
          setOpen(true)
        }}
        disabled={!activeId}
        data-testid="operations-button"
        aria-label="Session activity"
        title="What happened in this conversation — the session's operation log."
      >
        <HistoryIcon aria-hidden="true" className="size-3" />
        Activity
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          data-testid="operations-dialog"
          className="sm:max-w-2xl h-[85dvh] flex flex-col gap-3 p-4"
          onEscapeKeyDown={(e) => {
            // ★ Escape has to close this box, and Radix listens for it on the
            //   document in the **capture** phase — so a handler in the tree
            //   is already too late to say anything. ⇒ `onEscapeKeyDown` is
            //   the one place that can still answer, and the answer is yes.
            e.preventDefault()
            setOpen(false)
          }}
        >
          <DialogHeader>
            <DialogTitle>Session activity</DialogTitle>
            <DialogDescription>
              What happened in this conversation, oldest first. The log is the server's record.
            </DialogDescription>
          </DialogHeader>

          {status === "loading" ? (
            <p data-testid="operations-loading" className="text-muted-foreground text-sm">
              Loading…
            </p>
          ) : null}

          {status === "error" ? (
            <p data-testid="operations-error" role="alert" className="text-destructive text-sm">
              {error}
            </p>
          ) : null}

          {status === "ready" && operations.length === 0 ? (
            <p data-testid="operations-empty" className="text-muted-foreground text-sm">
              No activity yet.
            </p>
          ) : null}

          <ol data-testid="operations-list" className="min-h-0 flex-1 space-y-2 overflow-y-auto">
            {operations.map((op) => (
              <OperationRow key={op.seq} op={op} />
            ))}
          </ol>
        </DialogContent>
      </Dialog>
    </span>
  )
}

// OperationRow is one line of the log: when it happened, what it was, and
// what it was about.
function OperationRow({ op }: { op: OperationView }) {
  const details = operationDetails(op)
  return (
    <li
      data-testid="operations-item"
      data-kind={op.kind}
      data-seq={op.seq}
      className="border-border/60 flex flex-col gap-0.5 border-b pb-2 last:border-b-0"
    >
      <div className="flex items-baseline gap-2">
        <time
          dateTime={op.at}
          title={op.at}
          className="text-muted-foreground shrink-0 font-mono text-[11px] tabular-nums"
        >
          {formatAt(op.at)}
        </time>
        <span data-testid="operations-text" className={tone(op)}>
          {describeOperation(op)}
        </span>
      </div>
      {details.length === 0 ? null : (
        <div className="flex flex-wrap items-center gap-1.5">
          {details.map((d) => (
            <span
              key={d.key}
              data-testid="operations-detail"
              className="text-muted-foreground bg-muted rounded px-1.5 py-0.5 font-mono text-[11px]"
            >
              {d.label}
            </span>
          ))}
        </div>
      )}
    </li>
  )
}

// describeOperation turns one line of the log into a sentence.
//
// ★ The kinds are the server's vocabulary and the sentences are the
//   client's — but the client never invents a kind: a line it does not know
//   is shown under its own name, so a new kind shows up instead of
//   vanishing.
function describeOperation(op: OperationView): string {
  switch (op.kind) {
    case "session.created":
      return "Session created."
    case "session.renamed":
      return `Session renamed to "${op.title ?? "a new title"}".`
    case "session.agent_changed":
      return `Agent changed to ${op.agentId ?? "another agent"}.`
    case "session.authorization_confirmed":
      return "Full authorization confirmed."
    case "session.authorization_changed":
      return `Authorization mode changed to ${modeLabel(op.mode)}.`
    case "file.uploaded":
      return `File uploaded: ${op.name ?? op.path ?? "a file"}.`
    case "file.deleted":
      return `File deleted: ${op.path ?? "a file"}.`
    case "run.started":
      return op.turnKind === "resume"
        ? "Turn resumed — the round picked up where it stopped."
        : "Turn started — a new question."
    case "run.finished":
      return `Turn finished: ${stateLabel(op.state)}.`
    case "approval.requested":
      return `Approval requested for ${op.toolName ?? "a tool"}.`
    case "approval.resolved":
      return `Approval ${decisionLabel(op.approved)} for ${op.toolName ?? "a tool"}.`
    default:
      return op.kind
  }
}

// operationDetails are the identifiers of one line: what it is about.
function operationDetails(op: OperationView): { key: string; label: string }[] {
  const out: { key: string; label: string }[] = []
  if (op.runId) out.push({ key: "run", label: `run ${shortId(op.runId)}` })
  if (op.interruptId) out.push({ key: "interrupt", label: `interrupt ${shortId(op.interruptId)}` })
  if (op.path) out.push({ key: "path", label: op.path })
  if (op.agentId) out.push({ key: "agent", label: `agent ${op.agentId}` })
  if (op.projectId) out.push({ key: "project", label: `project ${op.projectId}` })
  if (op.mode) out.push({ key: "mode", label: modeLabel(op.mode) })
  if (op.size !== undefined) out.push({ key: "size", label: formatSize(op.size) })
  return out
}

// modeLabel is the level's own name, as the interface spells it.
//
// ★ It is the same word the authorization picker uses: two names for one
//   level would make the reader wonder whether they are the same thing.
function modeLabel(mode?: string): string {
  return authorizationOption(authorizationMode(mode)).label
}

// stateLabel names the round's ending.
function stateLabel(state?: string): string {
  switch (state) {
    case "completed":
      return "completed"
    case "failed":
      return "failed"
    case "aborted":
      return "aborted"
    case "interrupted":
      return "interrupted"
    case "waiting_approval":
      return "waiting for approval"
    default:
      return state ?? "finished"
  }
}

// decisionLabel is the answer to an approval.
//
// ★ "resolved" is the answer when the line carries no decision: a refusal
//   and a line that is not about an approval are two different facts.
function decisionLabel(approved?: boolean): string {
  if (approved === undefined) return "resolved"
  return approved ? "approved" : "refused"
}

// tone is the color of the line.
//
// ★ The colors are **tokens**: the light and the dark palette carry the
//   same set, and a literal color would only exist in one of them.
function tone(op: OperationView): string {
  if (op.kind === "approval.requested") return "text-warning"
  if (op.kind === "approval.resolved" && op.approved === true) return "text-success"
  return ""
}

// shortId is the head of an id — enough to recognize it in a line.
function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 12)}…` : id
}

// formatAt is the line's time, in the reader's own clock.
//
// ★ It is written out by hand: the browser's own formatting follows the
//   locale, and a log whose columns move with the locale is not a log.
function formatAt(at: string): string {
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return at
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

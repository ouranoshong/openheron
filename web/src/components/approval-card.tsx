import { ShieldAlertIcon } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  approvalArguments,
  approvalMessage,
  approvalTitle,
  approvalToolName,
} from "@/lib/approval"
import { selectInterrupts, useStore } from "@/lib/store"
import { useT } from "@/lib/use-t"

export function ApprovalCard({ inset = true }: { inset?: boolean }) {
  const interrupts = useStore(selectInterrupts)
  const answer = useStore((s) => s.answer)
  const t = useT()

  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState("")

  if (interrupts.length === 0) return null

  const respond = async (approved: boolean) => {
    await answer(interrupts, approved, reason.trim() || undefined)
    setRejecting(false)
    setReason("")
  }

  const multiple = interrupts.length > 1

  return (
    <div className={inset ? "shrink-0 px-4 pb-3" : undefined}>
      <div
        data-testid="approval-card"
        role="alertdialog"
        aria-labelledby="approval-title"
        className="border-warning/40 bg-warning/10 mx-auto w-full max-w-[var(--chat-column)] space-y-2.5 rounded-lg border p-3"
      >
        <div className="flex items-start gap-2">
          <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" />
          <div className="min-w-0">
            <p id="approval-title" className="text-sm font-medium">
              {approvalTitle(interrupts.length, t)}
            </p>
            <p className="text-muted-foreground text-xs">
              {t("messages.approvalPaused")}
            </p>
          </div>
        </div>

        <ul className="space-y-1.5">
          {interrupts.map((i) => {
            const args = approvalArguments(i)
            const message = approvalMessage(i)
            return (
              <li
                key={i.id}
                data-testid="approval-item"
                className="bg-background/60 rounded-md border px-2.5 py-2"
              >
                <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
                  <code
                    data-testid="approval-tool"
                    className="font-mono text-xs font-semibold"
                  >
                    {approvalToolName(i, t)}
                  </code>
                  {i.requestedByAgent ? (
                    <span
                      data-testid="approval-requested-by"
                      className="text-muted-foreground text-xs"
                    >
                      {t("messages.requestedBy", { agent: i.requestedByAgent })}
                    </span>
                  ) : null}
                </div>

                {args && (
                  <p
                    data-testid="approval-arguments"
                    className="text-muted-foreground mt-1 line-clamp-2 font-mono text-[11px] leading-snug break-all"
                    title={args}
                  >
                    {args}
                  </p>
                )}
                {message && (
                  <p className="text-muted-foreground mt-1 text-xs">{message}</p>
                )}
              </li>
            )
          })}
        </ul>

        {rejecting ? (
          <div className="flex gap-2">
            <input
              aria-label={t("messages.rejectionReason")}
              autoFocus
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t("messages.rejectionPlaceholder")}
              className="border-input bg-background min-w-0 flex-1 rounded-md border px-2 py-1 text-xs outline-none focus-visible:ring-2"
            />
            <Button size="sm" variant="destructive" onClick={() => void respond(false)}>
              {t("messages.confirmDenial")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setRejecting(false)}>
              {t("common.cancel")}
            </Button>
          </div>
        ) : (
          <div className="flex gap-2">
            <Button size="sm" onClick={() => void respond(true)}>
              {multiple ? t("messages.approveAll") : t("messages.approve")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => setRejecting(true)}>
              {multiple ? t("messages.denyAll") : t("messages.deny")}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

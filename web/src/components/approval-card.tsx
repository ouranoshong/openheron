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

/**
 * 审批卡（HITL）。
 *
 * ★ 等待审批时它**取代** composer，而不是叠在 composer 上方：
 *   轮次已经结束，输入框此刻本来就不能用。留着一个灰色的空输入框只会
 *   让用户以为「还能打字」，还把真正要做的决定挤到上面去。
 *
 * ★ 内容只留回答「批不批」所需的信息：工具名、请求方、参数摘要。
 *   协议字段（reason / toolCallId）不展示，见 lib/approval.ts。
 *
 * ★ 「拒绝」要求理由：openheron 把它存进审批记录，且会回灌给模型
 *   （ApprovalResult.Reason）。没有理由的拒绝对模型等于凭空断路。
 *
 * ★ 提交的是 payload.approved，不是 AG-UI 的 status。
 *   协议里 resume entry 的 status 只有 resolved / cancelled，描述的是
 *   「这个中断被处理掉了没有」，不表达「用户同不同意」；而 openheron 的服务端
 *   读的是 approved，status 会被忽略（见 api/v2_run.go）。
 *   只发 status 的后果是：用户的「同意」被当成拒绝，工具永远不执行，
 *   而且不报错。
 */
export function ApprovalCard({ inset = true }: { inset?: boolean }) {
  const interrupts = useStore(selectInterrupts)
  const answer = useStore((s) => s.answer)

  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState("")

  if (interrupts.length === 0) return null

  const respond = async (approved: boolean) => {
    // ★ 一次答完整组，不是逐个发。
    //
    //	逐个发的话，服务端会拒绝第二次 —— 因为答完第一个之后，其余的
    //	会带着新 id 重新中断，客户端手里那批旧 id 全废了。
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
        className="border-warning/40 bg-warning/10 mx-auto max-w-3xl space-y-2.5 rounded-lg border p-3"
      >
        <div className="flex items-start gap-2">
          <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" />
          <div className="min-w-0">
            <p id="approval-title" className="text-sm font-medium">
              {approvalTitle(interrupts.length)}
            </p>
            <p className="text-muted-foreground text-xs">
              The agent is paused until you respond.
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
                    {approvalToolName(i)}
                  </code>
                  {/*
                    ★ 子 agent 的审批必须标出请求方：多个 agent 同时要审批时，
                      「批的是谁的工具」直接决定要不要同意。根 agent 的中断不带
                      这个字段（它就是当前会话本身）。
                  */}
                  {i.requestedByAgent ? (
                    <span
                      data-testid="approval-requested-by"
                      className="text-muted-foreground text-xs"
                    >
                      Requested by {i.requestedByAgent}
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
              aria-label="Rejection reason"
              autoFocus
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Reason (sent back to the model)"
              className="border-input bg-background min-w-0 flex-1 rounded-md border px-2 py-1 text-xs outline-none focus-visible:ring-2"
            />
            <Button size="sm" variant="destructive" onClick={() => void respond(false)}>
              Confirm denial
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setRejecting(false)}>
              Cancel
            </Button>
          </div>
        ) : (
          <div className="flex gap-2">
            <Button size="sm" onClick={() => void respond(true)}>
              {multiple ? "Approve all" : "Approve"}
            </Button>
            <Button size="sm" variant="outline" onClick={() => setRejecting(true)}>
              {multiple ? "Deny all" : "Deny"}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

import { BrainIcon, ChevronRightIcon } from "lucide-react"
import { memo, useState } from "react"

import { AttachmentChip } from "@/components/attachment-chip"
import { MarkdownText, MessageBody } from "@/components/markdown"
import { ToolCard } from "@/components/tool-card"
import { thinkingLabel } from "@/lib/thinking"
import type { Message, Part } from "@/lib/types"
import { cn } from "@/lib/utils"

export function Reasoning({
  text,
  complete,
  interrupted,
  elapsedMs,
}: {
  text: string
  complete: boolean
  interrupted?: boolean
  elapsedMs: number | null
}) {
  const [open, setOpen] = useState(false)

  if (!text) return null

  return (
    <div data-testid="reasoning" className="border-muted/60 my-1.5 rounded-lg border border-dashed">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="text-muted-foreground hover:text-foreground flex w-full items-center gap-1.5 px-3 py-1.5 text-xs"
      >
        <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        <BrainIcon className="size-3.5 shrink-0" />
        <span className="font-medium" data-testid="reasoning-label">
          {thinkingLabel({ complete, elapsedMs, interrupted })}
        </span>
        {open && <span className="ml-auto opacity-70">collapse</span>}
      </button>
      {open && (
        <div className="text-muted-foreground border-t px-3 py-2 text-xs whitespace-pre-wrap">
          {text}
        </div>
      )}
    </div>
  )
}

// ★ MessageParts is memoized: only the message being streamed changes
//   while a turn runs, and the rest of the transcript must not re-render
//   with it.
export const MessageParts = memo(function MessageParts({
  parts,
  role,
}: {
  parts: Part[]
  role: Message["role"]
}) {
  return (
    <>
      {parts.map((p) => {
        switch (p.kind) {
          case "text":
            if (!p.text) return null
            if (role === "user") {
              return (
                <MessageBody key={p.id}>
                  <div data-testid="user-text" className="whitespace-pre-wrap break-words">
                    {p.text}
                  </div>
                </MessageBody>
              )
            }
            return (
              <MessageBody key={p.id}>
                <MarkdownText text={p.text} />
              </MessageBody>
            )

          case "reasoning":
            return (
              <Reasoning
                key={p.id}
                text={p.text}
                complete={p.complete}
                interrupted={p.interrupted}
                elapsedMs={p.elapsedMs ?? null}
              />
            )

          case "tool":
            return <ToolCard key={p.id} part={p} />

          // ★ A file that came with the message. It has no remove button:
          //   the list is history, and deleting the file would make the
          //   message claim something that is no longer true.
          case "attachment":
            return (
              <AttachmentChip
                key={p.id}
                name={p.name}
                size={p.size}
                testId="message-attachment"
                className="mb-1.5"
              />
            )
        }
      })}
    </>
  )
})

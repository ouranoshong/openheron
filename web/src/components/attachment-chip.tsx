import { PaperclipIcon, XIcon } from "lucide-react"

import { formatSize } from "@/lib/format-size"
import { cn } from "@/lib/utils"

// AttachmentChip is one file.
//
// ★ It appears in two places, and they are not the same thing:
//
//   - in the composer, the file is **pending**: it is on the server, and it
//     has not gone out with a question yet — so it carries a remove button;
//   - in a message, the file is **history**: it went out with that
//     question, and the transcript says so. There is nothing to remove —
//     deleting the file would make the message lie.
//
// ★ The two carry different test ids on purpose: a spec that counts the
//   composer's chips must not be satisfied by the chips in the history.
export function AttachmentChip({
  name,
  size,
  onRemove,
  testId,
  className,
}: {
  name: string
  size: number
  onRemove?: () => void
  testId: string
  className?: string
}) {
  return (
    <span
      data-testid={testId}
      data-attachment={name}
      className={cn(
        "bg-muted inline-flex max-w-full items-center gap-1.5 rounded-md px-2 py-1 text-xs",
        className,
      )}
    >
      <PaperclipIcon className="size-3 shrink-0 opacity-70" />
      <span className="max-w-40 truncate">{name}</span>
      <span className="text-muted-foreground opacity-70">{formatSize(size)}</span>
      {onRemove && (
        <button
          type="button"
          aria-label={`Remove ${name}`}
          onClick={onRemove}
          className="hover:bg-accent grid size-4 shrink-0 place-items-center rounded"
        >
          <XIcon className="size-3" />
        </button>
      )}
    </span>
  )
}

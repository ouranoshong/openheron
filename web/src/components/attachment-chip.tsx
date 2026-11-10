import {
  FileArchiveIcon,
  FileCodeIcon,
  FileIcon,
  FileImageIcon,
  FileSpreadsheetIcon,
  FileTextIcon,
  FileTypeIcon,
  XIcon,
  type LucideIcon,
} from "lucide-react"
import { useState, type ReactNode } from "react"

import { FilePreview } from "@/components/file-preview"
import { fileUrl } from "@/lib/api"
import { fileClass, isImage } from "@/lib/file-kind"
import { formatSize } from "@/lib/format-size"
import { cn } from "@/lib/utils"

// ★ The icons the classification names.
//
//	★ The classification is a **pure function** and returns the icon's
//	  *name*; the drawing lives here, in the component layer. Two truths
//	  about "what an icon is called" is one truth too many.
const ICONS: Record<string, LucideIcon> = {
  "file-image": FileImageIcon,
  "file-type": FileTypeIcon,
  "file-text": FileTextIcon,
  "file-code": FileCodeIcon,
  "file-spreadsheet": FileSpreadsheetIcon,
  "file-archive": FileArchiveIcon,
  file: FileIcon,
}

// THUMB is the pixel size of a picture inside a chip.
//
// ★ 48 is the size of a chip in a bubble — big enough to recognise the
//   picture, small enough that a row of ten attachments still fits on one
//   line.
const THUMB = 48

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
//
// ★ A picture is shown as a **thumbnail**, not as a paperclip: "I sent the
//   right picture" is the thing the user wants to know, and a paperclip
//   cannot answer it. The thumbnail is scaled on the way out, so the chip
//   does not pull a two-megabyte picture to draw forty-eight pixels.
//
// ★ A file that is gone falls back to its **icon**: a broken picture reads
//   as a fault in the interface, and it is not one.
export function AttachmentChip({
  name,
  size,
  path,
  mime,
  sessionId,
  onRemove,
  testId,
  className,
  thumbSize = THUMB,
}: {
  name: string
  size: number
  // path is the name the server stored the file under. Without it there is
  // nothing to preview — the chip then shows the icon and the name only.
  path?: string
  mime?: string
  // sessionId is the session the file belongs to; the preview URL is built
  // from it.
  sessionId?: string
  onRemove?: () => void
  testId: string
  className?: string
  thumbSize?: number
}) {
  const [open, setOpen] = useState(false)
  const cls = fileClass(name, mime)
  const Icon = ICONS[cls.icon] ?? FileIcon
  const previewable = !!sessionId && !!path
  const picture = previewable && isImage(name, mime)

  return (
    <>
      <span
        data-testid={testId}
        data-attachment={name}
        title={name}
        className={cn(
          "bg-muted inline-flex max-w-full items-center gap-1.5 rounded-md px-2 py-1 text-xs",
          className,
        )}
      >
        {previewable ? (
          <button
            type="button"
            data-testid={`${testId}-open`}
            aria-label={`Preview ${name}`}
            onClick={() => setOpen(true)}
            className="hover:bg-accent flex min-w-0 items-center gap-1.5 rounded text-left"
          >
            {picture ? (
              <Thumb
                sessionId={sessionId!}
                path={path!}
                name={name}
                size={thumbSize}
                fallback={<Icon className="size-4 shrink-0 opacity-70" />}
              />
            ) : (
              <Icon className="size-3.5 shrink-0 opacity-70" />
            )}
            <span className="max-w-40 truncate">{name}</span>
            <span className="text-muted-foreground opacity-70">{formatSize(size)}</span>
          </button>
        ) : (
          <>
            <Icon className="size-3.5 shrink-0 opacity-70" />
            <span className="max-w-40 truncate">{name}</span>
            <span className="text-muted-foreground opacity-70">{formatSize(size)}</span>
          </>
        )}
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
      {open && previewable && (
        <FilePreview
          sessionId={sessionId!}
          path={path!}
          name={name}
          mime={mime}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

// Thumb draws the picture of a chip.
//
// ★ The failure is handled here rather than by the caller: the picture may
//   be gone (the file was deleted), and the chip has to survive it.
function Thumb({
  sessionId,
  path,
  name,
  size,
  fallback,
}: {
  sessionId: string
  path: string
  name: string
  size: number
  fallback: ReactNode
}) {
  const [broken, setBroken] = useState(false)
  if (broken) return <>{fallback}</>
  return (
    <img
      data-testid="attachment-thumb"
      src={fileUrl(sessionId, path, size * 2)}
      alt={name}
      title={name}
      onError={() => setBroken(true)}
      style={{ width: `${size}px`, height: `${size}px` }}
      className="shrink-0 rounded object-cover"
    />
  )
}

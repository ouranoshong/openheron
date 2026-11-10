import { DownloadIcon, FileTextIcon, XIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { CodeBlock } from "@/components/code-block"
import { MarkdownText } from "@/components/markdown"
import { fileUrl } from "@/lib/api"
import { fileClass, fileKind, fileLang } from "@/lib/file-kind"

// ★ The text preview has a ceiling.
//
//	A three-megabyte log file in the DOM is a disaster — the layout stops
//	responding, and the reader gets a page they cannot scroll. So the
//	preview shows the head of the file and says that it did.
const MAX_TEXT_BYTES = 256 * 1024

// FilePreview shows one file, in an overlay.
//
// ★ One overlay for everything. A picture opens in it, a PDF opens in it,
//   a Markdown file opens in it — so the way out is always the same: the
//   background, or Escape. "One interaction, all the way through" beats
//   "pictures pop one way, text pops another".
//
// ★ The server never interprets the content: it hands back the bytes with
//   a content type that keeps a script from running. The **interface**
//   does the interpreting here — it renders the Markdown and highlights the
//   code, so nothing has to trust an uploaded HTML page.
export function FilePreview({
  sessionId,
  path,
  name,
  mime,
  onClose,
}: {
  sessionId: string
  path: string
  name: string
  mime?: string
  onClose: () => void
}) {
  const kind = fileKind(name, mime)
  const url = fileUrl(sessionId, path)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  return (
    <div
      data-testid="file-preview"
      role="dialog"
      aria-modal="true"
      aria-label={name}
      className="bg-background/80 fixed inset-0 z-50 flex items-center justify-center p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        data-testid="file-preview-panel"
        className="bg-background border-border flex max-h-full w-full max-w-4xl flex-col overflow-hidden rounded-xl border shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="border-border flex items-center gap-2 border-b px-4 py-2.5">
          <span className="truncate text-sm font-medium" title={name}>
            {name}
          </span>
          <button
            type="button"
            aria-label="Close preview"
            onClick={onClose}
            className="hover:bg-accent ml-auto grid size-6 shrink-0 place-items-center rounded"
          >
            <XIcon className="size-4" />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto p-4">
          {kind === "image" && <ImagePreview url={url} name={name} />}
          {kind === "pdf" && (
            <iframe
              data-testid="file-preview-pdf"
              title={name}
              src={url}
              className="h-[70vh] w-full rounded-lg border"
            />
          )}
          {(kind === "markdown" || kind === "code" || kind === "text") && (
            <TextPreview url={url} name={name} kind={kind} />
          )}
          {kind === "other" && <OtherPreview url={url} name={name} />}
        </div>
      </div>
    </div>
  )
}

// ImagePreview shows the picture itself.
//
// ★ The **original** is shown, not a scaled copy: opening a picture is
//   about seeing the detail, and a thumbnail has none.
function ImagePreview({ url, name }: { url: string; name: string }) {
  const [broken, setBroken] = useState(false)
  if (broken) {
    return (
      <div data-testid="file-preview-missing" className="text-muted-foreground py-10 text-center text-sm">
        The file is no longer in the session.
      </div>
    )
  }
  return (
    <img
      data-testid="file-preview-image"
      src={url}
      alt={name}
      onError={() => setBroken(true)}
      className="mx-auto max-h-[70vh] max-w-full rounded-lg object-contain"
    />
  )
}

// TextPreview shows the content of a text file.
//
// ★ The bytes come as **text/plain** — the server never lets an uploaded
//   page be a document — so the interface is the one that renders it.
function TextPreview({
  url,
  name,
  kind,
}: {
  url: string
  name: string
  kind: "markdown" | "code" | "text"
}) {
  const [state, setState] = useState<{ text: string; truncated: boolean } | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let live = true
    void (async () => {
      try {
        const res = await fetch(url)
        if (!res.ok) {
          if (live) setFailed(true)
          return
        }
        const raw = await res.text()
        if (!live) return
        // ★ The ceiling is applied to the **characters** the browser has
        //   already read: reading three megabytes to show the first
        //   hundred kilobytes is a waste the user pays for.
        setState({
          text: raw.slice(0, MAX_TEXT_BYTES),
          truncated: raw.length > MAX_TEXT_BYTES,
        })
      } catch {
        if (live) setFailed(true)
      }
    })()
    return () => {
      live = false
    }
  }, [url])

  if (failed) {
    return (
      <div data-testid="file-preview-missing" className="text-muted-foreground py-10 text-center text-sm">
        The file is no longer in the session.
      </div>
    )
  }
  if (!state) {
    return (
      <div data-testid="file-preview-loading" className="text-muted-foreground py-10 text-center text-sm">
        Loading…
      </div>
    )
  }

  return (
    <div data-testid="file-preview-text">
      {kind === "markdown" ? (
        <MarkdownText text={state.text} />
      ) : kind === "code" ? (
        <CodeBlock lang={fileLang(name)} text={state.text} />
      ) : (
        <pre
          data-testid="file-preview-plain"
          className="text-[13px] leading-relaxed break-words whitespace-pre-wrap"
        >
          {state.text}
        </pre>
      )}
      {state.truncated && (
        <p data-testid="file-preview-truncated" className="text-muted-foreground mt-3 text-xs">
          Showing the first {Math.floor(MAX_TEXT_BYTES / 1024)} KiB. Download the file to read the rest.
        </p>
      )}
    </div>
  )
}

// OtherPreview is what a file that cannot be shown looks like.
//
// ★ It is not an error: a zip is a zip, and the way to look at a zip is to
//   download it.
function OtherPreview({ url, name }: { url: string; name: string }) {
  const cls = fileClass(name)
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <FileTextIcon className="text-muted-foreground size-10" />
      <p className="text-sm">{cls.label}</p>
      <a
        data-testid="file-preview-download"
        href={url}
        download={name}
        className="border-border inline-flex items-center gap-2 rounded-lg border px-3 py-1.5 text-sm"
      >
        <DownloadIcon className="size-4" />
        Download
      </a>
    </div>
  )
}

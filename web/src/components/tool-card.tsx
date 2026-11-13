import {
  BotIcon,
  BookOpenIcon,
  ChevronRightIcon,
  DatabaseIcon,
  FilePenIcon,
  FilePlusIcon,
  FileSearchIcon,
  FileTextIcon,
  FolderIcon,
  GlobeIcon,
  LibraryIcon,
  ListTreeIcon,
  ScanSearchIcon,
  SearchIcon,
  SettingsIcon,
  SquareTerminalIcon,
  TextSearchIcon,
  WrenchIcon,
  type LucideIcon,
} from "lucide-react"
import { useState } from "react"

import { CodeBlock } from "@/components/code-block"
import { looksLikeJson } from "@/lib/highlight"
import { prettyArgs } from "@/lib/tool-args"
import { toolIcon, type ToolIconName } from "@/lib/tool-icons"
import type { ToolPart } from "@/lib/types"
import { cn } from "@/lib/utils"

// ★ The icons the tool table names.
//
//	★ The table names an icon; this table draws it. Two truths about
//	  "what an icon is called" is one truth too many — so the *name*
//	  lives in [toolIcon] and only the drawing lives here.
//
//	★ The key type is the table's own, so a name without a drawing here
//	  is a **compile error** — not a card that shows the wrong glyph.
const GLYPHS: Record<ToolIconName, LucideIcon> = {
  folder: FolderIcon,
  "scan-search": ScanSearchIcon,
  "text-search": TextSearchIcon,
  "file-text": FileTextIcon,
  "file-plus": FilePlusIcon,
  "file-pen": FilePenIcon,
  "square-terminal": SquareTerminalIcon,
  globe: GlobeIcon,
  "list-tree": ListTreeIcon,
  bot: BotIcon,
  "book-open": BookOpenIcon,
  settings: SettingsIcon,
  search: SearchIcon,
  library: LibraryIcon,
  "file-search": FileSearchIcon,
  database: DatabaseIcon,
  wrench: WrenchIcon,
}

const TITLES: Record<string, string> = {
  ls: "List directory",
  glob: "Find files",
  grep: "Search content",
  read_file: "Read file",
  write_file: "Write file",
  edit_file: "Edit file",
  execute: "Run command",
  fetch: "Fetch URL",
  write_todos: "Update todos",
}

// ★ The key the argument lives under, per tool.
//
// ★ It is a **lookup**, not a rule: a tool the table does not name still
//
//	shows its arguments, they are just not lifted into the header. The
//	header is a summary — it must never hide a call that the expanded
//	body shows.
const HINT_KEYS: Record<string, string> = {
  read_file: "file_path",
  write_file: "file_path",
  edit_file: "file_path",
  execute: "command",
  grep: "pattern",
  glob: "pattern",
  ls: "path",
  fetch: "url",
}

/**
 * hintOf is the one-line summary of the arguments: the value under the
 * tool's own key.
 *
 * ★ The value is taken from the **raw** arguments, not from the
 *   pretty-printed ones: the two hold the same values, and the raw text is
 *   the one that parses while the call is still streaming.
 */
function hintOf(name: string, args: string): string | undefined {
  const key = HINT_KEYS[name]
  if (!key) return undefined
  let parsed: Record<string, unknown>
  try {
    parsed = JSON.parse(args || "{}") as Record<string, unknown>
  } catch {
    return undefined
  }
  const v = parsed[key]
  return typeof v === "string" ? v : undefined
}

const PHASE_LABEL: Record<ToolPart["phase"], string> = {
  streaming: "reading arguments",
  running: "running",
  complete: "done",
}

export function ToolCard({ part }: { part: ToolPart }) {
  // ★ The name comes from the table, so the drawing is **total**: every
  //   name the table can produce has a glyph, or the build would not
  //   compile. There is no second fallback here to hide a mistake.
  const glyph = toolIcon(part.name)
  const Icon = GLYPHS[glyph]
  const title = TITLES[part.name] || part.name || "tool"
  const hint = part.name ? hintOf(part.name, part.argsText) : undefined
  const resultLines = part.result?.split("\n").length
  const args = prettyArgs(part.argsText)
  // ★ The card starts **closed**, whether the call is live or replayed.
  //
 //   A tool call is a step, not a message: a conversation that opens
  //   every step shows the reader two code blocks per step and buries the
  //   answer they came for. The header carries enough to say what the step
  //   was, and the body is one click away.
  const [open, setOpen] = useState(false)

  return (
    <div
      data-testid="tool-card"
      data-open={open}
      className="bg-muted/40 my-1.5 w-full overflow-hidden rounded-lg border"
    >
      <button
        type="button"
        data-testid="tool-card-toggle"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="text-muted-foreground hover:bg-muted/60 grid w-full grid-cols-[auto_auto_minmax(0,1fr)] items-center gap-x-2 gap-y-1 px-3 py-2 text-left text-xs sm:grid-cols-[auto_auto_minmax(0,1fr)_auto]"
      >
        <ChevronRightIcon
          data-testid="tool-chevron"
          className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")}
        />
        <Icon data-icon={glyph} className="size-3.5 shrink-0" />
        <span className="truncate font-medium">{title}</span>

        {/* ★ The argument is the one line that says what the call did, so
            it gets a row of its own: it is never squeezed out by the
            metadata, and a long path is truncated rather than pushed off
            the edge.

            ★ On a wide screen it sits **under the title**, in the title's
            own column, and the metadata stays on the first row at the far
            end. On a narrow screen it takes the whole row and the metadata
            drops below it — the order that matters: what the call was,
            then how it went. */}
        {hint && (
          <code
            data-testid="tool-hint"
            title={hint}
            className="col-span-3 truncate font-mono opacity-70 sm:col-span-1 sm:col-start-3 sm:row-start-2"
          >
            {hint}
          </code>
        )}

        {/* ★ The metadata is the one part of the header that may be
            dropped when the width runs out: the title and the argument are
            what the call *was*, and "12 lines / done" is decoration.

            ★ On a narrow screen it takes a row of its own and sits at the
            **end** of it, so it lines up with the right edge the way it
            does on a wide one. */}
        <span
          data-testid="tool-meta"
          className="col-span-3 flex items-center justify-end gap-2 text-[11px] tracking-wide uppercase opacity-70 sm:col-span-1 sm:col-start-4 sm:row-start-1"
        >
          {resultLines !== undefined && <span className="normal-case">{resultLines} lines</span>}
          <span>{PHASE_LABEL[part.phase]}</span>
        </span>
      </button>

      {open && (
        <div
          data-testid="tool-body"
          className="max-h-[70vh] overflow-y-auto overscroll-contain"
        >
          {part.argsText && (
            <CodeBlock
              testId="tool-args"
              lang="json"
              text={args.text}
              wrap
              className="max-w-full overflow-x-hidden border-t rounded-none border-x-0 border-b-0"
            />
          )}

          {part.result !== undefined && (
            <CodeBlock
              testId="tool-result"
              lang={looksLikeJson(part.result) ? "json" : undefined}
              text={part.result}
              className={cn(
                "max-h-80 overflow-y-auto overscroll-contain border-t rounded-none border-x-0 border-b-0",
                part.isError && "text-destructive",
              )}
            />
          )}
        </div>
      )}
    </div>
  )
}

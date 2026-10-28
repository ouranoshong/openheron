import {
  ChevronRightIcon,
  ClipboardIcon,
  FileOutputIcon,
  FolderSearchIcon,
  GlobeIcon,
  ListChecksIcon,
  PencilIcon,
  SearchIcon,
  TerminalIcon,
} from "lucide-react"
import { useState, type ComponentType } from "react"

import { CodeBlock } from "@/components/code-block"
import { looksLikeJson } from "@/lib/highlight"
import type { ToolPart } from "@/lib/types"
import { cn } from "@/lib/utils"

const ICONS: Record<string, ComponentType<{ className?: string }>> = {
  ls: FolderSearchIcon,
  glob: SearchIcon,
  grep: SearchIcon,
  read_file: ClipboardIcon,
  write_file: FileOutputIcon,
  edit_file: PencilIcon,
  execute: TerminalIcon,
  fetch: GlobeIcon,
  write_todos: ListChecksIcon,
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

function highlight(name: string, args: string): string | undefined {
  let parsed: Record<string, unknown>
  try {
    parsed = JSON.parse(args || "{}") as Record<string, unknown>
  } catch {
    return undefined
  }
  const key = {
    read_file: "file_path",
    write_file: "file_path",
    edit_file: "file_path",
    execute: "command",
    grep: "pattern",
    glob: "pattern",
    ls: "path",
    fetch: "url",
  }[name]
  const v = key ? parsed[key] : undefined
  return typeof v === "string" ? v : undefined
}

const PHASE_LABEL: Record<ToolPart["phase"], string> = {
  streaming: "reading arguments",
  running: "running",
  complete: "done",
}

export function ToolCard({ part }: { part: ToolPart }) {
  const Icon = ICONS[part.name] || TerminalIcon
  const title = TITLES[part.name] || part.name || "tool"
  const hint = part.name ? highlight(part.name, part.argsText) : undefined
  const resultLines = part.result?.split("\n").length
  const [open, setOpen] = useState(!part.fromHistory)

  return (
    <div
      data-testid="tool-card"
      data-open={open}
      className="bg-muted/40 my-1.5 w-full overflow-hidden rounded-lg border"
    >
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="text-muted-foreground hover:bg-muted/60 flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs"
      >
        <ChevronRightIcon
          className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")}
        />
        <Icon className="size-3.5 shrink-0" />
        <span className="shrink-0 font-medium">{title}</span>
        {hint && <code className="min-w-0 flex-1 truncate font-mono opacity-70">{hint}</code>}
        <span className="ml-auto flex shrink-0 items-center gap-2 text-[11px] tracking-wide uppercase">
          {resultLines !== undefined && <span className="normal-case opacity-70">{resultLines} lines</span>}
          <span>{PHASE_LABEL[part.phase]}</span>
        </span>
      </button>

      {open && part.argsText && (
        <CodeBlock
          testId="tool-args"
          lang="json"
          text={part.argsText}
          className="max-h-80 overflow-y-auto overscroll-contain border-t rounded-none border-x-0 border-b-0"
        />
      )}

      {open && part.result !== undefined && (
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
  )
}

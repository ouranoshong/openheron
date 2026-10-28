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

/**
 * 工具卡的图标与标题表。
 *
 * AG-UI 的 TOOL_CALL_* 事件带着完整的 toolCallName 与累积后的 arguments，
 * 但不描述「这个工具干什么、结果长什么样」—— 那部分知识只存在于后端。
 * 所以这里按名字建一张静态映射表。
 *
 * ★ 一律展示原始参数与原始结果，不做「人类可读摘要」。
 *   用户研究 agent 行为时需要看到真实内容，任何自作聪明的省略都会让
 *   观察失真。
 */

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

/** 展示用的标题，不沿用函数名原样。 */
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

/** 从参数里挑一个最能代表「这次调用在干嘛」的字段。 */
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
  // ★ `||` 而不是 `??`：toolName 为空串时 `"" ?? fallback` 求值成 ""，
  //   而 "" 不是 nullish —— 结果是渲染一个空标题。`||` 才正确。
  const Icon = ICONS[part.name] || TerminalIcon
  const title = TITLES[part.name] || part.name || "tool"
  const hint = part.name ? highlight(part.name, part.argsText) : undefined
  const resultLines = part.result?.split("\n").length
  // 历史默认折叠；实时运行中的卡片保持展开，用户需要看到过程。
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

      {/* ★ 参数一定是 JSON（AG-UI 的 toolCall.arguments），直接按 json 高亮。
       *   结果则要先判断 —— read_file 返回文件内容、execute 返回命令输出、
       *   ls 返回文件名列表，把它们当 JSON 高亮只会得到一片误导性的颜色。
       *
       *   折叠只是展示状态：展开后仍是完整原文，不截断、不摘要。限高让
       *   单个超大结果在卡片内部滚动，而不是把整段历史撑到数屏。 */}
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

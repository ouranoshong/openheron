import { ChevronRightIcon, LayersIcon } from "lucide-react"
import { useState } from "react"

import { ToolCard } from "@/components/tool-card"
import { phaseLabel, summarize, type ToolRun } from "@/lib/tool-groups"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/use-t"

/**
 * ToolGroup is a stretch of tool calls that follow each other.
 *
 * ★ **One call is not a group.** A step is one line, and one line is
 *   already quiet — a bar with "1 steps" on it would say less than the
 *   step itself.
 *
 * ★ **The bar is the run, not a list of the runs.** It says how many calls
 *   there were and which tools they used, and it is the only thing on the
 *   screen until the reader asks for the steps: a conversation that opens
 *   every call shows the reader a screen of steps and buries the answer
 *   they came for.
 *
 * ★ **A failure is never folded away.** A run that came back with an error
 *   says so on the bar, in the warning's own colour — the reader must not
 *   have to open anything to learn that something went wrong.
 */
export function ToolGroup({ run }: { run: ToolRun }) {
  // ★ The state is here even for a single call: the run grows while the
  //   turn runs, and a node that is unmounted and mounted again loses it.
  const [open, setOpen] = useState(false)
  const t = useT()
  const summary = summarize(run, t)

  if (run.parts.length === 1) return <ToolCard part={run.parts[0]!} />

  return (
    <div data-testid="tool-group" data-open={open} className="my-1.5 w-full">
      <button
        type="button"
        data-testid="tool-group-toggle"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="text-muted-foreground hover:text-foreground grid w-full grid-cols-[auto_auto_minmax(0,1fr)] items-center gap-x-2 gap-y-1 px-3 py-1 text-left text-xs sm:grid-cols-[auto_auto_minmax(0,1fr)_auto]"
      >
        <ChevronRightIcon
          data-testid="tool-group-chevron"
          className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")}
        />
        <LayersIcon aria-hidden="true" className="size-3.5 shrink-0" />
        <span data-testid="tool-group-summary" className="min-w-0 truncate">
          <span data-testid="tool-group-count" className="font-medium">
            {t("messages.toolSteps", { count: summary.count })}
          </span>
          {summary.titles.length > 0 ? (
            <span data-testid="tool-group-titles" className="opacity-80">
              {" · "}
              {summary.titles.join(", ")}
            </span>
          ) : null}
        </span>

        {/* ★ The metadata is the one part of the line that may be dropped
            when the width runs out — and the failure never is. */}
        <span
          data-testid="tool-group-meta"
          className="col-span-3 flex items-center justify-end gap-2 text-[11px] tracking-wide uppercase opacity-70 sm:col-span-1 sm:col-start-4 sm:row-start-1"
        >
          {summary.failed > 0 ? (
            <span data-testid="tool-group-failed" className="text-destructive normal-case">
              {t("messages.toolFailedCount", { count: summary.failed })}
            </span>
          ) : null}
          {summary.phase !== "failed" ? <span>{phaseLabel(summary.phase, t)}</span> : null}
        </span>
      </button>

      {open ? (
        <div data-testid="tool-group-body" className="mt-1">
          {run.parts.map((p) => (
            <ToolCard key={p.id} part={p} />
          ))}
        </div>
      ) : null}
    </div>
  )
}

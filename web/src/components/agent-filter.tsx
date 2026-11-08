import { useMemo } from "react"

import { collectAgents, formatTokens } from "@/lib/agents"
import { selectActiveContext, selectActiveUsage, selectMessages, useStore } from "@/lib/store"
import type { ContextUsage } from "@/lib/types"
import { cn } from "@/lib/utils"

export function AgentFilter({ className }: { className?: string }) {
  const usage = useStore(selectActiveUsage)
  const context = useStore(selectActiveContext)
  const messages = useStore(selectMessages)
  const filter = useStore((s) => s.agentFilter)
  const setFilter = useStore((s) => s.setAgentFilter)

  const agents = useMemo(() => collectAgents(messages, usage), [messages, usage])

  if (agents.length < 2) return null

  return (
    <div
      data-testid="agent-filter"
      // ★ It lives in the header now, so it has to be able to give way: the
      //   row shrinks and scrolls, the chips inside it do not.
      className={cn(
        "flex min-w-0 shrink items-center gap-1.5 overflow-x-auto",
        className,
      )}
    >
      <span className="text-muted-foreground shrink-0 text-xs">Agent</span>
      <FilterChip
        active={filter === null}
        label="All"
        testId="agent-filter-all"
        onClick={() => setFilter(null)}
      />
      {agents.map((agent) => (
        <FilterChip
          key={agent}
          active={filter === agent}
          label={agent}
          testId={`agent-filter-${agent}`}
          tokens={usage?.byAgent?.[agent]?.totalTokens}
          context={context?.[agent]}
          onClick={() => setFilter(filter === agent ? null : agent)}
        />
      ))}
    </div>
  )
}

function FilterChip({
  active,
  label,
  tokens,
  context,
  testId,
  onClick,
}: {
  active: boolean
  label: string
  tokens?: number
  context?: ContextUsage
  testId: string
  onClick: () => void
}) {
  // The two numbers are different quantities and must not be rendered as if
  // they were the same one: the occupancy is how full the context is now,
  // the spend is what the agent has consumed over the whole session.
  const title = context
    ? [
        // The first line keeps its old shape: the label and the spend.
        tokens === undefined ? label : `${label} — ${tokens} tokens`,
        context.contextWindow > 0
          ? `Context: ≈${formatTokens(context.usedTokens)} / ${formatTokens(context.contextWindow)}`
          : `Context: ≈${formatTokens(context.usedTokens)} tokens`,
        // Where the context gets cut, when it does.
        ...(context.summarizeAt && context.summarizeAt > 0
          ? [`Compacted at: ${context.summarizeAt} tokens`]
          : []),
      ].join("\n")
    : tokens === undefined
      ? label
      : `${label} — ${tokens} tokens`

  return (
    <button
      type="button"
      data-testid={testId}
      aria-pressed={active}
      onClick={onClick}
      title={title}
      className={cn(
        "shrink-0 rounded-md border px-2 py-0.5 text-xs transition-colors",
        active
          ? "border-border bg-accent text-accent-foreground"
          : "border-border/60 text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
      )}
    >
      {label}
      {context ? (
        // Both numbers ride on the chip: the occupancy is the actionable one,
        // the spend is what the agent has cost so far. They are different
        // quantities, so the tooltip names them.
        <span data-testid={`${testId}-context`} className="ml-1 tabular-nums">
          {context.contextWindow > 0
            ? `${formatTokens(context.usedTokens)}/${formatTokens(context.contextWindow)}`
            : `≈${formatTokens(context.usedTokens)}`}
        </span>
      ) : null}
      {tokens !== undefined && tokens > 0 ? (
        <span
          data-testid={`${testId}-tokens`}
          className="text-muted-foreground ml-1 tabular-nums"
        >
          {formatTokens(tokens)}
        </span>
      ) : null}
    </button>
  )
}

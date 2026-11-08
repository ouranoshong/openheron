import { Skeleton } from "@/components/ui/skeleton"
import { formatPercent, formatTokens } from "@/lib/agents"
import type { AgentBadge } from "@/lib/agent-info"
import type { ContextUsage } from "@/lib/types"
import { Loader2Icon } from "lucide-react"

const META =
  "text-muted-foreground mx-auto mt-1.5 flex min-h-4 w-full max-w-[var(--chat-column)] flex-wrap items-center gap-x-2 gap-y-0.5 px-1 text-[11px]"

export function ComposerMeta({
  badge,
  loading,
  error,
  context,
  notice,
  busy,
}: {
  badge: AgentBadge | null
  loading: boolean
  error?: string
  context?: ContextUsage
  // notice is a transient line about work the server is doing right now.
  notice?: string
  // busy says the session has a live turn that this surface does not own —
  // another tab, another window, another client. It is not a failure.
  busy?: boolean
}) {
  // The notice says the server is working on something. It is not an error,
  // and it does not replace anything — the rest of the line still stands.
  const noticeNode = notice ? (
    <span data-testid="composer-meta-notice" className="inline-flex items-center gap-1.5">
      <Loader2Icon aria-hidden="true" className="size-3 shrink-0 animate-spin" />
      {notice}
    </span>
  ) : null

  // The busy line says whose turn it is. The stop button beside it acts on
  // the session's turn, whoever owns it — that is the only way for this
  // surface to proceed.
  const busyNode = busy ? (
    <span data-testid="composer-meta-busy" className="inline-flex items-center gap-1.5">
      <Loader2Icon aria-hidden="true" className="size-3 shrink-0 animate-spin" />
      This session is running in another window — stop it to take over
    </span>
  ) : null

  if (loading && !badge) {
    return (
      <div data-testid="composer-meta" aria-hidden="true" className={META}>
        <Skeleton className="h-3 w-56" />
      </div>
    )
  }

  if (!badge) {
    return (
      <div data-testid="composer-meta" className={META}>
        {noticeNode}
        {busyNode}
        <span data-testid="composer-meta-error" title={error}>
          Agent info unavailable
        </span>
      </div>
    )
  }

  const model = `${badge.provider}/${badge.model}`
  const reasoningTitle =
    badge.reasoningAvailable.length > 0
      ? `Reasoning: ${badge.reasoning}\nAvailable: ${badge.reasoningAvailable.join(", ")}`
      : `Reasoning: ${badge.reasoning}`

  return (
    <div
      data-testid="composer-meta"
      aria-label="Current agent configuration"
      className={META}
    >
      {noticeNode}
      {busyNode}
      <span
        data-testid="composer-meta-agent"
        title={badge.description || badge.id}
        className="max-w-48 min-w-0 truncate"
      >
        Agent: {badge.id}
      </span>
      <span aria-hidden="true" className="shrink-0">
        ·
      </span>
      <span data-testid="composer-meta-model" title={model} className="min-w-0 truncate">
        {model}
      </span>
      <span aria-hidden="true" className="shrink-0">
        ·
      </span>
      <span
        data-testid="composer-meta-reasoning"
        title={reasoningTitle}
        className="min-w-0 truncate"
      >
        Reasoning: {badge.reasoning}
      </span>
      {context ? (
        <>
          <span aria-hidden="true" className="shrink-0">
            ·
          </span>
          <ContextMeter usage={context} />
        </>
      ) : null}
    </div>
  )
}

// ContextMeter shows how full this agent's context is.
//
// The number is the size of the agent's most recent request, not a sum over
// the session, and it is the provider's own count — so it is an estimate,
// and it is marked as one.
//
// A window of zero means the user configured none. There is then no ratio to
// show, and showing "0 %" would be a lie.
function ContextMeter({ usage }: { usage: ContextUsage }) {
  const known = usage.contextWindow > 0
  const ratio = known ? Math.min(1, usage.usedTokens / usage.contextWindow) : 0
  const label = known
    ? `Context: ≈${formatTokens(usage.usedTokens)} / ${formatTokens(usage.contextWindow)} (${formatPercent(ratio)})`
    : `Context: ≈${formatTokens(usage.usedTokens)} tokens`

  const lines = [
    // The number is the context's size. It is measured by the most recent
    // request — or by the summary, right after a manual compaction.
    "Tokens in this agent's context — the most recent request, or the summary after a compaction.",
  ]
  if (known) {
    lines.push(`Window: ${usage.contextWindow} tokens`)
  } else {
    lines.push("The model declares no context window.")
  }
  if (usage.summarizeAt && usage.summarizeAt > 0) {
    lines.push(`Compacted at: ${usage.summarizeAt} tokens`)
  }
  if (usage.summarized) {
    lines.push(`Compacted ${usage.summarized} time(s)`)
  }

  return (
    <span
      data-testid="composer-meta-context"
      title={lines.join("\n")}
      className="inline-flex min-w-0 items-center gap-1.5"
    >
      <span className="truncate tabular-nums">{label}</span>
      {known ? (
        <span
          aria-hidden="true"
          className="bg-muted/40 relative inline-block h-1 w-10 shrink-0 overflow-hidden rounded-full"
        >
          <span
            className={
              ratio >= 0.95
                ? "bg-destructive absolute inset-y-0 left-0"
                : ratio >= 0.8
                  ? "bg-amber-500 absolute inset-y-0 left-0"
                  : "bg-muted-foreground/60 absolute inset-y-0 left-0"
            }
            style={{ width: `${Math.round(ratio * 100)}%` }}
          />
        </span>
      ) : null}
    </span>
  )
}

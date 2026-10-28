import { Skeleton } from "@/components/ui/skeleton"
import type { AgentBadge } from "@/lib/agent-info"

const META =
  "text-muted-foreground mx-auto mt-1.5 flex min-h-4 max-w-3xl flex-wrap items-center gap-x-2 gap-y-0.5 px-1 text-[11px]"

export function ComposerMeta({
  badge,
  loading,
  error,
}: {
  badge: AgentBadge | null
  loading: boolean
  error?: string
}) {
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
    </div>
  )
}

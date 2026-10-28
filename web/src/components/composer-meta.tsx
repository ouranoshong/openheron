import { Skeleton } from "@/components/ui/skeleton"
import type { AgentBadge } from "@/lib/agent-info"

const META =
  "text-muted-foreground mx-auto mt-1.5 flex min-h-4 max-w-3xl flex-wrap items-center gap-x-2 gap-y-0.5 px-1 text-[11px]"

/**
 * 输入框下方的 agent / 模型 / 思考档位。
 *
 * ★ 只读信息，不放进 textarea 的 flex 行：放进那一行会挤压输入宽度，
 *   而且窄屏时标签会跟发送按钮抢空间。放在圆角框下方，欢迎页与底部
 *   版式共用同一份布局。
 */
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

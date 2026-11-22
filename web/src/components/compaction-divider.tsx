import { formatTokens } from "@/lib/agents"
import type { CompactionMark } from "@/lib/types"
import { useT } from "@/lib/use-t"

// CompactionDivider marks the boundary of the effective history.
//
// It is not a chat bubble: the message it stands for is a summary, and the
// model reads the summary instead of the messages above the line. Those
// messages stay in the transcript — compaction is not deletion — so the user
// can still read what was said before the cut.
//
// The numbers are estimates, and the divider says so. They are missing when
// the compaction was announced live: the estimate is measured against the
// transcript, which is written when the turn ends.
export function CompactionDivider({ mark }: { mark: CompactionMark }) {
  const t = useT()
  const saved =
    mark.savedTokens > 0
      ? t("messages.tokensSaved", { tokens: formatTokens(mark.savedTokens) })
      : ""
  const label =
    mark.superseded > 0
      ? t("messages.earlierMessages", { count: mark.superseded })
      : t("messages.earlierMessagesPlain")
  const text = [label, t("messages.compactedIntoSummary"), saved].filter(Boolean).join(" · ")

  const title = [
    t("messages.compactionReadsSummary"),
    t("messages.compactionStillThere"),
  ]
  if (mark.superseded > 0) {
    title.push(t("messages.compactionSuperseded", { count: mark.superseded }))
  }
  if (mark.savedTokens > 0) {
    title.push(t("messages.compactionEstimatedTokens", { count: mark.savedTokens }))
  }

  return (
    <div
      data-testid="compaction-divider"
      role="separator"
      aria-label={text}
      title={title.join("\n")}
      className="my-5 flex items-center gap-3"
    >
      <span aria-hidden="true" className="bg-border h-px flex-1" />
      <span className="text-muted-foreground shrink-0 text-[11px]">{text}</span>
      <span aria-hidden="true" className="bg-border h-px flex-1" />
    </div>
  )
}

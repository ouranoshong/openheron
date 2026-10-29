import { formatTokens } from "@/lib/agents"
import type { CompactionMark } from "@/lib/types"

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
  const saved = mark.savedTokens > 0 ? `≈${formatTokens(mark.savedTokens)} tokens saved` : ""
  const label = mark.superseded > 0 ? `${mark.superseded} earlier messages` : "Earlier messages"
  const text = [label, "compacted into a summary", saved].filter(Boolean).join(" · ")

  const title = [
    "The model reads a summary of the messages above this line, not the messages themselves.",
    "The messages are still in the transcript.",
  ]
  if (mark.superseded > 0) {
    title.push(`Superseded messages: ${mark.superseded}`)
  }
  if (mark.savedTokens > 0) {
    title.push(`Estimated tokens saved: ${mark.savedTokens}`)
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

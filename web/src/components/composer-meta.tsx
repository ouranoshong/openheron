import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { formatPercent, formatTokens } from "@/lib/agents"
import type { AgentBadge } from "@/lib/agent-info"
import type { ContextUsage } from "@/lib/types"
import { useT } from "@/lib/use-t"
import { cn } from "@/lib/utils"
import { Loader2Icon } from "lucide-react"
import type { ReactNode } from "react"

const META =
  "text-muted-foreground mx-auto mt-1.5 flex min-h-4 w-full max-w-[var(--chat-column)] flex-wrap items-center gap-x-2 gap-y-0.5 px-1 text-[11px] @max-lg:gap-y-1"

// ★ Joined is a separator and the item it introduces, as **one unit**.
//
// ★ A separator that is a flex item of its own lands wherever the wrapping
//   puts it: a line that ends with "·" reads as a dangling mark, and a line
//   that begins with one reads as a continuation of the line above. Bundling
//   the two means they wrap together — the separator never falls behind the
//   item it belongs to.
//
// ★ The unit does not shrink: an item that squeezes itself to fit the line
//   beside it is read as "deepsee…", and a name that is cut in half says
//   less than a name on the next line. ⇒ The unit wraps instead, and it
//   truncates only when the name is longer than the whole row.
//
// ★ `className` is how a unit steps aside on a narrow screen. ★ Hiding it is
//   not hiding the fact: the fact moves into a tooltip, where a phone can
//   still read it.
function Joined({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full shrink-0 items-center gap-1",
        className,
      )}
    >
      <span aria-hidden="true" className="shrink-0">
        ·
      </span>
      {children}
    </span>
  )
}

export function ComposerMeta({
  badge,
  loading,
  error,
  context,
  notice,
  busy,
  action,
  onPickAgent,
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
  // action is an entry that belongs **on this line**: the line under the
  // input box is where the things about the next conversation live.
  action?: ReactNode
  // ★ onPickAgent opens the agent picker. ★ The agent's name is a **door**,
  //   not a label: it opens the same picker the `/agents` command opens, so
  //   there is one list and one behavior, not two.
  onPickAgent(): void
}) {
  const t = useT()

  // The notice says the server is working on something. It is not an error,
  // and it does not replace anything — the rest of the line still stands.
  //
  // ★ On a narrow screen it takes a row of its own: a state sentence and the
  //   facts beside it are two different readings, and sharing a line makes
  //   both of them harder.
  const noticeNode = notice ? (
    <span
      data-testid="composer-meta-notice"
      className="inline-flex @max-lg:basis-full items-center gap-1.5"
    >
      <Loader2Icon aria-hidden="true" className="size-3 shrink-0 animate-spin" />
      {notice}
    </span>
  ) : null

  // The busy line says whose turn it is. The stop button beside it acts on
  // the session's turn, whoever owns it — that is the only way for this
  // surface to proceed.
  const busyNode = busy ? (
    <span
      data-testid="composer-meta-busy"
      className="inline-flex @max-lg:basis-full items-center gap-1.5"
    >
      <Loader2Icon aria-hidden="true" className="size-3 shrink-0 animate-spin" />
      {t("composer.meta.busyElsewhere")}
    </span>
  ) : null

  // ★ The line's own content changes **shape** with the agent's state — a
  //   skeleton, an error, the badge. ★ The action is rendered beside it, and
  //   it must not move when the shape changes: React reconciles children by
  //   index, so an action that shifts because a sibling appeared before it
  //   is an action that is unmounted and mounted again — and a dialog that
  //   lives in it closes when it is.
  //
  //   ⇒ The content goes into one wrapper and the action sits beside it, so
  //   the action's index never changes.
  let body: ReactNode
  if (loading && !badge) {
    body = (
      <div aria-hidden="true">
        <Skeleton className="h-3 w-56" />
      </div>
    )
  } else if (!badge) {
    body = (
      <>
        {noticeNode}
        {busyNode}
        <span data-testid="composer-meta-error" title={error}>
          {t("composer.meta.agentUnavailable")}
        </span>
      </>
    )
  } else {
    const model = `${badge.provider}/${badge.model}`
    const reasoning = t("composer.reasoning", { reasoning: badge.reasoning })
    const reasoningTitle =
      badge.reasoningAvailable.length > 0
        ? `${reasoning}\n${t("composer.meta.reasoningAvailable", {
            available: badge.reasoningAvailable.join(", "),
          })}`
        : reasoning

    body = (
      <>
        {noticeNode}
        {busyNode}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          // ★ The name is an **entry**, and it is rendered like the entries
          //   beside it: the same ghost button, the same size, the same hover.
          //   ★ A name that underlines on hover says "this is a link to
          //   somewhere else" — ★ this one stays on this line and opens the
          //   picker that already lives on this line. ⇒ No underline.
          //   ★ On a narrow column the padding goes: ★ the icon next to it
          //   needs a little air around it, a **word** does not — and the
          //   row is one line only if the name does not push the facts over.
          className="text-muted-foreground hover:text-foreground h-6 max-w-[min(12rem,100%)] gap-1 px-1.5 text-[11px] @max-3xl:border-0 @max-3xl:px-0"
          onClick={onPickAgent}
          data-testid="composer-meta-agent"
          // ★ The tooltip carries **two** things: what this agent is, and
          //   what the click does. ★ A name on its own is a fact; a name you
          //   can click is a door, and a door that says nothing about where it
          //   leads is a door nobody uses.
          title={[badge.description || badge.id, t("composer.meta.switchAgent")].join("\n")}
        >
          <span data-testid="composer-meta-agent-name" className="min-w-0 truncate">
            {/* ★ The word "Agent" is a **label for the wide layout**: there is
                room for it there, and the row is a ledger of names. ★ On a
                narrow screen the row has to fit on one line, and the header
                already says "Agent" — so the prefix steps aside and the name
                stays. */}
            <span className="@max-lg:hidden">{t("composer.meta.agentLabel")}</span>
            {badge.id}
          </span>
        </Button>
        {/* ★ The model is the item that **gives way** on a narrow screen.
            ★ The priority is written down: the agent's name and the context
            ★ occupancy are never cut — they are the identity and the number
            ★ — and the model name is the one that truncates.
            ★ The unit may shrink, and it may take at most what is left after
            ★ the protected items (the agent's name, the context, the three
            ★ entries and the gaps — about 12rem). ⇒ The row keeps **one**
            ★ line, and the model's full name stays in its tooltip. */}
        <Joined className="@max-3xl:shrink @max-3xl:max-w-[min(24rem,calc(100%_-_12rem))]">
          <span
            data-testid="composer-meta-model"
            // ★ The model's tooltip carries the reasoning level as well. ★ On
            //   a narrow screen the reasoning **steps aside** — it is the
            //   item that costs the most space and says the least — and a
            //   fact that steps aside has to land somewhere a phone can
            //   still read it.
            title={`${model}\n${reasoningTitle}`}
            className="min-w-0 max-w-[min(24rem,100%)] truncate"
          >
            {model}
          </span>
        </Joined>
        {/* ★ The reasoning level steps aside on a narrow screen: it is in the
            model's tooltip there. ★ The unit is hidden whole — separator and
            item together — so no line is left with a dangling "·". */}
        <Joined className="@max-lg:hidden">
          <span
            data-testid="composer-meta-reasoning"
            title={reasoningTitle}
            className="min-w-0 max-w-[min(12rem,100%)] truncate"
          >
            {reasoning}
          </span>
        </Joined>
        {context ? (
          <Joined>
            <ContextMeter usage={context} />
          </Joined>
        ) : null}
      </>
    )
  }

  return (
    <div
      data-testid="composer-meta"
      aria-label={t("composer.meta.ariaLabel")}
      className={META}
    >
      {/* ★ `contents` puts the line's own children back into the flex row:
          the wrapper has no box of its own, so the gaps and the wrapping are
          the row's. */}
      <div className="contents">{body}</div>
      {action}
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
  const t = useT()
  const known = usage.contextWindow > 0
  const ratio = known ? Math.min(1, usage.usedTokens / usage.contextWindow) : 0
  const label = known
    ? t("composer.meta.context.label", {
        used: formatTokens(usage.usedTokens),
        window: formatTokens(usage.contextWindow),
        percent: formatPercent(ratio),
      })
    : t("common.contextUsed", { used: formatTokens(usage.usedTokens) })
  // ★ The narrow line keeps the **number that matters** and the bar: the
  //   share is what the eye watches, and the bar says the same thing. ★ The
  //   tokens and the window are in the tooltip — a phone reads a tooltip
  //   on demand, and a line reads at a glance.
  const shortLabel = known ? formatPercent(ratio) : `≈${formatTokens(usage.usedTokens)}`

  const lines = [
    // ★ The full sentence, because the narrow line does not carry it.
    label,
    // The number is the context's size. It is measured by the most recent
    // request — or by the summary, right after a manual compaction.
    t("composer.meta.context.tokens"),
  ]
  if (known) {
    lines.push(t("composer.meta.context.window", { tokens: usage.contextWindow }))
  } else {
    lines.push(t("composer.meta.context.noWindow"))
  }
  if (usage.summarizeAt && usage.summarizeAt > 0) {
    lines.push(t("composer.meta.context.compactedAt", { tokens: usage.summarizeAt }))
  }
  if (usage.summarized) {
    lines.push(t("composer.meta.context.compactedTimes", { count: usage.summarized }))
  }

  return (
    <span
      data-testid="composer-meta-context"
      title={lines.join("\n")}
      className="inline-flex min-w-0 max-w-full items-center gap-1.5"
    >
      <span className="min-w-0 max-w-[min(24rem,100%)] truncate tabular-nums">
        {/* ★ Two sentences, one line: the narrow one is the short label, the
            wide one the whole sentence. ★ Only one of them is displayed —
            the other is `display: none`, so it is not read aloud either. */}
        <span className="hidden @lg:inline">{label}</span>
        <span className="@lg:hidden">{shortLabel}</span>
      </span>
      {known ? (
        <span
          aria-hidden="true"
          // ★ The bar is the **second** encoding of the same number — the
          //   share is right beside it. ★ On a narrow screen it is shorter:
          //   the row has to hold the facts **and** the three entries on one
          //   line, and every pixel of that line comes out of the chat.
          className="bg-muted/40 relative inline-block h-1 w-10 shrink-0 overflow-hidden rounded-full @max-3xl:w-8"
        >
          <span
            className={
              ratio >= 0.95
                ? "bg-destructive absolute inset-y-0 left-0"
                : ratio >= 0.8
                  ? "bg-warning absolute inset-y-0 left-0"
                  : "bg-muted-foreground/60 absolute inset-y-0 left-0"
            }
            style={{ width: `${Math.round(ratio * 100)}%` }}
          />
        </span>
      ) : null}
    </span>
  )
}

import { ArrowDownIcon, SquareIcon } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import { AgentFilter } from "@/components/agent-filter"
import { BrandMark } from "@/components/brand-mark"
import { MessageParts } from "@/components/message-parts"
import { collectAgents, filterByAgent } from "@/lib/agents"
import { Skeleton } from "@/components/ui/skeleton"
import {
  selectActiveError,
  selectActiveLoading,
  selectActiveRetry,
  selectMessages,
  selectStatus,
  useStore,
} from "@/lib/store"
import { turnFlags } from "@/lib/turns"
import type { Message, TurnStatus } from "@/lib/types"
import { cn } from "@/lib/utils"

export function MessageList() {
  const messages = useStore(selectMessages)
  const status = useStore(selectStatus)
  const error = useStore(selectActiveError)
  const retry = useStore(selectActiveRetry)
  const loading = useStore(selectActiveLoading)

  const activeId = useStore((s) => s.activeId)
  const agentFilter = useStore((s) => s.agentFilter)
  const scrollRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true)
  const [pinState, setPinState] = useState<{ id: string | null; pinned: boolean }>({
    id: activeId,
    pinned: true,
  })
  const pinned = pinState.id === activeId ? pinState.pinned : true

  const visible = useMemo(() => filterByAgent(messages, agentFilter), [messages, agentFilter])

  const flags = useMemo(() => turnFlags(visible), [visible])

  const multiAgent = useMemo(() => collectAgents(messages).length > 1, [messages])

  const BOTTOM_TOLERANCE = 32

  const atBottom = () => {
    const el = scrollRef.current
    if (!el) return true
    return el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_TOLERANCE
  }

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const onScroll = () => {
      const next = atBottom()
      pinnedRef.current = next
      setPinState((prev) =>
        prev.id === activeId && prev.pinned === next ? prev : { id: activeId, pinned: next },
      )
    }
    el.addEventListener("scroll", onScroll, { passive: true })
    return () => el.removeEventListener("scroll", onScroll)
  }, [activeId])

  useEffect(() => {
    pinnedRef.current = true
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [activeId])

  useEffect(() => {
    const el = scrollRef.current
    if (el && pinnedRef.current) el.scrollTop = el.scrollHeight
  })

  const jumpToLatest = () => {
    pinnedRef.current = true
    setPinState({ id: activeId, pinned: true })
    const el = scrollRef.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <AgentFilter />
      <div
        ref={scrollRef}
        data-testid="message-list"
        aria-busy={loading || undefined}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        <div className="mx-auto max-w-3xl px-4 py-6">
          {loading && messages.length === 0 && <MessageSkeleton />}

          {!loading && messages.length === 0 && !error && <Welcome />}

          {visible.map((m, i) => (
            <MessageRow
              key={m.id}
              message={m}
              startsTurn={flags[i]?.startsTurn ?? false}
              showRole={flags[i]?.showRole ?? false}
              showAgent={multiAgent}
              first={i === 0}
            />
          ))}

          {retry && (
            <p className="text-muted-foreground mt-4 text-xs" role="status">
              {retry.message}
            </p>
          )}

          {error && (
            <div
              data-testid="run-error"
              role="alert"
              className="border-destructive/40 bg-destructive/10 text-destructive mt-4 space-y-2 rounded-lg border p-3 text-sm"
            >
              <p className="font-medium">Run failed</p>
              <p className="text-xs break-words opacity-90">{error.message}</p>
            </div>
          )}

          <StatusLine status={status} />
        </div>
      </div>

      {!pinned && (
        <button
          type="button"
          data-testid="jump-to-latest"
          aria-label="Jump to latest"
          onClick={jumpToLatest}
          className="bg-background hover:bg-accent hover:text-accent-foreground border-border absolute right-4 bottom-4 grid size-9 place-items-center rounded-full border shadow-md"
        >
          <ArrowDownIcon className="size-4" />
        </button>
      )}
    </div>
  )
}

export function EmptyHero() {
  return (
    <div className="text-muted-foreground mb-6 flex flex-col items-center gap-1 text-center">
      <BrandMark className="mb-1 size-16" />
      <p className="text-foreground text-xl font-semibold">openheron</p>
      <p className="text-sm">Ask anything about this workspace.</p>
    </div>
  )
}

function Welcome() {
  return (
    <div className="flex h-full min-h-48 flex-col items-center justify-center text-center">
      <EmptyHero />
    </div>
  )
}

function StatusLine({ status }: { status: TurnStatus }) {
  if (status === "running") {
    return (
      <p className="text-muted-foreground mt-4 flex items-center gap-1.5 text-xs" role="status">
        <span className="bg-sky-500 size-1.5 animate-pulse rounded-full" />
        Working…
      </p>
    )
  }
  return null
}

function MessageRow({
  message,
  startsTurn,
  showRole,
  showAgent,
  first,
}: {
  message: Message
  startsTurn: boolean
  showRole: boolean
  showAgent: boolean
  first: boolean
}) {
  const isUser = message.role === "user"
  return (
    <article
      data-testid="message"
      data-role={message.role}
      className={cn(
        "flex flex-col",
        isUser && "items-end",
        !first && (startsTurn ? "mt-6" : "mt-1.5"),
      )}
    >
      <div
        className={cn(
          "min-w-0",
          isUser
            ? "bg-muted max-w-[85%] rounded-2xl px-4 py-2.5"
            :
              "w-full",
        )}
      >
        {!isUser && showRole && (
          <p
            data-testid="role-label"
            className="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase"
          >
            {showAgent && message.agent ? message.agent : message.role}
          </p>
        )}
        <MessageParts parts={message.parts} role={message.role} />
      </div>
    </article>
  )
}

function MessageSkeleton() {
  return (
    <div data-testid="message-skeleton" aria-hidden="true" className="space-y-6">
      <div className="flex justify-end">
        <Skeleton className="h-10 w-2/5 rounded-2xl" />
      </div>
      <div className="space-y-2">
        <Skeleton className="h-3 w-3/4" />
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-1/2" />
      </div>
      <div className="flex justify-end">
        <Skeleton className="h-10 w-1/3 rounded-2xl" />
      </div>
      <div className="space-y-2">
        <Skeleton className="h-3 w-2/3" />
        <Skeleton className="h-3 w-5/6" />
      </div>
    </div>
  )
}

export function StopButton() {
  const stop = useStore((s) => s.stop)
  return (
    <button
      type="button"
      aria-label="Stop generating"
      onClick={() => void stop()}
      className="bg-primary text-primary-foreground hover:bg-primary/90 grid size-8 shrink-0 place-items-center rounded-lg"
    >
      <SquareIcon data-testid="stop-icon" className="size-3.5 fill-current" strokeWidth={0} />
    </button>
  )
}

export { ArrowDownIcon }

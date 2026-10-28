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

/**
 * 消息流。
 *
 * ★ 自动滚动的判据是「用户离底部有多近」，不是「有没有新消息」——
 *   用户往上翻看历史时来了新 token，把视口拽回底部是最烦人的一种行为。
 *   只有本来就在底部附近（80px 内）才跟着走。
 */
export function MessageList() {
  // ★ 走 store 里的 selector 而不是内联表达式：内联的 `?? []` 每次渲染
  //   造一个新数组，zustand 判定快照一直在变 → 无限重渲染。
  const messages = useStore(selectMessages)
  const status = useStore(selectStatus)
  const error = useStore(selectActiveError)
  const retry = useStore(selectActiveRetry)
  const loading = useStore(selectActiveLoading)

  const activeId = useStore((s) => s.activeId)
  const agentFilter = useStore((s) => s.agentFilter)
  const scrollRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true)
  /**
   * 与 pinnedRef 同步的渲染状态。
   *
   * ★ 带上 activeId 是为了在切换会话时“就地”重置，而不是在 effect 里
   *   setState —— 后者会多触发一轮渲染，也触发 lint 的
   *   set-state-in-effect。新会话第一次渲染时 id 对不上，pin 自然为 true。
   */
  const [pinState, setPinState] = useState<{ id: string | null; pinned: boolean }>({
    id: activeId,
    pinned: true,
  })
  const pinned = pinState.id === activeId ? pinState.pinned : true

  /**
   * 按 agent 过滤后的消息。
   *
   * ★ 用 useMemo 而不是放进 selector：过滤会**造新数组**，selector 每次
   *   返回新数组会被 zustand 判定「快照一直在变」→ 无限重渲染
   *   （见 store.ts §5.2）。派生值在组件里算就够。
   */
  const visible = useMemo(() => filterByAgent(messages, agentFilter), [messages, agentFilter])

  /**
   * 轮次标记基于**过滤后**的列表算。
   *
   * ★ 不能沿用过滤前的 flags：过滤会改变相邻关系，按老下标取会张冠李戴
   *   （过滤后第 2 条拿到的可能是过滤前第 5 条的标记）。而过滤后的首条
   *   本来就该显示角色标签 —— 它现在是这段视图的开头。
   */
  const flags = useMemo(() => turnFlags(visible), [visible])

  /**
   * 会话里是否有多于一个 agent。
   *
   * ★ 单 agent 会话**不显示** agent 名 —— 那时每条消息都标着同一个名字，
   *   标签等于没信息，还把「Assistant」这个有用的角色提示挤掉了。
   */
  const multiAgent = useMemo(() => collectAgents(messages).length > 1, [messages])

  /**
   * 判定「还在底部」的容差。
   *
   * ★ 32px（约一行半）而不是更大的值：
   *   内容行高约 20px，阈值 80px 等于「用户上滑 4 行仍被拽回去」。
   *   而只要内容比视口高出不到阈值，「滚到顶」和「在底部」就分不开 ——
   *   一次上滑会被误判成还在底部。
   *
   *   也不能取 0：我们自己 `scrollTop = scrollHeight` 之后浏览器上报的
   *   位置偶尔差一两个像素，阈值 0 会让刚滚到底就被判成「离开了」。
   */
  const BOTTOM_TOLERANCE = 32

  const atBottom = () => {
    const el = scrollRef.current
    if (!el) return true
    return el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_TOLERANCE
  }

  /**
   * 跟随状态**只**由 scroll 事件决定。
   *
   * ★ 之前这里还有一个「每次渲染后重新测量」的 effect，它会把跟随
   *   打断：测量发生在**内容长高之后、滚动之前**，所以内容一次涨过
   *   阈值（80px）就被判定成「用户已经翻到上面去了」，于是不再滚动。
   *   最典型的受害者是**异步加载的历史** —— 首屏 0 条，加载完一次撑满，
   *   打开一个有历史的会话就停在最开头那几条，用户看到的是对话的起点
   *   而不是最新回复（实测距底部 1650px）。
   *
   *   重测看着像「兜底」，实际是它自己在破坏跟随。删掉。
   */
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

  /**
   * 切换会话：滚动容器是同一个 DOM 节点，scrollTop 会从上一个会话留下来。
   * 不重置的话，切到一个更长的会话会停在中间某处 —— 用户点会话是想看
   * 它的最新内容，不是接着上一次的位置。
   */
  useEffect(() => {
    pinnedRef.current = true
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [activeId])

  /** 内容变化时跟随到底部。 */
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

/**
 * 空状态的品牌区。
 *
 * ★ 导出给 shell 用：在「还没有对话」时它要和 composer 一起**整体居中**，
 *   而不在消息流里各自居中 —— 后者会把输入框留在屏幕最底部，与常见的
 *   空状态布局不一致。
 */
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

/**
 * 一条消息。
 *
 * ★ 两种宽度规则，**必须**这样分：
 *
 *   user      —— 右对齐、缩到内容宽度、最宽 85%，带气泡底色。
 *   assistant —— **通栏**。不加横向内边距、不加气泡。
 *
 *   之前两边都套了 `max-w-[85%]`，于是每条消息各自缩到内容宽度 ——
 *   实测同一屏里四条消息分别是 225 / 217 / 213 / 325 px，参差不齐；
 *   而同一条 assistant 里的代码块还比旁边的正文窄一圈（气泡的 px-4）。
 *
 *   assistant 通栏之后，同一条消息里的正文、代码块、工具卡宽度一致，
 *   不同消息之间也一致 —— 这就是「对齐并统一」。
 */
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
  /** 会话里有多个 agent 时，标签显示**是谁**而不是角色。 */
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
            : // 通栏：宽度由父容器决定，内部各 block 自然等宽
              "w-full",
        )}
      >
        {!isUser && showRole && (
          <p
            data-testid="role-label"
            className="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase"
          >
            {/* ★ 多 agent 下标签是**谁**（agent 名），不是角色 ——
                「Assistant」在多 agent 会话里是废话：三条 assistant 消息
                可能分属三个 agent。只有 agent 缺失时才退回角色。 */}
            {showAgent && message.agent ? message.agent : message.role}
          </p>
        )}
        <MessageParts parts={message.parts} role={message.role} />
      </div>
    </article>
  )
}

/**
 * 历史加载占位。
 *
 * ★ 切会话时 store 会先清空消息再请求，不能只显示空白 —— 那看起来像
 *   「这个会话没有内容」。骨架沿用消息流的左右版式，数据到达后直接替换。
 */
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

/** 停止按钮。放在 composer 里，但逻辑上属于轮次控制。 */
export function StopButton() {
  const stop = useStore((s) => s.stop)
  return (
    <button
      type="button"
      aria-label="Stop generating"
      onClick={() => void stop()}
      // ★ 与发送按钮同一槽位、同一尺寸与主色：运行中它是唯一的动作按钮，
      //   视觉重量必须能顶替发送按钮，而不是像一个附带的 ghost 图标。
      className="bg-primary text-primary-foreground hover:bg-primary/90 grid size-8 shrink-0 place-items-center rounded-lg"
    >
      {/*
        ★ 停止必须是**实心**方块。默认的 Square 是描边图标，在 32px 的
          按钮里看起来像一个空复选框 —— 实测用户会以为它是「未选中」，
          而不是「中断」。strokeWidth=0 去掉矩形边框，fill-current 让方块
          跟着前景色实心显示。
      */}
      <SquareIcon data-testid="stop-icon" className="size-3.5 fill-current" strokeWidth={0} />
    </button>
  )
}

export { ArrowDownIcon }

import { PanelLeftIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { ApprovalCard } from "@/components/approval-card"
import { BrandMark } from "@/components/brand-mark"
import { Composer } from "@/components/composer"
import { EmptyHero, MessageList } from "@/components/message-list"
import { Sidebar } from "@/components/sidebar"
import { selectIsEmptyView, selectStatus, useStore } from "@/lib/store"
import { cn } from "@/lib/utils"

type Dot = { className: string; label: string }

/**
 * 应用外壳。
 *
 * ★ 待审批优先于运行中：等审批时轮次已经结束（RUN_FINISHED 带 interrupt），
 *   status 是 idle，只按它判断会让状态点在最需要提醒的那一刻显示成「空闲」。
 */
const DOT: Record<string, Dot> = {
  idle: { className: "bg-muted-foreground/40", label: "Idle" },
  running: { className: "bg-sky-500 animate-pulse", label: "Running" },
  "awaiting-approval": { className: "bg-warning", label: "Waiting for approval" },
  error: { className: "bg-destructive", label: "Failed" },
}

export function Shell() {
  const [collapsed, setCollapsed] = useState(false)
  const status = useStore(selectStatus)
  const isEmptyView = useStore(selectIsEmptyView)
  const refreshList = useStore((s) => s.refreshList)
  const openSession = useStore((s) => s.openSession)
  const activeId = useStore((s) => s.activeId)

  /**
   * 挂载时：拉列表，并把上次停在的那条会话打开。
   *
   * ★ activeId 从 localStorage 恢复，但**光恢复 id 不会加载内容** ——
   *   对话状态是懒加载的。于是刷新后侧栏高亮着某条会话，聊天区却是空的，
   *   表现是「刷新一下，刚才那个对话就没了」。
   *
   * ★ StrictMode 下 effect 会跑两次：两次 GET /v2/sessions + 两次
   *   GET /v2/sessions/:id/messages，都是读操作，不建任何东西。
   *   不要为了「只跑一次」加 ref 守卫 —— 那种写法在依赖变化时反而漏更新。
   */
  useEffect(() => {
    void refreshList()
    if (activeId) void openSession(activeId)
  }, [refreshList, openSession, activeId])

  const dot = DOT[status] ?? DOT.idle!

  return (
    <div className="bg-muted/30 flex h-dvh flex-col overflow-hidden p-2">
      <div className="bg-background flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg md:flex-row">
        {!collapsed && <Sidebar />}

        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center gap-2 px-4">
            <button
              type="button"
              onClick={() => setCollapsed((v) => !v)}
              className="hover:bg-accent hover:text-accent-foreground grid size-8 shrink-0 place-items-center rounded-md"
              aria-label={collapsed ? "Show sidebar" : "Hide sidebar"}
            >
              <PanelLeftIcon className="size-4" />
            </button>
            <BrandMark className="size-5" />
            <span className="min-w-0 truncate text-sm font-medium">openheron</span>
            <span
              data-testid="status-dot"
              className={cn("size-1.5 shrink-0 rounded-full", dot.className)}
              title={dot.label}
              aria-label={dot.label}
              role="status"
            />
          </header>

          {isEmptyView ? (
            /* ★ 空状态：品牌区与输入框**整体**垂直居中。
             *
             *   消息流版式把 composer 固定在底部，而没有任何对话时那一屏
             *   只有输入框孤零零贴着下沿 —— 与常见空状态布局不一致。
             *   这里把两者放进同一个居中的组里。
             *
             *   代价：第一句发出去时布局会切一次（输入框从中间移到底部）。
             *   这是空状态布局的固有取舍 —— 两种版式没法同时成立。
             */
            <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
              <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-4 pb-[8vh]">
                <div className="w-full max-w-3xl">
                  <EmptyHero />
                  {/* inset={false}：留白交给上面那个居中的容器 */}
                  <Composer inset={false} />
                </div>
              </div>
            </main>
          ) : (
            <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
              <MessageList />
              {/* 待审批时审批卡**取代**输入框：轮次已结束，输入框本来就不能用。 */}
              {status === "awaiting-approval" ? <ApprovalCard /> : <Composer />}
            </main>
          )}
        </div>
      </div>
    </div>
  )
}

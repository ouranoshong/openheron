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
            <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
              <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-4 pb-[8vh]">
                <div className="w-full max-w-3xl">
                  <EmptyHero />
                  <Composer inset={false} />
                </div>
              </div>
            </main>
          ) : (
            <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
              <MessageList />
              {status === "awaiting-approval" ? <ApprovalCard /> : <Composer />}
            </main>
          )}
        </div>
      </div>
    </div>
  )
}

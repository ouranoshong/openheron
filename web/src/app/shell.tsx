import { PanelLeftIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { ApprovalCard } from "@/components/approval-card"
import { AgentFilter } from "@/components/agent-filter"
import { BrandMark } from "@/components/brand-mark"
import { Composer } from "@/components/composer"
import { EmptyHero, MessageList } from "@/components/message-list"
import { Sidebar } from "@/components/sidebar"
import { ThemeToggle } from "@/components/theme-toggle"
import { selectIsEmptyView, selectStatus, useStore } from "@/lib/store"
import { cn } from "@/lib/utils"

type Dot = { className: string; label: string }

const SIDEBAR_KEY = "openheron.sidebar.collapsed"

const DOT: Record<string, Dot> = {
  idle: { className: "bg-muted-foreground/40", label: "Idle" },
  running: { className: "bg-info animate-pulse", label: "Running" },
  "awaiting-approval": { className: "bg-warning", label: "Waiting for approval" },
  error: { className: "bg-destructive", label: "Failed" },
}

export function Shell() {
  // ★ The sidebar's collapsed state is remembered. A reload that brings the
  //   sidebar back is a small thing that happens every session, and the
  //   state is a preference, not a fact about the conversation.
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(SIDEBAR_KEY) === "1"
    } catch {
      // A browser that refuses storage still gets a working sidebar.
      return false
    }
  })
  const status = useStore(selectStatus)
  const isEmptyView = useStore(selectIsEmptyView)
  const refreshList = useStore((s) => s.refreshList)
  const openSession = useStore((s) => s.openSession)
  const watchSession = useStore((s) => s.watchSession)
  const activeId = useStore((s) => s.activeId)

  const toggleSidebar = () =>
    setCollapsed((v) => {
      const next = !v
      try {
        localStorage.setItem(SIDEBAR_KEY, next ? "1" : "0")
      } catch {
        // Not being able to remember is not a reason to break the toggle.
      }
      return next
    })

  useEffect(() => {
    void refreshList()
    if (activeId) void openSession(activeId)
  }, [refreshList, openSession, activeId])

  // ★ The session's announcement channel follows the session on screen.
  //   It is what tells this surface that an approval was answered in
  //   another tab: without it the card stays and every answer it offers
  //   is refused.
  useEffect(() => {
    watchSession(activeId)
  }, [watchSession, activeId])

  const dot = DOT[status] ?? DOT.idle!

  return (
    // ★ The frame is for the small screens only. On a desktop the window is
    //   the frame, and a grey border around the whole app makes the content
    //   look narrower than it is — which is the opposite of what a wider
    //   column is for.
    <div className="bg-muted/30 flex h-dvh flex-col overflow-hidden p-2 md:p-0">
      <div className="bg-background flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg md:rounded-none md:flex-row">
        {!collapsed && <Sidebar />}

        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center gap-2 px-4">
            <button
              type="button"
              onClick={toggleSidebar}
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
            {/* ★ The agent filter and the context occupancy moved into the
                header: they were a second horizontal bar of their own, and
                a chat is mostly text. ★ The filter sits **left**, next to the
                title it belongs to — the numbers on the chips are about the
                conversation you are reading, and the eye reaches for them
                there, not across the window. The theme switch takes the far
                end of the row. */}
            <AgentFilter />
            <ThemeToggle className="ml-auto" />
          </header>

          {isEmptyView ? (
            <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
              <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-4 pb-[8vh]">
                <div className="w-full max-w-[var(--chat-column)]">
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

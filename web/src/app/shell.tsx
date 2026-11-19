import { PanelLeftIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { ApprovalCard } from "@/components/approval-card"
import { AgentFilter } from "@/components/agent-filter"
import { BrandMark } from "@/components/brand-mark"
import { Composer } from "@/components/composer"
import { EmptyHero, MessageList } from "@/components/message-list"
import { Sidebar } from "@/components/sidebar"
import { SidebarDrawer } from "@/components/sidebar-drawer"
import { ThemeToggle } from "@/components/theme-toggle"
import { selectIsEmptyView, selectStatus, useStore } from "@/lib/store"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/lib/use-media-query"

type Dot = { className: string; label: string }

const SIDEBAR_KEY = "openheron.sidebar.collapsed"

// ★ The breakpoint the layout turns on. ★ It is Tailwind's `md`, and it is
//   the same number the stylesheet uses — a second breakpoint would be a
//   second truth, and the two would disagree on the tablet.
const WIDE = "(min-width: 768px)"

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
  // ★ The drawer is its own state. ★ Collapsing is a preference about a
  //   desktop; the drawer is about a screen — and a small screen has no
  //   use for the first one.
  const [drawerOpen, setDrawerOpen] = useState(false)
  const wide = useMediaQuery(WIDE)
  const status = useStore(selectStatus)
  const isEmptyView = useStore(selectIsEmptyView)
  const refreshList = useStore((s) => s.refreshList)
  const refreshProjects = useStore((s) => s.refreshProjects)
  const openSession = useStore((s) => s.openSession)
  const watchSession = useStore((s) => s.watchSession)
  const activeId = useStore((s) => s.activeId)

  const toggleSidebar = () => {
    if (!wide) {
      setDrawerOpen((v) => !v)
      return
    }
    setCollapsed((v) => {
      const next = !v
      try {
        localStorage.setItem(SIDEBAR_KEY, next ? "1" : "0")
      } catch {
        // Not being able to remember is not a reason to break the toggle.
      }
      return next
    })
  }

  // ★ The drawer has no business being open on a wide screen: it would
  //   cover the chat that the docked sidebar now sits beside. ★ The state
  //   is dropped **during render**, not in an effect: an effect would set
  //   it a frame later, and the drawer would be open for that frame.
  //   React re-renders immediately, so the drawer is never drawn open.
  const [wasWide, setWasWide] = useState(wide)
  if (wasWide !== wide) {
    setWasWide(wide)
    if (wide) setDrawerOpen(false)
  }

  // ★ Choosing a conversation closes the drawer. ★ The point of choosing
  //   one is to read it, and the drawer is in the way of reading it. ★ The
  //   comparison is against the **previous** one: opening the drawer on
  //   the conversation that is already open must not close it again.
  const [lastActive, setLastActive] = useState(activeId)
  if (lastActive !== activeId) {
    setLastActive(activeId)
    setDrawerOpen(false)
  }

  useEffect(() => {
    void refreshList()
    // ★ The project table is a second answer, and it is read beside the
    //   session list: the sidebar groups the sessions by it.
    void refreshProjects()
    if (activeId) void openSession(activeId)
  }, [refreshList, refreshProjects, openSession, activeId])

  // ★ The session's announcement channel follows the session on screen.
  //   It is what tells this surface that an approval was answered in
  //   another tab: without it the card stays and every answer it offers
  //   is refused.
  useEffect(() => {
    watchSession(activeId)
  }, [watchSession, activeId])

  const dot = DOT[status] ?? DOT.idle!

  // ★ The button says what it does **here**: on a wide screen it shows or
  //   hides the column, on a narrow one it opens or closes the drawer.
  //   One label for both would be a label that is wrong half the time.
  const sidebarLabel = wide
    ? collapsed
      ? "Show sidebar"
      : "Hide sidebar"
    : drawerOpen
      ? "Close conversations"
      : "Open conversations"

  return (
    // ★ The frame is for the small screens only. On a desktop the window is
    //   the frame, and a grey border around the whole app makes the content
    //   look narrower than it is — which is the opposite of what a wider
    //   column is for.
    // ★ The padding comes from the stylesheet rather than from a utility
    //   class: the safe area has to be part of it, and `env()` cannot live
    //   in a class attribute.
    <div className="app-frame bg-muted/30 flex h-dvh flex-col overflow-hidden">
      <div className="bg-background flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg md:rounded-none md:flex-row">
        {/* ★ One sidebar, never two. ★ A wide screen keeps it in the flow
            beside the chat; a narrow one keeps it in a drawer over the
            chat. ★ Rendering both would give the page two session trees —
            two of everything that reads and writes them. */}
        {wide ? (
          !collapsed && <Sidebar />
        ) : (
          <SidebarDrawer open={drawerOpen} onOpenChange={setDrawerOpen} />
        )}

        {/* ★ `min-h-0` is load-bearing, and it is the same rule the sidebar
            follows: ★ a flex item's automatic minimum size is its content, so
            a column without it grows to whatever is inside it. ★ The chat
            column holds the transcript, and a transcript is as tall as the
            conversation — ★ so the column grew to the height of the whole
            conversation, the input box was pushed thousands of pixels below
            the window, and the outer `overflow-hidden` cut it away. ★ The
            column's height is the frame's height; the transcript scrolls
            inside it. */}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center gap-2 px-4">
            <button
              type="button"
              onClick={toggleSidebar}
              className="hover:bg-accent hover:text-accent-foreground touch-target grid size-8 shrink-0 place-items-center rounded-md"
              aria-label={sidebarLabel}
              aria-expanded={wide ? !collapsed : drawerOpen}
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

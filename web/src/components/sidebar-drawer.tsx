import { Dialog as DialogPrimitive } from "radix-ui"

import { Sidebar } from "@/components/sidebar"

/**
 * The sidebar as a drawer: the same tree, over the chat, on a small screen.
 *
 * ★ It is a **Radix dialog**, and that is not decoration. ★ Radix listens
 *   for Escape on the document in the **capture phase**, which runs before
 *   React's handlers — so a component that wanted to keep the drawer open
 *   would have to say so through `onEscapeKeyDown`. ★ The same machinery
 *   brings the focus trap, the scroll lock and the `aria-modal`, and none
 *   of the three is worth writing by hand: a drawer that lets the page
 *   scroll under it, or that drops the focus into the chat behind it, is
 *   a drawer that argues with the surface it belongs to.
 *
 * ★ The drawer is **not** the collapsed sidebar. ★ Collapsing is a
 *   preference about a desktop; the drawer is about a screen. ★ They are
 *   two different states, and a small screen has no use for the first one.
 *
 * ★ The title is there for the screen reader, not for the eye: Radix names
 *   the dialog by it, and a dialog without a name is an announcement with
 *   nothing in it.
 */
export function SidebarDrawer({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange(open: boolean): void
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay
          data-testid="sidebar-drawer-overlay"
          className="fixed inset-0 z-40 bg-black/30"
        />
        <DialogPrimitive.Content
          data-testid="sidebar-drawer"
          // ★ Radix warns about a missing description, and the drawer has
          //   none to give: the tree beside it says what it is.
          aria-describedby={undefined}
          className="bg-background fixed inset-y-0 left-0 z-50 flex w-[var(--sidebar-width)] max-w-[85vw] flex-col shadow-2xl outline-none"
        >
          <DialogPrimitive.Title className="sr-only">Conversations</DialogPrimitive.Title>
          {/* ★ The tree fills the drawer and scrolls inside it: the list can
              be long, and a drawer that grows past the screen is a drawer
              that hides its own bottom. ★ `min-h-0` comes from the sidebar
              itself — the column's height is the screen's height, never the
              tree's. */}
          <Sidebar className="h-full" />
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

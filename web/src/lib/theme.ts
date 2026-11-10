// ★ The theme is one boolean, and it has to be readable from two places:
//   the toggle in the header and the code editor in the settings dialog.
//   Reading the DOM from each of them would work, but then a toggle would
//   leave the editor on the old colors — and the editor is the one place
//   where a stale theme is visible as wrong colors.
//
//   A module-level value with a listener list is enough, and it keeps the
//   decision in **one** place: whoever wants to know asks here.
//
//   ★ The very first decision is made by a script in `index.html`, before the
//   first paint — a page that paints white and then turns dark is a flash on
//   every load. That script reads the same storage key this module writes.

const KEY = "openheron.theme"

type Listener = () => void

const listeners = new Set<Listener>()

/** The theme in effect right now. */
export function isDark(): boolean {
  return (
    typeof document !== "undefined" && document.documentElement.classList.contains("dark")
  )
}

/**
 * Apply a theme: the class on the root element, the stored preference, and
 * every listener that has to redraw.
 */
export function setTheme(dark: boolean): void {
  if (typeof document !== "undefined") {
    document.documentElement.classList.toggle("dark", dark)
  }
  try {
    localStorage.setItem(KEY, dark ? "dark" : "light")
  } catch {
    // A browser that refuses storage still gets the theme it just picked.
  }
  for (const listener of listeners) listener()
}

/** Flip the theme and report the new one. */
export function toggleTheme(): boolean {
  const next = !isDark()
  setTheme(next)
  return next
}

/**
 * Subscribe to theme changes. Returns the unsubscribe function, which is
 * what `useSyncExternalStore` wants.
 */
export function subscribeTheme(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

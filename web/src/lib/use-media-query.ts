import { useCallback, useSyncExternalStore } from "react"

/**
 * useMediaQuery answers whether a media query holds right now.
 *
 * ★ It is a **subscription**, not a poll: the listener is attached to the
 *   query itself, so the answer changes when the window changes, and
 *   nothing else has to ask again.
 *
 * ★ The first answer is read **synchronously**. ★ The interface is drawn
 *   before the first effect runs, and a drawer that opened for one frame on
 *   a desktop would be a flash of the wrong layout — the first paint has to
 *   be the right one.
 *
 * ★ The server snapshot is `false`: this is a client-only interface, and a
 *   snapshot that claimed the screen were wide would be a lie nobody can
 *   check.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const list = window.matchMedia(query)
      list.addEventListener("change", onChange)
      return () => list.removeEventListener("change", onChange)
    },
    [query],
  )
  const getSnapshot = useCallback(() => window.matchMedia(query).matches, [query])
  return useSyncExternalStore(subscribe, getSnapshot, () => false)
}

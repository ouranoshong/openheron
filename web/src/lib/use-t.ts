import { useCallback, useSyncExternalStore } from "react"

import { locale, subscribeLocale, translate, type MessageKey } from "./i18n"

/**
 * The translator, bound to the language in effect.
 *
 * ★ The hook is what makes a **switch** work: the component re-renders when
 *   the language changes, and the string it holds is the new one. ★ The
 *   subscription is per component — a component that does not read any copy
 *   does not re-render when the language changes.
 */
export function useT(): (
  key: MessageKey,
  params?: Record<string, string | number>,
) => string {
  const loc = useSyncExternalStore(subscribeLocale, locale, locale)
  return useCallback(
    (key: MessageKey, params?: Record<string, string | number>) =>
      translate(loc, key, params),
    [loc],
  )
}

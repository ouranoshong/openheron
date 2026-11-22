// ★ The interface speaks the **user's** language; the runtime speaks English.
//
// ★ Two audiences, two rules: what the interface shows is translated, and
//   what the **runtime** emits — the log lines, the API bodies, the
//   sentences the server sends — stays English (AGENTS.md). ★ A Chinese
//   interface with an English error is the boundary, not a mistake.
//
// ★ The decision lives here and nowhere else: the stored preference first,
//   then the browser's language, then English. ★ A module-level value with a
//   listener list is enough — the same shape `theme.ts` uses, for the same
//   reason: whoever wants to know asks here.

import { catalog, type MessageKey } from "./locales"

export type { MessageKey } from "./locales"

/** The languages the interface speaks. */
export type Locale = "zh" | "en"

const KEY = "openheron.locale"

type Listener = () => void

const listeners = new Set<Listener>()

/** The translator a component gets: `t("composer.input.placeholder")`. */
export type Translate = (
  key: MessageKey,
  params?: Record<string, string | number>,
) => string

/**
 * The locale, from the stored preference, then the browser, then English.
 *
 * ★ A stored preference beats the browser: the user said which language they
 *   want, and the browser is only a guess about it.
 */
export function resolveLocale(stored: string | null, browser: string | undefined): Locale {
  if (stored === "zh" || stored === "en") return stored
  // ★ `navigator.language` is not always there: a test runner has a
  //   `navigator` without a language in it.
  if (browser?.toLowerCase().startsWith("zh")) return "zh"
  return "en"
}

function readStored(): string | null {
  try {
    return localStorage.getItem(KEY)
  } catch {
    // ★ A browser that refuses storage still gets a language.
    return null
  }
}

/**
 * Put the language on the document.
 *
 * ★ `lang` is how a screen reader learns to pronounce what it reads: English
 *   read with Chinese rules gets the pauses and the word breaks wrong, and
 *   the other way round is just as bad. ⇒ It has to be right before the
 *   first paint, and the module that decides it runs before React does.
 */
function applyToDocument(loc: Locale): void {
  if (typeof document === "undefined") return
  document.documentElement.lang = loc === "zh" ? "zh-CN" : "en"
}

let current: Locale =
  typeof navigator !== "undefined"
    ? resolveLocale(readStored(), navigator.language)
    : "en"

applyToDocument(current)

/** The language in effect right now. */
export function locale(): Locale {
  return current
}

/**
 * Set the language: the document, the stored preference, and everyone who
 * is listening.
 */
export function setLocale(next: Locale): void {
  current = next
  try {
    localStorage.setItem(KEY, next)
  } catch {
    // ★ A browser that refuses storage still gets the language it just picked.
  }
  applyToDocument(next)
  for (const listener of listeners) listener()
}

/** Subscribe to language changes — what `useSyncExternalStore` wants. */
export function subscribeLocale(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/**
 * The string for a key, in a language.
 *
 * ★ A language that has not written the string yet falls back to **English**:
 *   a rough edge is better than an empty space. ★ The key itself is the last
 *   resort — it is at least something to look for.
 */
export function translate(
  loc: Locale,
  key: MessageKey,
  params?: Record<string, string | number>,
): string {
  const entry = catalog[key]
  const text = entry[loc] || entry.en || key
  return params ? interpolate(text, params) : text
}

/**
 * A translator bound to one language.
 *
 * ★ This is what the **pure** functions in `lib/` take: a function in `lib/`
 *   cannot use a hook, and a hook would make it impure.
 */
export function translator(loc: Locale): Translate {
  return (key, params) => translate(loc, key, params)
}

function interpolate(
  text: string,
  params: Record<string, string | number>,
): string {
  return text.replace(/\{(\w+)\}/g, (whole, name) =>
    name in params ? String(params[name]) : whole,
  )
}

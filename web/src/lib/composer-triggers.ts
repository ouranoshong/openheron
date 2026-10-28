/**
 * Caret-level parsing for the composer's @file and /command pickers.
 *
 * All rules live here as pure functions so the browser component only wires
 * state to them. Markdown context is deliberately not inspected: a code block
 * containing "@" opens the picker too, and Escape closes it.
 */

export type TriggerKind = "mention" | "command"

export interface TriggerToken {
  kind: TriggerKind
  /** Index of the triggering "@" or "/". */
  start: number
  /** Caret position this token was parsed at. */
  end: number
  /** Text between the trigger and the caret. */
  query: string
  /** Mention typed as `@"..."`, used to keep spaces in paths. */
  quoted: boolean
}

const COMMAND_PREFIX = /^\s*\/([^\s]*)$/

export function findTrigger(text: string, caret: number): TriggerToken | null {
  const end = Math.max(0, Math.min(caret, text.length))
  const prefix = text.slice(0, end)

  // A command only triggers at the start of the message: that keeps URLs,
  // "and/or", and command arguments from opening the menu.
  const command = COMMAND_PREFIX.exec(prefix)
  if (command) {
    return {
      kind: "command",
      start: prefix.lastIndexOf("/"),
      end,
      query: command[1],
      quoted: false,
    }
  }

  for (let i = end - 1; i >= 0; i--) {
    if (text[i] !== "@") continue
    // An "@" inside a word (email addresses, handles) is not a mention.
    if (i > 0 && !/\s/.test(text[i - 1])) continue

    const after = text.slice(i + 1, end)
    if (after.startsWith('"')) {
      const inner = after.slice(1)
      // A closing quote means the mention is already complete.
      if (inner.includes('"')) return null
      return { kind: "mention", start: i, end, query: inner, quoted: true }
    }
    if (/\s/.test(after)) return null
    return { kind: "mention", start: i, end, query: after, quoted: false }
  }

  return null
}

/** Paths with spaces use the quoted form so the token stays one unit. */
export function mentionInsertion(path: string): string {
  return /\s/.test(path) ? `@"${path}"` : `@${path}`
}

export function applyTrigger(
  text: string,
  token: TriggerToken,
  replacement: string,
): { text: string; caret: number } {
  const before = text.slice(0, token.start)
  const after = text.slice(token.end)
  const next = after[0]
  // A space at end-of-text is what lets the user keep typing the next token;
  // only an already-present whitespace suppresses it.
  const suffix = next !== undefined && /\s/.test(next) ? "" : " "
  const caret = before.length + replacement.length + suffix.length
  return { text: before + replacement + suffix + after, caret }
}

export function removeTrigger(text: string, token: TriggerToken): string {
  const out = text.slice(0, token.start) + text.slice(token.end)
  return out.trim() === "" ? "" : out
}

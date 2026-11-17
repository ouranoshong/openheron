
import type { SecretGroup, SecretItem } from "@/lib/secrets-api"

// The pure layer of the secrets panel.
//
// ★ Everything here is a **function of the server's answer**: no fetch, no
//   state, no framework. That is what makes it testable by `bun test`
//   without a browser, and it is what keeps the panel itself a rendering.
//
// ★ The wording is **English** — the whole interface is, and a panel that
//   mixed two languages would make "is this a bug or did I misconfigure it?"
//   harder to answer.

export type SecretState = "stored" | "not set" | "unavailable"

export interface SecretStatus {
  state: SecretState
  /** The one-word badge. */
  label: string
  /** Which source is in force, in words. */
  effective: string
  /** The warning the panel shows when the environment overrides the key. */
  warning?: string
}

/**
 * The label of a group.
 *
 * ★ The server sends one, and it is used as it is: the server owns the
 *   partition table, and a client that keeps its own list of names is a
 *   second truth.
 */
export function groupLabel(group: SecretGroup): string {
  return group.label
}

/**
 * The source in force, in words.
 *
 * ★ The server's source strings are places — "env:LLM_DEEPSEEK_API_KEY",
 *   "keyring:llm:deepseek", "file:/etc/key", "none" — and a place is not
 *   something a reader should have to decode. This is the one place where
 *   the raw string is turned into a sentence.
 */
export function sourceLabel(source: string): string {
  if (!source || source === "none") return "nowhere — no key is in effect"
  const sep = source.indexOf(":")
  if (sep < 0) return source
  const kind = source.slice(0, sep)
  const rest = source.slice(sep + 1)
  switch (kind) {
    case "env":
      return `the environment variable ${rest}`
    case "keyring":
      return `the keyring entry ${rest}`
    case "file":
      return `the file ${rest}`
    default:
      return source
  }
}

/**
 * The status of one entry.
 *
 * ★ "Stored" and "in force" are two different questions, and they disagree
 *   exactly when the environment overrides the key. The status carries both,
 *   because a panel that showed only one of them would leave the user
 *   believing a stored key is in use when it is not.
 */
export function statusOf(item: SecretItem): SecretStatus {
  const effective = sourceLabel(item.effective)
  if (item.shadowed) {
    return {
      state: "stored",
      label: "stored",
      effective,
      warning: `The environment variable ${item.envVar} wins — the stored key is not in effect.`,
    }
  }
  if (!item.stored) {
    return { state: "not set", label: "not set", effective }
  }
  return { state: "stored", label: "stored", effective }
}

/**
 * Whether the environment variable holds something.
 *
 * ★ The answer is read off the **effective** source rather than asked for
 *   separately: the effective source *is* the environment when the
 *   environment has anything to say. Asking twice would be two answers to
 *   one question, and they would disagree the day one of them is forgotten.
 */
export function envState(item: SecretItem): "set" | "not set" {
  return item.effective.startsWith("env:") || item.effective.startsWith("file:")
    ? "set"
    : "not set"
}

/**
 * The sentence the remove-a-group confirmation shows.
 *
 * ★ The count **and** the scope: the two groups are separate namespaces, and
 *   a user who removes the provider keys must be told that the MCP servers'
 *   tokens are not affected — that is the difference between a scoped
 *   operation and a guess.
 */
export function removeGroupMessage(group: SecretGroup): string {
  const count = group.items.length
  const scope = group.id === "llm" ? "provider secret" : "MCP server secret"
  const plural = count === 1 ? scope : `${scope}s`
  const other = group.id === "llm" ? "MCP server secrets" : "provider secrets"
  return `Remove ${count} ${plural}? Only the ${group.label} group is affected; ${other} are untouched.`
}


import type { Translate } from "@/lib/i18n"
import type { SecretGroup, SecretItem } from "@/lib/secrets-api"

// The pure layer of the secrets panel.
//
// ★ Everything here is a **function of the server's answer**: no fetch, no
//   state, no framework. That is what makes it testable by `bun test`
//   without a browser, and it is what keeps the panel itself a rendering.
//
// ★ The wording is the **reader's** language: it comes from the catalog, and
//   the translator is an **argument** — a pure function cannot read the
//   language by itself. ★ The sentences the **server** sends stay English:
//     they are the runtime's own voice, and the boundary is the boundary.

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
export function sourceLabel(source: string, t: Translate): string {
  if (!source || source === "none") return t("secrets.source.none")
  const sep = source.indexOf(":")
  if (sep < 0) return source
  const kind = source.slice(0, sep)
  const rest = source.slice(sep + 1)
  switch (kind) {
    case "env":
      return t("secrets.source.env", { name: rest })
    case "keyring":
      return t("secrets.source.keyring", { name: rest })
    case "file":
      return t("secrets.source.file", { name: rest })
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
export function statusOf(item: SecretItem, t: Translate): SecretStatus {
  const effective = sourceLabel(item.effective, t)
  if (item.shadowed) {
    return {
      state: "stored",
      label: t("secrets.status.stored"),
      effective,
      warning: t("secrets.status.shadowed", { envVar: item.envVar }),
    }
  }
  if (!item.stored) {
    return { state: "not set", label: t("secrets.status.notSet"), effective }
  }
  return { state: "stored", label: t("secrets.status.stored"), effective }
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
  // ★ The answer is a **fact**, not a sentence: "set" here is a value the
  //   panel turns into words, and the words come from the catalog at the
  //   place that shows them. ⇒ A translation of the word does not belong
  //   inside the fact.
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
export function removeGroupMessage(group: SecretGroup, t: Translate): string {
  const count = group.items.length
  // ★ The two namespaces are named by **keys**, not by words written here: the
  //   sentence has to name the group that is being emptied and the one that
  //   is not, and a language that has no plural still needs both.
  const scope =
    group.id === "llm"
      ? count === 1
        ? t("secrets.scope.providerOne")
        : t("secrets.scope.providerMany")
      : count === 1
        ? t("secrets.scope.mcpOne")
        : t("secrets.scope.mcpMany")
  const other =
    group.id === "llm" ? t("secrets.scope.mcpMany") : t("secrets.scope.providerMany")
  return t("secrets.remove.message", { count, scope, group: group.label, other })
}

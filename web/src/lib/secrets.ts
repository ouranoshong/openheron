
import { ApiError } from "@/lib/api"
import type { MessageKey, Translate } from "@/lib/i18n"
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

/**
 * The groups the interface knows by name.
 *
 * ★ Keyed by the server's **id**, which is stable; the server's `label` is
 *   the English fallback and the safety net for an id that is not in here.
 */
const GROUP_KEYS: Record<string, MessageKey> = {
  llm: "secrets.group.llm",
  mcp: "secrets.group.mcp",
}

/**
 * The failures the interface can say in its own words.
 *
 * ★ The `code` is the server's, and it is stable; a code that is not in here
 *   is shown as the server's English sentence.
 */
const ERROR_KEYS: Record<string, MessageKey> = {
  secret_unavailable: "secrets.error.secretUnavailable",
  invalid_body: "secrets.error.invalidBody",
  unknown: "secrets.error.unknown",
  network: "secrets.error.network",
}

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
 * The display name of a group.
 *
 * ★ The **id** is the stable identifier, and it is what the interface
 *   translates: the server's `label` is its English voice, and a reader who
 *   is reading Chinese must not be handed an English heading.
 *
 * ★ The server's label is still the fallback — for an id the catalog does not
 *   know, and for the English interface itself. ⇒ A group the client has
 *   never heard of still gets a heading instead of a blank line.
 *
 * ★ The name is never case-folded: "MCP servers" is not "mcp servers".
 */
export function groupLabel(group: SecretGroup, t: Translate): string {
  const key = GROUP_KEYS[group.id]
  if (key) return t(key)
  const label = group.label?.trim()
  return label ? label : group.id
}

/**
 * A failure, in the parts the interface needs.
 *
 * ★ The server's `code` is stable and machine-readable; the sentence it sent
 *   is English. ⇒ The two travel together: the code says **what** happened,
 *   the message says what the server said, and the interface picks the
 *   sentence by the code.
 */
export interface SecretsFailure {
  /** The server's code, or a client-synthesized one ("network" / "unknown"). */
  code: string
  /** The server's English sentence — shown as it is when the code is unknown. */
  message: string
}

/**
 * Read a failure off a thrown value.
 *
 * ★ A failure that is not an `ApiError` never reached the server's error
 *   envelope: the response could not be parsed (`unknown`) or the server
 *   could not be reached at all (`network`). ⇒ Its English text
 *   ("Failed to fetch") is never shown; it is replaced by a sentence of the
 *   reader's own language.
 */
export function failureOf(e: unknown): SecretsFailure {
  if (e instanceof ApiError) {
    return { code: e.code, message: e.message }
  }
  if (e instanceof SyntaxError) {
    return { code: "unknown", message: e.message }
  }
  return { code: "network", message: e instanceof Error ? e.message : String(e) }
}

/**
 * The sentence the panel shows for a failure.
 *
 * ★ A known code says the failure in the reader's words; an unknown code
 *   says what the server said, untranslated and unswallowed — the server's
 *   sentence is better than a wrong guess.
 */
export function errorMessage(failure: SecretsFailure, t: Translate): string {
  const key = ERROR_KEYS[failure.code]
  if (key) return t(key)
  return failure.message
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
  // ★ The group that is being emptied is named in the reader's language, too:
  //   the sentence names one group and clears the other, and both names have
  //   to be the ones the reader sees on the panel.
  return t("secrets.remove.message", { count, scope, group: groupLabel(group, t), other })
}

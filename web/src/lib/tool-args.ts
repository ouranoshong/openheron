// ★ The arguments of a tool call arrive as **text the model streamed**, and
//
//	that text is not always what the model meant to send:
//
//	- it is often one long line (`{"path": "/a/b", "pattern": "*.go"}`), and
//	  one line of JSON is hard to read;
//	- during streaming it is **not valid JSON yet** — the delta arrives in
//	  pieces, so a half-streamed object cannot be parsed;
//	- it may not be JSON at all — a tool whose arguments are not an object
//	  (or a malformed one) has to survive the trip unchanged.
//
// ⇒ Every function here is **total**: it returns a string and never
//   throws, because a card that throws while rendering is a card that
//   takes the whole transcript down with it.

// MAX_DEPTH is how deep the pretty printer descends.
//
// ★ The depth is a **readability** limit, not a safety one: a tool
//
//	argument nested eight levels deep is not something a person reads, and
//	walking it costs a stack. Past the limit the value is emitted as the
//	compact JSON the model sent — the information is not lost, it is only
//	shown the way it arrived.
const MAX_DEPTH = 6

// INDENT is one level of indentation.
//
// ★ Two spaces, not four: the tool card is a compact block in a column
//
//	that is 64rem wide, and the arguments are usually three keys deep at
//	most. Four spaces would spend the width on whitespace.
const INDENT = "  "

export interface PrettyArgs {
  /** The text to render. */
  text: string
  /** True when the text was re-formatted, false when it is the original. */
  formatted: boolean
}
/**
 * prettyArgs re-formats a tool call's arguments.
 *
 * ★ It returns the **original text** whenever it cannot make it better:
 *
 *	- the text is empty or not JSON → unchanged;
 *	- the text is not valid JSON (streaming, or malformed) → unchanged;
 *	- the text is valid JSON but already formatted the way this function
 *	  would format it → unchanged, so the card does not flicker between two
 *	  spellings of the same thing while the text streams in.
 */
export function prettyArgs(raw: string): PrettyArgs {
  const text = raw ?? ""
  if (!text.trim()) return { text, formatted: false }

  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    // ★ A half-streamed object is the normal case here, not an exception:
    //   the card renders while the arguments are still arriving.
    return { text, formatted: false }
  }

  const pretty = renderValue(parsed, 0)
  if (pretty === text) return { text, formatted: false }
  return { text: pretty, formatted: true }
}

/**
 * renderValue renders one JSON value.
 *
 * ★ A **string** is rendered on its own, with the quotes kept — a bare
//
//	string without quotes is ambiguous (is `true` the boolean or the word?),
//	and a quoted one can be told apart from a number at a glance.
 *
 * ★ A **string that holds a newline** is the case that decides the layout:
//
//	a file's content is one string with newlines in it, and breaking it
//	across lines without saying so makes the argument unreadable. It is
//	therefore rendered as a block, with the newlines visible.
 */
function renderValue(value: unknown, depth: number): string {
  if (depth > MAX_DEPTH) return JSON.stringify(value) ?? "null"

  if (typeof value === "string") return renderString(value, depth)
  if (value === null || typeof value !== "object") return JSON.stringify(value) ?? "null"

  if (Array.isArray(value)) {
    if (value.length === 0) return "[]"
    const pad = INDENT.repeat(depth + 1)
    const items = value.map((item) => `${pad}${renderValue(item, depth + 1)}`)
    return `[\n${items.join(",\n")}\n${INDENT.repeat(depth)}]`
  }

  const entries = Object.entries(value as Record<string, unknown>)
  if (entries.length === 0) return "{}"

  const pad = INDENT.repeat(depth + 1)
  const closePad = INDENT.repeat(depth)
  const rows = entries.map(([key, item]) => {
    // ★ The key is quoted, so a key that carries a quote or a newline does
    //   not break the row it sits on.
    const safeKey = JSON.stringify(key)
    const rendered = renderValue(item, depth + 1)
    return `${pad}${safeKey}: ${rendered}`
  })
  return `{\n${rows.join(",\n")}\n${closePad}}`
}

/**
 * renderString renders a JSON string.
 *
 * ★ A string without a newline keeps its quotes, so the value is still
 *   recognisable as a string — `true` and `"true"` are told apart at a
 *   glance.
 *
 * ★ A string **with** a newline in it is a different animal: a file's
//	content, a patch, a multi-line command. Writing it as one escaped line
//   makes it unreadable (`"line one\nline two"` is one row of noise), so it
//   is written as a **block**: the real newlines, indented, with the
//   quotes kept on the first and last line.
 *
 *   ⇒ The block is a **display**, not JSON: a JSON string cannot hold a
 *     raw newline, so this form is not meant to be parsed back. That is the
 *     trade — a file's content has to be readable, and "readable" and
 *     "parseable" cannot both hold for a value with newlines in it.
 */
function renderString(value: string, depth: number): string {
  if (!value.includes("\n")) return JSON.stringify(value)

  const pad = INDENT.repeat(depth + 1)
  const closePad = INDENT.repeat(depth)
  const lines = value.split("\n").map((line) => `${pad}${line}`)
  return `"\n${lines.join("\n")}\n${closePad}"`
}

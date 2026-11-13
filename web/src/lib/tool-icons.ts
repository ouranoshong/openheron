// tool-icons names the icon a tool call is drawn with.
//
// ★ This module is **pure**: it imports no framework and no component, so
//   `bun test` can prove it right on its own. The icon it names is a
//   **string**, not a React component — the component that draws the icon
//   lives in the component layer, and this file stays testable.

// FALLBACK_ICON is the icon a tool call is drawn with when the tool is not
// in the table.
//
// ★ A wrench, not a terminal: a tool the table does not name — an MCP
//   server's tool, a third-party provider's tool — must not borrow the
//   `>_` glyph, because that glyph is the shape that started this change.
export const FALLBACK_ICON = "wrench"

// ★ The table names an icon, and the component layer draws it — so the
//   names are **literal types**: a name without a drawing is a compile
//   error, not a card that quietly shows a wrench instead.
//
// ★ Every entry is a *thing* — a folder, a file, a tree, a globe — and
//   never a caret, a chevron, or a bare `>_`. The icon sits one gap from
//   the chevron that folds the card, so a glyph that reads as an arrow
//   reads as a **second folding control**: the reader clicks the wrong one
//   and the card does not open.
//
// ★ One family, one icon: the six `config_*` tools are one service, and
//   the four `request_*` methods are one verb on the web. The title on the
//   card says which one it was.
const ICONS = {
  // the filesystem and the shell
  ls: "folder",
  glob: "scan-search",
  grep: "text-search",
  read_file: "file-text",
  write_file: "file-plus",
  edit_file: "file-pen",
  execute: "square-terminal",

  // the delegation, the plans, the manuals
  task: "bot",
  write_todos: "list-tree",
  skill: "book-open",

  // the configuration service — one family, one gear
  config_get: "settings",
  config_validate: "settings",
  config_template: "settings",
  config_notices: "settings",
  config_tier: "settings",
  config_write: "settings",

  // the tools/ files: http, web search, wikipedia, the knowledge base
  request_get: "globe",
  request_post: "globe",
  request_put: "globe",
  request_delete: "globe",
  duckduckgo_search: "search",
  wikipedia_search: "library",
  kb_search: "file-search",
  kb_index: "database",

  // a provider's own web tool
  fetch: "globe",
} as const

// ToolIconName is every icon name the table can produce.
export type ToolIconName = (typeof ICONS)[keyof typeof ICONS] | typeof FALLBACK_ICON

const LOOKUP: Record<string, ToolIconName | undefined> = ICONS

// toolIcon answers which icon a tool call is drawn with.
//
// ★ It is a **lookup**, not a rule: a tool the table does not name still
//   gets an icon — the fallback — because an unnamed tool is still a tool
//   call, and a card without an icon is a card without a shape.
export function toolIcon(name: string): ToolIconName {
  return LOOKUP[name] ?? FALLBACK_ICON
}

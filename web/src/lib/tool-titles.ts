// tool-titles names the title a tool call is drawn with.
//
// ★ This module is **pure**: it imports no framework and no component, so
//   `bun test` can prove it right on its own. The title it names is a
//   **string**, not a React node — the component that draws the card lives
//   in the component layer, and this file stays testable.
//
// ★ The card shows the tool's **name** when the table has no title for it:
//   a name nobody has a word for is still a name, and it is what the model
//   called. Hiding it behind "tool" would say less, not more.
const TITLES = {
  // the filesystem and the shell
  ls: "List directory",
  glob: "Find files",
  grep: "Search content",
  read_file: "Read file",
  write_file: "Write file",
  edit_file: "Edit file",
  execute: "Run command",

  // the delegation, the plans, the manuals
  task: "Run a sub-agent",
  write_todos: "Update todos",
  skill: "Load a skill",

  // ★ The configuration service is one service, so the six calls are six
  //   words about the same thing: read, check, scaffold, notices, tiers,
  //   and write.
  config_get: "Read configuration",
  config_validate: "Validate configuration",
  config_template: "Show a template",
  config_notices: "Show notices",
  config_tier: "Explain approval tiers",
  config_write: "Write configuration",

  // ★ The HTTP file is one verb on the web; the method is the difference.
  request_get: "Send a GET request",
  request_post: "Send a POST request",
  request_put: "Send a PUT request",
  request_delete: "Send a DELETE request",

  // the search and the reference
  duckduckgo_search: "Search the web",
  wikipedia_search: "Search Wikipedia",

  // the knowledge base: one reads, one writes the index
  kb_search: "Search the knowledge base",
  kb_index: "Index the knowledge base",

  // a provider's own web tool
  fetch: "Fetch URL",
} as const

// ToolTitleName is every title the table can produce.
export type ToolTitleName = (typeof TITLES)[keyof typeof TITLES]

const LOOKUP: Record<string, ToolTitleName | undefined> = TITLES

// toolTitle answers which title a tool call is drawn with.
//
// ★ It is a **lookup**, not a rule: a tool the table does not name still
//   gets a title — the name itself — because a card that shows a bare
//   `mcp__github__create_issue` tells the reader more than one that shows
//   the word "tool".
export function toolTitle(name: string): string {
  return LOOKUP[name] ?? name
}

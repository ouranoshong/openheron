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
import type { Translate } from "./i18n"

const TITLES = {
  // the filesystem and the shell
  ls: "tool.title.ls",
  glob: "tool.title.glob",
  grep: "tool.title.grep",
  read_file: "tool.title.readFile",
  write_file: "tool.title.writeFile",
  edit_file: "tool.title.editFile",
  execute: "tool.title.execute",

  // the delegation, the plans, the manuals
  task: "tool.title.task",
  write_todos: "tool.title.writeTodos",
  skill: "tool.title.skill",

  // ★ The configuration service is one service, so the six calls are six
  //   words about the same thing: read, check, scaffold, notices, tiers,
  //   and write.
  config_get: "tool.title.configGet",
  config_validate: "tool.title.configValidate",
  config_template: "tool.title.configTemplate",
  config_notices: "tool.title.configNotices",
  config_tier: "tool.title.configTier",
  config_write: "tool.title.configWrite",

  // ★ The HTTP file is one verb on the web; the method is the difference.
  request_get: "tool.title.requestGet",
  request_post: "tool.title.requestPost",
  request_put: "tool.title.requestPut",
  request_delete: "tool.title.requestDelete",

  // the search and the reference
  duckduckgo_search: "tool.title.duckduckgoSearch",
  wikipedia_search: "tool.title.wikipediaSearch",

  // the knowledge base: one reads, one writes the index
  kb_search: "tool.title.kbSearch",
  kb_index: "tool.title.kbIndex",

  // a provider's own web tool
  fetch: "tool.title.fetch",
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
//
// ★ The translator comes **in**: this module is pure, and a pure function
//   cannot read the language on its own.
export function toolTitle(name: string, t: Translate): string {
  const key = LOOKUP[name]
  return key ? t(key) : name
}

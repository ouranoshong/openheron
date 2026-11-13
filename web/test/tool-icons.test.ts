import { describe, expect, test } from "bun:test"

import { FALLBACK_ICON, toolIcon } from "../src/lib/tool-icons"

// BARE_FOLDING is the set of glyphs whose whole drawing is a chevron or an
// arrow stroke, with nothing around it.
//
// ★ The icon sits next to the chevron that folds the card. A glyph that is
//   *just* a `>` reads as a second folding control — and a reader who
//   clicks it clicks the wrong thing and the card does not open.
//
// ★ A framed glyph is not in this set: lucide's `square-terminal` draws a
//   `>` **inside an 18x18 box**, and the box is what tells it apart from
//   the chevron next to it.
const BARE_FOLDING = [
  "chevron-right",
  "chevron-down",
  "chevron-up",
  "chevrons-right",
  "chevrons-up-down",
  "arrow-right",
  "arrow-left",
  "move-right",
  "caret-right",
  "terminal",
]

describe("toolIcon", () => {
  test("the filesystem and the shell have an icon each", () => {
    expect(toolIcon("ls")).toBe("folder")
    expect(toolIcon("glob")).toBe("scan-search")
    expect(toolIcon("grep")).toBe("text-search")
    expect(toolIcon("read_file")).toBe("file-text")
    expect(toolIcon("write_file")).toBe("file-plus")
    expect(toolIcon("edit_file")).toBe("file-pen")
    expect(toolIcon("execute")).toBe("square-terminal")
  })

  test("the delegation, the plans and the manuals have an icon each", () => {
    expect(toolIcon("task")).toBe("bot")
    expect(toolIcon("write_todos")).toBe("list-tree")
    expect(toolIcon("skill")).toBe("book-open")
  })

  test("★ the configuration service is one family, and it is one gear", () => {
    // ★ The configuration service answers more calls in a real run than the
    //   filesystem does, and it is one service — so the icon says "this is
    //   the configuration", and the title says which call it was.
    for (const name of [
      "config_get",
      "config_validate",
      "config_template",
      "config_notices",
      "config_tier",
      "config_write",
    ]) {
      expect(toolIcon(name)).toBe("settings")
    }
  })

  test("the tools/ files have an icon each", () => {
    // ★ The four HTTP methods are one verb on the web, so they share the
    //   globe; the rest of the files are one thing each.
    for (const name of ["request_get", "request_post", "request_put", "request_delete"]) {
      expect(toolIcon(name)).toBe("globe")
    }
    expect(toolIcon("duckduckgo_search")).toBe("search")
    expect(toolIcon("wikipedia_search")).toBe("library")
    expect(toolIcon("kb_search")).toBe("file-search")
    expect(toolIcon("kb_index")).toBe("database")
  })

  test("two tools that used to share an icon do not share one now", () => {
    // ★ `glob` and `grep` were both a magnifier: the reader could not tell
    //   a file search from a content search.
    expect(toolIcon("glob")).not.toBe(toolIcon("grep"))
  })

  test("a tool the table does not name gets the fallback", () => {
    expect(toolIcon("mcp__github__create_issue")).toBe(FALLBACK_ICON)
    expect(toolIcon("some_provider__some_tool")).toBe(FALLBACK_ICON)
    expect(toolIcon("")).toBe(FALLBACK_ICON)
  })

  test("no tool is drawn with a bare folding symbol", () => {
    for (const name of [
      "ls",
      "glob",
      "grep",
      "read_file",
      "write_file",
      "edit_file",
      "execute",
      "task",
      "write_todos",
      "skill",
      "config_get",
      "request_get",
      "duckduckgo_search",
      "wikipedia_search",
      "kb_search",
      "kb_index",
    ]) {
      expect(BARE_FOLDING).not.toContain(toolIcon(name))
    }
    expect(BARE_FOLDING).not.toContain(FALLBACK_ICON)
  })

  test("the fallback is a tool, not a terminal", () => {
    // ★ The bare `>_` is what made the old card look like a fold symbol.
    expect(FALLBACK_ICON).not.toBe("terminal")
  })

  test("execute keeps its terminal, but a framed one", () => {
    // ★ `square-terminal` draws the `>` inside a box; `terminal` draws it
    //   on its own, and that is the one that reads as a chevron.
    expect(toolIcon("execute")).toBe("square-terminal")
  })
})

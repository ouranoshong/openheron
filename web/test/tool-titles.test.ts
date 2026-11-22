import { describe, expect, test } from "bun:test"

import { translator } from "../src/lib/i18n"

import { toolTitle } from "../src/lib/tool-titles"

// ★ The copy is English here: these assertions are written against it.
const en = translator("en")

// ★ The table and the icon table cover the same tools. A tool that has an
//   icon but no title is a card that shows a glyph and a raw name — the
//   reader sees `config_get` where they could have read "Read
//   configuration".
const TOOLS = [
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
  "config_validate",
  "config_template",
  "config_notices",
  "config_tier",
  "config_write",
  "request_get",
  "request_post",
  "request_put",
  "request_delete",
  "duckduckgo_search",
  "wikipedia_search",
  "kb_search",
  "kb_index",
  "fetch",
]

describe("toolTitle", () => {
  test("★ every tool the icon table knows has a title", () => {
    for (const name of TOOLS) {
      expect(toolTitle(name, en)).not.toBe(name)
    }
  })

  test("the filesystem and the shell are named by what they do", () => {
    expect(toolTitle("ls", en)).toBe("List directory")
    expect(toolTitle("glob", en)).toBe("Find files")
    expect(toolTitle("grep", en)).toBe("Search content")
    expect(toolTitle("read_file", en)).toBe("Read file")
    expect(toolTitle("write_file", en)).toBe("Write file")
    expect(toolTitle("edit_file", en)).toBe("Edit file")
    expect(toolTitle("execute", en)).toBe("Run command")
  })

  test("★ the configuration service is one service, so the six are six words", () => {
    // ★ The icon is one gear for all six; the title says which call it was.
    //   A reader who sees "Read configuration" knows the service; a reader
    //   who sees "config_get" has to know the name.
    expect(toolTitle("config_get", en)).toBe("Read configuration")
    expect(toolTitle("config_validate", en)).toBe("Validate configuration")
    expect(toolTitle("config_template", en)).toBe("Show a template")
    expect(toolTitle("config_notices", en)).toBe("Show notices")
    expect(toolTitle("config_tier", en)).toBe("Explain approval tiers")
    expect(toolTitle("config_write", en)).toBe("Write configuration")
  })

  test("the delegation, the plans and the manuals are named", () => {
    expect(toolTitle("task", en)).toBe("Run a sub-agent")
    expect(toolTitle("write_todos", en)).toBe("Update todos")
    expect(toolTitle("skill", en)).toBe("Load a skill")
  })

  test("the HTTP file is one verb, and the method is the difference", () => {
    expect(toolTitle("request_get", en)).toBe("Send a GET request")
    expect(toolTitle("request_post", en)).toBe("Send a POST request")
    expect(toolTitle("request_put", en)).toBe("Send a PUT request")
    expect(toolTitle("request_delete", en)).toBe("Send a DELETE request")
  })

  test("the search, the reference and the knowledge base are named", () => {
    expect(toolTitle("duckduckgo_search", en)).toBe("Search the web")
    expect(toolTitle("wikipedia_search", en)).toBe("Search Wikipedia")
    expect(toolTitle("kb_search", en)).toBe("Search the knowledge base")
    expect(toolTitle("kb_index", en)).toBe("Index the knowledge base")
  })

  test("★ a tool the table does not name keeps its own name", () => {
    // ★ The fallback is the name, not the word "tool": an MCP server's tool
    //   is named by the server, and hiding that says less than showing it.
    expect(toolTitle("mcp__github__create_issue", en)).toBe("mcp__github__create_issue")
    expect(toolTitle("some_provider__some_tool", en)).toBe("some_provider__some_tool")
    expect(toolTitle("", en)).toBe("")
  })

  test("★ no title is the raw tool name", () => {
    for (const name of TOOLS) {
      const title = toolTitle(name, en)
      expect(title).not.toContain("_")
    }
  })
})

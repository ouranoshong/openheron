import { describe, expect, test } from "bun:test"

import { toolTitle } from "../src/lib/tool-titles"

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
      expect(toolTitle(name)).not.toBe(name)
    }
  })

  test("the filesystem and the shell are named by what they do", () => {
    expect(toolTitle("ls")).toBe("List directory")
    expect(toolTitle("glob")).toBe("Find files")
    expect(toolTitle("grep")).toBe("Search content")
    expect(toolTitle("read_file")).toBe("Read file")
    expect(toolTitle("write_file")).toBe("Write file")
    expect(toolTitle("edit_file")).toBe("Edit file")
    expect(toolTitle("execute")).toBe("Run command")
  })

  test("★ the configuration service is one service, so the six are six words", () => {
    // ★ The icon is one gear for all six; the title says which call it was.
    //   A reader who sees "Read configuration" knows the service; a reader
    //   who sees "config_get" has to know the name.
    expect(toolTitle("config_get")).toBe("Read configuration")
    expect(toolTitle("config_validate")).toBe("Validate configuration")
    expect(toolTitle("config_template")).toBe("Show a template")
    expect(toolTitle("config_notices")).toBe("Show notices")
    expect(toolTitle("config_tier")).toBe("Explain approval tiers")
    expect(toolTitle("config_write")).toBe("Write configuration")
  })

  test("the delegation, the plans and the manuals are named", () => {
    expect(toolTitle("task")).toBe("Run a sub-agent")
    expect(toolTitle("write_todos")).toBe("Update todos")
    expect(toolTitle("skill")).toBe("Load a skill")
  })

  test("the HTTP file is one verb, and the method is the difference", () => {
    expect(toolTitle("request_get")).toBe("Send a GET request")
    expect(toolTitle("request_post")).toBe("Send a POST request")
    expect(toolTitle("request_put")).toBe("Send a PUT request")
    expect(toolTitle("request_delete")).toBe("Send a DELETE request")
  })

  test("the search, the reference and the knowledge base are named", () => {
    expect(toolTitle("duckduckgo_search")).toBe("Search the web")
    expect(toolTitle("wikipedia_search")).toBe("Search Wikipedia")
    expect(toolTitle("kb_search")).toBe("Search the knowledge base")
    expect(toolTitle("kb_index")).toBe("Index the knowledge base")
  })

  test("★ a tool the table does not name keeps its own name", () => {
    // ★ The fallback is the name, not the word "tool": an MCP server's tool
    //   is named by the server, and hiding that says less than showing it.
    expect(toolTitle("mcp__github__create_issue")).toBe("mcp__github__create_issue")
    expect(toolTitle("some_provider__some_tool")).toBe("some_provider__some_tool")
    expect(toolTitle("")).toBe("")
  })

  test("★ no title is the raw tool name", () => {
    for (const name of TOOLS) {
      const title = toolTitle(name)
      expect(title).not.toContain("_")
    }
  })
})

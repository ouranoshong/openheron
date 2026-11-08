import { describe, expect, test } from "bun:test"

import {
  blocking,
  fileTemplate,
  issueLine,
  resolveTemplate,
  suggestPath,
  type Issue,
} from "../src/lib/settings-validate"

// ★ What is left in this module is string shaping and rendering. The rules
// themselves — which sections exist, which file names a section accepts,
// whether an api_key may be a literal, which reasoning levels a model declares
// — are `spec`'s, and the server says them. A copy here was a second authority
// that drifted on every rename; those specs now live in agentkit/spec, where
// the loader can be asked directly.

describe("issueLine", () => {
  test("a line-numbered issue reads with its line", () => {
    expect(issueLine({ line: 3, message: "field bogus_key not found" })).toBe(
      "line 3: field bogus_key not found",
    )
  })

  // ★ 0 means "no line", not "line zero". A cross-file rule has no position
  //   to point at, and its message names the files.
  test("an issue without a line reads as its message alone", () => {
    expect(issueLine({ line: 0, message: "agents/main.md references model a/b" })).toBe(
      "agents/main.md references model a/b",
    )
  })
})

describe("blocking", () => {
  test("a hint does not block, an error does, and no severity means error", () => {
    const issues: Issue[] = [
      { line: 1, message: "e", severity: "error" },
      { line: 2, message: "h", severity: "hint" },
      { line: 3, message: "unspecified" },
    ]
    expect(blocking(issues).map((i) => i.message)).toEqual(["e", "unspecified"])
  })
})

describe("suggestPath", () => {
  test("adds the extension the section wants", () => {
    expect(suggestPath("researcher", "agents")).toBe("agents/researcher.md")
    expect(suggestPath("deepseek", "llm")).toBe("llm/deepseek.yaml")
    expect(suggestPath("http", "tools")).toBe("tools/http.yaml")
    expect(suggestPath("github", "mcps")).toBe("mcps/github.yaml")
  })

  test("does not double an extension the user already typed", () => {
    expect(suggestPath("main.md", "agents")).toBe("agents/main.md")
    expect(suggestPath("http.yaml", "tools")).toBe("tools/http.yaml")
  })

  test("an empty name has no path", () => {
    expect(suggestPath("", "agents")).toBe("")
    expect(suggestPath("   ", "tools")).toBe("")
  })

  // ★ String shaping, not validation: a name the section will refuse is still
  //   shaped into a path. The refusal is the server's (`spec.ValidatePath`),
  //   and the dialog shows the path so the shaping is visible.
  test("a name the server will refuse is still shaped", () => {
    expect(suggestPath("github__x", "mcps")).toBe("mcps/github__x.yaml")
    expect(suggestPath("whatever", "tools")).toBe("tools/whatever.yaml")
  })
})

describe("resolveTemplate", () => {
  const templates = {
    tools: "# one file = one tool",
    "tools/http.yaml": "methods: [get]",
  }

  test("the file's own template wins over the section's", () => {
    expect(resolveTemplate(templates, "tools/http.yaml", "tools")).toBe("methods: [get]")
  })

  test("a file with no template of its own inherits the section's", () => {
    expect(resolveTemplate(templates, "tools/other.yaml", "tools")).toBe("# one file = one tool")
  })

  test("fileTemplate says when there was nothing to inherit", () => {
    expect(fileTemplate(templates, "tools/other.yaml")).toBeUndefined()
    expect(fileTemplate(templates, "tools/http.yaml")).toBe("methods: [get]")
    expect(fileTemplate(undefined, "tools/http.yaml")).toBeUndefined()
  })
})

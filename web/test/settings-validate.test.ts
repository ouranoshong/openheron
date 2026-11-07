import { describe, expect, test } from "bun:test"

import {
  check,
  fileTemplate,
  isEditable,
  resolveTemplate,
  suggestPath,
  validateContent,
  validatePath,
  type Issue,
  type ReasoningCatalog,
  type Section,
} from "../src/lib/settings-validate"

const provider = (content: string): Issue[] => validateContent("llm/x.yaml", content)
const agent = (content: string, catalog: ReasoningCatalog = {}): Issue[] =>
  validateContent("agents/x.md", content, catalog)
const policy = (content: string): Issue[] => validateContent("policy/permissions.yaml", content)
const limits = (content: string): Issue[] => validateContent("policy/limits.yaml", content)

const lines = (issues: { line: number; message: string }[]) => issues.map((i) => i.line)

describe("validatePath", () => {
  test("accepts the editable sections", () => {
    for (const p of [
      "policy/permissions.yaml",
      "llm/deepseek.yaml",
      "agents/main.md",
      "tools/http.yaml",
      "mcps/github.yaml",
    ]) {
      expect(validatePath(p)).toEqual([])
    }
  })

  // ★ The **manifest** is the truth about which sections are editable —
  //   the server owns the partition table. The client passes the
  //   manifest's categories in, so a partition added on the server is
  //   editable the moment the manifest says so.
  test("the editable sections come from the manifest", () => {
    expect(validatePath("mcps/github.yaml", ["policy", "mcps"])).toEqual([])
    expect(validatePath("mcps/github.yaml", ["policy"])[0]!.message).toContain("not editable")
    expect(validatePath("tools/http.yaml", ["policy", "mcps"])[0]!.message).toContain("not editable")
  })

  // ★ The file's name IS the server's name, and a name with a run of
  //   underscores could not be read back.
  test("the server name has to be one that could be read back", () => {
    expect(validatePath("mcps/github.yaml")).toEqual([])
    expect(validatePath("mcps/github__x.yaml")[0]!.message).toContain("read back")
    expect(validatePath("mcps/GitHub.yaml")[0]!.message).toContain("read back")
    expect(validatePath("mcps/github.txt")[0]!.message).toContain(".yaml")
  })

  test("rejects server/ with the reason attached", () => {
    const issues = validatePath("server/config.yaml")
    expect(issues).toHaveLength(1)
    expect(issues[0]!.message).toContain("restart")
  })

  test("rejects escapes and absolute paths", () => {
    for (const p of ["", "  ", "/etc/passwd", "../x.yaml", "policy/../x.yaml", "a/b/c"]) {
      expect(validatePath(p).length).toBeGreaterThan(0)
    }
  })

  test("enforces the policy/ file whitelist", () => {
    expect(validatePath("policy/permissions.yaml")).toHaveLength(0)
    expect(validatePath("policy/limits.yaml")).toHaveLength(0)
    expect(validatePath("policy/whatever.yaml")[0]!.message).toContain("permissions.yaml")
  })

  test("enforces the tools/ file whitelist", () => {
    for (const f of ["http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml"]) {
      expect(validatePath(`tools/${f}`)).toHaveLength(0)
    }
    expect(validatePath("tools/whatever.yaml")[0]!.message).toContain("http.yaml")
  })

  test("requires the right extension per section", () => {
    expect(validatePath("agents/main.txt")).toHaveLength(1)
    expect(validatePath("llm/deepseek.json")).toHaveLength(1)
  })

  test("isEditable matches the documented set", () => {
    expect(isEditable("policy")).toBe(true)
    expect(isEditable("agents")).toBe(true)
    expect(isEditable("llm")).toBe(true)
    expect(isEditable("tools")).toBe(true)
    expect(isEditable("mcps")).toBe(true)
    expect(isEditable("server")).toBe(false)
    expect(isEditable("skills")).toBe(false)
  })
})

describe("suggestPath", () => {
  test("adds the section and the extension", () => {
    expect(suggestPath("deepseek", "llm")).toBe("llm/deepseek.yaml")
    expect(suggestPath("researcher.md", "agents")).toBe("agents/researcher.md")
  })

  test("leaves a correct name alone", () => {
    expect(suggestPath("permissions.yaml", "policy")).toBe("policy/permissions.yaml")
  })

  test("completes the policy file names", () => {
    expect(suggestPath("limits", "policy")).toBe("policy/limits.yaml")
    expect(suggestPath("permissions", "policy")).toBe("policy/permissions.yaml")
  })

  test("returns empty for a blank name", () => {
    expect(suggestPath("   ", "llm")).toBe("")
  })
})

// ── llm/<provider>.yaml ───────────────────────────────────────────────────

describe("validateProvider", () => {
  const good = "type: openai\nbase_url: https://api.x.com\nmodels:\n  - id: fast\n"

  test("accepts a complete provider", () => {
    expect(provider(good)).toEqual([])
  })

  test("requires type, base_url and models", () => {
    const issues = provider("type: openai\nbase_url: https://x\n")
    expect(issues.map((i) => i.message)).toContain("missing models — a provider without models cannot be referenced")
  })

  test("rejects a literal api_key", () => {
    const issues = provider("type: openai\nbase_url: https://x\napi_key: sk-real\nmodels: []\n")
    expect(issues.some((i) => i.message.includes("must be a reference"))).toBe(true)
  })

  test("accepts both reference forms", () => {
    for (const key of ["${MY_KEY}", '"@file:/run/secrets/k"', ""]) {
      const issues = provider(`type: openai\nbase_url: https://x\napi_key: ${key}\nmodels: []\n`)
      expect(issues.filter((i) => i.message.includes("api_key"))).toEqual([])
    }
  })

  test("flags an unquoted @file value", () => {
    const issues = provider("type: openai\nbase_url: https://x\napi_key: @file:/k\nmodels: []\n")
    expect(issues.some((i) => i.message.includes("must be quoted"))).toBe(true)
  })

  test("flags a base_url that needs quotes", () => {
    const issues = provider("type: openai\nbase_url: :8080\nmodels: []\n")
    expect(issues.some((i) => i.line === 2 && i.message.includes("quoted"))).toBe(true)
  })

  test("flags a unit-less timeout", () => {
    const issues = provider("type: openai\nbase_url: https://x\ntimeout: 300\nmodels: []\n")
    expect(issues.some((i) => i.line === 3 && i.message.includes("unit"))).toBe(true)
  })

  test("accepts a timeout with a unit", () => {
    for (const t of ["30s", "5m", "1h", "500ms"]) {
      const issues = provider(`type: openai\nbase_url: https://x\ntimeout: ${t}\nmodels: []\n`)
      expect(issues.filter((i) => i.message.includes("unit"))).toEqual([])
    }
  })

  test("ignores comments and indented keys", () => {
    const content = "# top comment\ntype: openai\nbase_url: https://x\nmodels:\n  - id: fast\n    extra:\n      timeout: 5m\n"
    expect(provider(content)).toEqual([])
  })
})

// ── agents/<id>.md ────────────────────────────────────────────────────────

describe("validateAgent", () => {
  test("accepts a complete agent", () => {
    const md = "---\nmodel: openai/gpt-4o\ndescription: root\n---\n\nYou are helpful.\n"
    expect(agent(md)).toEqual([])
  })

  test("requires a model and never offers inheritance", () => {
    const issues = agent("---\ndescription: root\n---\nbody\n")
    expect(issues.some((i) => i.message.includes("never inherited"))).toBe(true)
  })

  test("flags an empty model as well as a missing one", () => {
    const missing = agent("---\ndescription: root\n---\nbody\n")
    const empty = agent("---\nmodel:\ndescription: root\n---\nbody\n")
    expect(missing.filter((i) => i.message.includes("never inherited"))).toHaveLength(1)
    expect(empty.filter((i) => i.message.includes("never inherited"))).toHaveLength(1)
    expect(empty[0]!.line).toBe(2)
  })

  test("requires the provider prefix", () => {
    const issues = agent("---\nmodel: gpt-4o\n---\nbody\n")
    expect(issues.some((i) => i.line === 2 && i.message.includes("<provider>/<model>"))).toBe(true)
  })

  test("flags frontmatter that does not start on line 1", () => {
    const issues = agent("intro\n---\nmodel: openai/gpt-4o\n---\nbody\n")
    expect(issues.some((i) => i.message.includes("first line"))).toBe(true)
  })

  test("flags unterminated frontmatter", () => {
    const issues = agent("---\nmodel: openai/gpt-4o\nbody with no closing ---\n")
    expect(issues.some((i) => i.message.includes("never closed"))).toBe(true)
  })

  test("flags a missing frontmatter on an otherwise valid body", () => {
    const issues = agent("just prose, no frontmatter at all\n")
    expect(issues.some((i) => i.message.includes("missing frontmatter"))).toBe(true)
  })

  test("a legal reasoning level is not an issue at all", () => {
    const issues = agent("---\nmodel: openai/gpt-4o\nreasoning: high\n---\nbody\n")
    expect(issues).toEqual([])
  })

  test("a trailing comment does not become part of the level", () => {
    const withCatalog = agent("---\nmodel: openai/gpt-4o\nreasoning: high  # note\n---\nbody\n", {
      "openai/gpt-4o": ["off", "low", "high"],
    })
    expect(withCatalog).toEqual([])
  })

  test("flags a level the model does not declare", () => {
    const issues = agent("---\nmodel: openai/gpt-4o\nreasoning: ultra\n---\nbody\n", {
      "openai/gpt-4o": ["off", "low", "high"],
    })
    expect(issues).toHaveLength(1)
    expect(issues[0]!.line).toBe(3)
    expect(issues[0]!.message).toContain('"ultra"')
    expect(issues[0]!.message).toContain("openai/gpt-4o")
    expect(issues[0]!.message).toContain("off, low, high")
  })

  test("says nothing when the vocabulary is unknown", () => {
    const issues = agent("---\nmodel: brand-new/nope\nreasoning: ultra\n---\nbody\n")
    expect(issues).toEqual([])
  })
})

// ── policy/permissions.yaml ───────────────────────────────────────────────

describe("validatePolicy", () => {
  test("accepts the factory default", () => {
    expect(policy("allow: [ls, read_file]\nask: [write_file]\ndeny: []\n")).toEqual([])
  })

  test("rejects an unknown key", () => {
    const issues = policy("allow: [ls]\naskk: [write_file]\n")
    expect(issues).toHaveLength(1)
    expect(issues[0]!.line).toBe(2)
    expect(issues[0]!.message).toContain("unknown key")
  })

  test("flags a tool listed in two tiers", () => {
    const issues = policy("allow: [execute]\nask: [execute]\n")
    expect(issues).toHaveLength(1)
    expect(issues[0]!.message).toContain("already listed in allow")
  })

  test("flags a tool listed twice inside one tier", () => {
    const issues = policy("ask: [execute, execute]\n")
    expect(issues.some((i) => i.message.includes("already listed"))).toBe(true)
  })

  test("accepts an empty document", () => {
    expect(policy("")).toEqual([])
  })
})

describe("validateLimits", () => {
  test("accepts the two known sections with their keys", () => {
    expect(limits("agent:\n  max_depth: 2\n")).toHaveLength(0)
    expect(limits("retry:\n  max_retries: 3\n")).toHaveLength(0)
    expect(limits("agent:\n  max_depth: 2\nretry:\n  max_retries: 0\n")).toHaveLength(0)
  })

  test("treats 0 as a meaningful value, not an error", () => {
    expect(limits("agent:\n  max_depth: 0\n")).toHaveLength(0)
  })

  test("ignores comments and blank lines", () => {
    expect(limits("# 上限\n\nagent:\n  # 根是第 0 层\n  max_depth: 2\n")).toHaveLength(0)
  })

  test("rejects an unknown section", () => {
    const issues = limits("budget:\n  max_depth: 2\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("unknown section")
  })

  test("rejects an unknown key inside a known section", () => {
    const issues = limits("agent:\n  max_tokens: 100\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("unknown key")
  })

  test("a section name used as a key says it looks like a new section", () => {
    const issues = limits("agent:\n  max_depth: 1\n  retry:\n  max_retries: 2\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("looks like a new section")
  })

  test("rejects a non-integer or negative value", () => {
    expect(limits("agent:\n  max_depth: abc\n").length).toBeGreaterThan(0)
    expect(limits("agent:\n  max_depth: -1\n").length).toBeGreaterThan(0)
    expect(limits("retry:\n  max_retries: 1.5\n").length).toBeGreaterThan(0)
  })

  test("rejects an indented key with no section above it", () => {
    const issues = limits("  max_depth: 2\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("without a section")
  })

  test("does not apply the permissions rules to it", () => {
    expect(limits("allow: [ls]\n").length).toBeGreaterThan(0)
    expect(limits("agent:\n  max_depth: 1\n").length).toBe(0)
  })
})

describe("check", () => {
  test("a bad path short-circuits before content is examined", () => {
    const r = check("server/config.yaml", "anything at all")
    expect(r.ok).toBe(false)
    expect(r.needsServer).toBe(false)
  })

  test("a clean local pass still asks the server", () => {
    const r = check("policy/permissions.yaml", "allow: [ls]\n")
    expect(r.ok).toBe(true)
    expect(r.needsServer).toBe(true)
  })

  test("reports both path and content issues from one entry point", () => {
    const r = check("llm/x.yaml", "type: openai\n")
    expect(r.ok).toBe(false)
    expect(r.issues.map((i) => i.message).join(" ")).toContain("missing base_url")
  })

  test("is total over every editable section", () => {
    const sections: Section[] = ["policy", "agents", "llm"]
    for (const s of sections) {
      expect(() => check(`${s}/probe`, "")).not.toThrow()
    }
  })

  test("reports an issue's line as 1-based and in source order", () => {
    const r = check("llm/x.yaml", "type: openai\napi_key: sk-leak\nbase_url: https://x\nmodels: []\n")
    expect(lines(r.issues)).toEqual([...lines(r.issues)].sort((a, b) => a - b))
    expect(r.issues.some((i) => i.line === 2)).toBe(true)
  })
})

describe("validateContent dispatch", () => {
  test("each section routes to its own rules", () => {
    expect(validateContent("policy/permissions.yaml", "nope: [x]\n").length).toBeGreaterThan(0)
    expect(validateContent("agents/x.md", "no frontmatter here").length).toBeGreaterThan(0)
    expect(validateContent("llm/x.yaml", "api_key: sk-x\n").length).toBeGreaterThan(0)
    expect(validateContent("server/config.yaml", "whatever")).toEqual([])
  })

  test("the llm section is the only one guarding secrets", () => {
    const isSecretRule = (i: Issue) => i.message.includes("must be a reference")
    expect(validateContent("llm/x.yaml", "type: o\nbase_url: https://x\napi_key: sk-x\nmodels: []\n")
      .some(isSecretRule)).toBe(true)
    expect(validateContent("policy/permissions.yaml", "api_key: sk-x\n").some(isSecretRule)).toBe(false)
    expect(validateContent("policy/limits.yaml", "api_key: sk-x\n").some(isSecretRule)).toBe(false)
    expect(validateContent("agents/x.md", "---\nmodel: a/b\napi_key: sk-x\n---\n").some(isSecretRule)).toBe(false)
  })
})

// ── tools/ ────────────────────────────────────────────────────────────────

describe("validateContent for tools/", () => {
  test("flags a timeout without a unit", () => {
    const issues = validateContent("tools/http.yaml", "methods: [get]\ntimeout: 30\n")
    expect(issues.some((i) => i.message.includes("unit"))).toBe(true)
  })

  test("accepts a timeout with a unit", () => {
    expect(validateContent("tools/http.yaml", "methods: [get]\ntimeout: 30s\n")).toEqual([])
    expect(validateContent("tools/web_search.yaml", "timeout: 60s\n")).toEqual([])
  })

  test("rejects an absolute corpus path", () => {
    const issues = validateContent("tools/kb.yaml", "corpus: /usr/share/doc\n")
    expect(issues.some((i) => i.message.includes("relative"))).toBe(true)
  })

  test("requires a model when the corpus is set", () => {
    const issues = validateContent("tools/kb.yaml", "corpus: docs\n")
    expect(issues.some((i) => i.message.includes("model is required"))).toBe(true)
    expect(validateContent("tools/kb.yaml", "corpus: docs\nmodel: local/embed\n")).toEqual([])
  })

  test("an empty corpus is off, not an error", () => {
    expect(validateContent("tools/kb.yaml", "corpus: \"\"\n")).toEqual([])
  })

  // ★ The tools/ section template is nothing but comments, and a file built
  //   from it has no YAML document at all — the server's decoder answers
  //   with a bare "EOF". The editor must catch it before the round trip.
  test("flags a file that has no YAML document at all", () => {
    for (const content of ["", "\n\n", "# only a comment\n", "# tools/ one file = one tool:\n"]) {
      const issues = validateContent("tools/http.yaml", content)
      expect(issues.length).toBeGreaterThan(0)
      expect(issues[0]!.message).toContain("no YAML content")
    }
  })

  test("a separator alone is still a document", () => {
    expect(validateContent("tools/http.yaml", "---\n")).toEqual([])
  })
})

describe("resolveTemplate", () => {
  const templates = {
    tools: "# tools/ one file = one tool:\n#   http.yaml  request_get\n",
    "tools/http.yaml": "methods: [get]\n",
  }

  // ★ The file's template wins. The section template only describes the
  //   directory, and for tools/ it is nothing but comments — offering it as
  //   the skeleton is what produced a file the server could not parse.
  test("the file's template wins over the section's", () => {
    expect(resolveTemplate(templates, "tools/http.yaml", "tools")).toBe("methods: [get]\n")
  })

  test("falls back to the section template when the file has none", () => {
    expect(resolveTemplate(templates, "tools/other.yaml", "tools")).toBe(templates.tools)
  })

  test("an empty path falls back to the section template", () => {
    expect(resolveTemplate(templates, "", "tools")).toBe(templates.tools)
  })

  test("survives a manifest that carries no templates", () => {
    expect(resolveTemplate(undefined, "tools/http.yaml", "tools")).toBe("")
  })
})

describe("fileTemplate", () => {
  const templates = {
    tools: "# tools/ one file = one tool:\n",
    "tools/http.yaml": "methods: [get]\n",
  }

  test("returns the file's own template", () => {
    expect(fileTemplate(templates, "tools/http.yaml")).toBe("methods: [get]\n")
  })

  // ★ There is no fallback here on purpose: a file with no template of its
  //   own must keep whatever the editor already holds, or correcting a
  //   file name would throw the user's work away.
  test("has no fallback to the section's template", () => {
    expect(fileTemplate(templates, "tools/other.yaml")).toBeUndefined()
    expect(fileTemplate(templates, "")).toBeUndefined()
    expect(fileTemplate(undefined, "tools/http.yaml")).toBeUndefined()
  })
})
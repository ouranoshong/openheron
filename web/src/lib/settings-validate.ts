
export interface Issue {
  line: number
  message: string
  severity?: Severity
}

export type Severity = "error" | "hint"

export function blocking(issues: readonly Issue[]): Issue[] {
  return issues.filter((i) => (i.severity ?? "error") === "error")
}

export type Section = "policy" | "agents" | "llm" | "server" | "tools"

const ALLOWED_FILES: Record<Section, readonly string[]> = {
  policy: ["permissions.yaml", "limits.yaml"],
  agents: [],
  llm: [],
  server: ["config.yaml"],
  tools: ["http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml"],
}

export const EDITABLE_SECTIONS: readonly Section[] = ["policy", "agents", "llm", "tools"]

export function isEditable(section: string): boolean {
  return (EDITABLE_SECTIONS as readonly string[]).includes(section)
}

export function validatePath(path: string): Issue[] {
  const issues: Issue[] = []
  const at = (line: number, message: string) => issues.push({ line, message })

  const trimmed = path.trim()
  if (!trimmed) {
    at(1, "path is empty")
    return issues
  }
  if (trimmed.startsWith("/")) {
    at(1, "path must be relative to the config directory")
    return issues
  }
  const segments = trimmed.split("/")
  if (segments.some((s) => s === ".." || s === ".")) {
    at(1, 'path must not contain "." or ".."')
    return issues
  }
  if (segments.length !== 2) {
    at(1, "path must be <section>/<file>")
    return issues
  }

  const [section, file] = segments as [string, string]
  if (!isEditable(section)) {
    at(1, `${section}/ is not editable${section === "server" ? " — changes need a restart" : ""}`)
    return issues
  }
  if (file.startsWith(".")) {
    at(1, "file name must not start with a dot")
    return issues
  }

  const allowed = ALLOWED_FILES[section as Section] ?? []
  if (allowed.length > 0 && !allowed.includes(file)) {
    at(1, `${section}/ only accepts ${allowed.join(", ")}`)
  }
  if (section === "agents" && !file.endsWith(".md")) {
    at(1, "agents/ files must end with .md")
  }
  if (section === "llm" && !file.endsWith(".yaml")) {
    at(1, "llm/ files must end with .yaml")
  }
  return issues
}

export function suggestPath(fileName: string, section: Section): string {
  let name = fileName.trim()
  if (!name) return ""
  if (section === "agents" && !name.endsWith(".md")) name = `${name}.md`
  if (section === "llm" && !name.endsWith(".yaml")) name = `${name}.yaml`
  if (section === "policy" && !name.endsWith(".yaml")) name = `${name}.yaml`
  if (section === "tools" && !name.endsWith(".yaml")) name = `${name}.yaml`
  return `${section}/${name}`
}

export function validateContent(
  path: string,
  content: string,
  catalog: ReasoningCatalog = {},
): Issue[] {
  const section = path.split("/")[0] as Section
  switch (section) {
    case "llm":
      return validateProvider(content)
    case "agents":
      return validateAgent(content, catalog)
    case "policy":
      return path.endsWith("limits.yaml") ? validateLimits(content) : validatePolicy(content)
    case "tools":
      return validateTool(path, content)
    default:
      return []
  }
}

// ── tools/<tool>.yaml ─────────────────────────────────────────────────────

function validateTool(path: string, content: string): Issue[] {
  const issues: Issue[] = []
  const lines = content.split("\n")

  lines.forEach((line, i) => {
    const n = i + 1
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const m = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
    if (!m) return
    const [, key, rawValue] = m as unknown as [string, string, string]
    const value = rawValue.trim()

    if (key === "timeout" && value && !/^\d+(\.\d+)?(ns|us|ms|s|m|h)/.test(value) && !/^["']/.test(value)) {
      issues.push({ line: n, message: "timeout needs a unit, e.g. 30s or 5m" })
    }

    if (key === "corpus" && value && value.startsWith("/")) {
      issues.push({
        line: n,
        message: "corpus must be relative to the project root — the index and its corpus share one lifetime",
      })
    }
  })

  if (path.endsWith("kb.yaml")) {
    const value = (key: string) => {
      const raw = content.match(new RegExp(`^\\s*${key}:\\s*(.*)$`, "m"))?.[1]?.trim() ?? ""
      return raw === '""' || raw === "''" ? "" : raw
    }
    if (value("corpus") !== "" && value("model") === "") {
      issues.push({
        line: 1,
        message: "model is required when corpus is set — it must reference an embedding model under llm/",
      })
    }
  }

  return issues
}

// ── llm/<provider>.yaml ───────────────────────────────────────────────────

function validateProvider(content: string): Issue[] {
  const issues: Issue[] = []
  const lines = content.split("\n")

  let hasBaseURL = false
  let hasType = false
  let modelsDeclared = false

  lines.forEach((line, i) => {
    const n = i + 1
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const m = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
    if (!m) return
    const [, key, rawValue] = m as unknown as [string, string, string]
    const value = rawValue.trim()

    if (key === "base_url") {
      hasBaseURL = true
      if (value.startsWith(":") && !/^["']/.test(value)) {
        issues.push({ line: n, message: 'base_url starting with ":" must be quoted' })
      }
    }
    if (key === "type") hasType = true
    if (key === "models") modelsDeclared = true

    if (key === "timeout" && value && !/^\d+(\.\d+)?(ns|us|ms|s|m|h)/.test(value) && !/^["']/.test(value)) {
      issues.push({ line: n, message: "timeout needs a unit, e.g. 30s or 5m" })
    }

    if (key === "api_key" && value) {
      const unquoted = value.replace(/^["']|["']$/g, "")
      const isRef = /^\$\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(unquoted)
      const isFile = unquoted.startsWith("@file:")
      if (!isRef && !isFile) {
        issues.push({
          line: n,
          message: "api_key must be a reference: ${ENV_VAR} or @file:/path — a literal would be committed",
        })
      }
      if (isFile && unquoted.startsWith("@file:") && !/^["']/.test(value)) {
        issues.push({ line: n, message: 'an "@file:" value must be quoted: api_key: "@file:/path"' })
      }
    }
  })

  if (!hasType) issues.push({ line: 1, message: "missing type" })
  if (!hasBaseURL) issues.push({ line: 1, message: "missing base_url" })
  if (!modelsDeclared) issues.push({ line: 1, message: "missing models — a provider without models cannot be referenced" })
  return issues
}

// ── agents/<id>.md ────────────────────────────────────────────────────────

export type ReasoningCatalog = Readonly<Record<string, readonly string[]>>

function stripComment(value: string): string {
  for (let i = 0; i < value.length; i++) {
    if (value[i] !== "#") continue
    if (i === 0 || /\s/.test(value[i - 1]!)) return value.slice(0, i)
  }
  return value
}

function validateAgent(content: string, catalog: ReasoningCatalog = {}): Issue[] {
  const issues: Issue[] = []
  const lines = content.split("\n")

  const open = lines.findIndex((l) => l.trim() === "---")
  if (open < 0) {
    issues.push({ line: 1, message: "missing frontmatter; an agent file needs --- … --- with a model" })
    return issues
  }
  if (open !== 0) {
    issues.push({ line: 1, message: "the frontmatter must start on the first line" })
  }

  const end = lines.findIndex((l, i) => i > open && l.trim() === "---")
  if (end < 0) {
    issues.push({ line: open + 1, message: "frontmatter is never closed — add a closing ---" })
    return issues
  }

  let sawModel = false
  let modelRef = ""
  for (let i = open + 1; i < end; i++) {
    const raw = lines[i]!
    const trimmed = raw.trim()
    if (!trimmed || trimmed.startsWith("#")) continue
    const idx = raw.indexOf(":")
    if (idx <= 0) continue
    const key = raw.slice(0, idx).trim()
    const value = raw.slice(idx + 1)
    const n = i + 1

    if (key === "model") {
      sawModel = true
      const v = value.trim()
      modelRef = v
      if (!v) {
        issues.push({ line: n, message: "model is required — pick one from the available list; it is never inherited" })
      } else if (!v.includes("/")) {
        issues.push({ line: n, message: 'model must be "<provider>/<model>"' })
      }
    }
    if (key === "reasoning" && value.trim()) {
      const level = stripComment(value).trim()
      const known = catalog[modelRef]
      if (known && !known.includes(level)) {
        issues.push({
          line: n,
          message:
            `reasoning level "${level}" is not declared by ${modelRef}; ` +
            `it supports: ${known.join(", ")}`,
        })
      }
    }
  }

  if (!sawModel) {
    issues.push({ line: 1, message: "model is required — pick one from the available list; it is never inherited" })
  }
  return issues
}

// ── policy/permissions.yaml ───────────────────────────────────────────────

const TIERS = new Set(["allow", "ask", "deny"])

function validatePolicy(content: string): Issue[] {
  const issues: Issue[] = []
  const seen = new Map<string, string>()

  content.split("\n").forEach((raw, i) => {
    const trimmed = raw.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const m = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
    if (!m) return
    const key = m[1]!
    if (!TIERS.has(key)) {
      issues.push({ line: i + 1, message: `unknown key "${key}" — expected allow, ask or deny` })
      return
    }

    for (const name of listItems(m[2] ?? "")) {
      const prev = seen.get(name)
      if (prev !== undefined) {
        issues.push({
          line: i + 1,
          message: `"${name}" is already listed in ${prev} — deny wins, but this is probably a typo`,
        })
      } else {
        seen.set(name, key)
      }
    }
  })

  return issues
}

function listItems(raw: string): string[] {
  const t = raw.trim()
  if (!t.startsWith("[")) return []
  const inner = t.replace(/^\[/, "").replace(/\]$/, "")
  return inner
    .split(",")
    .map((s) => s.trim().replace(/^["']|["']$/g, ""))
    .filter(Boolean)
}

// ── policy/limits.yaml ────────────────────────────────────────────────────

const LIMIT_SECTIONS = new Set(["agent", "retry"])

const LIMIT_KEYS: Record<string, ReadonlySet<string>> = {
  agent: new Set(["max_depth"]),
  retry: new Set(["max_retries"]),
}

function validateLimits(content: string): Issue[] {
  const issues: Issue[] = []
  let current: string | null = null

  content.split("\n").forEach((raw, i) => {
    const trimmed = raw.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const line = i + 1
    const indented = /^\s/.test(raw)

    if (!indented) {
      const sec = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
      if (!sec) {
        issues.push({ line, message: "cannot parse this line" })
        return
      }
      const key = sec[1]!
      if (!LIMIT_SECTIONS.has(key)) {
        issues.push({
          line,
          message: `unknown section "${key}" — expected agent or retry`,
        })
        return
      }
      current = key
      if ((sec[2] ?? "").trim() !== "") {
        issues.push({
          line,
          message: `"${key}" is a section, not a value — put its keys on the next lines`,
        })
      }
      return
    }

    const kv = /^\s+([a-z_]+):\s*(.*)$/.exec(raw)
    if (!kv) {
      issues.push({ line, message: "cannot parse this line" })
      return
    }
    if (current === null) {
      issues.push({ line, message: "indented key without a section — add `agent:` or `retry:` first" })
      return
    }
    const key = kv[1]!
    const allowed = LIMIT_KEYS[current]!
    if (!allowed.has(key)) {
      if (LIMIT_SECTIONS.has(key)) {
        issues.push({
          line,
          message: `"${key}" looks like a new section — it must start at the left margin (no indentation)`,
        })
        return
      }
      issues.push({
        line,
        message: `unknown key "${key}" in ${current} — expected ${[...allowed].join(" or ")}`,
      })
      return
    }
    const rawValue = (kv[2] ?? "").trim()
    if (!/^\d+$/.test(rawValue)) {
      issues.push({
        line,
        message: `"${key}" must be a non-negative integer (0 = no limit), got "${rawValue}"`,
      })
    }
  })

  return issues
}

export interface ValidationResult {
  ok: boolean
  issues: Issue[]
  needsServer: boolean
}

export function check(
  path: string,
  content: string,
  catalog: ReasoningCatalog = {},
): ValidationResult {
  const pathIssues = validatePath(path)
  if (pathIssues.length > 0) {
    return { ok: false, issues: pathIssues, needsServer: false }
  }
  const issues = validateContent(path, content, catalog)
  return { ok: blocking(issues).length === 0, issues, needsServer: true }
}
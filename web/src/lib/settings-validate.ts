
export interface Issue {
  line: number
  message: string
  severity?: Severity
}

export type Severity = "error" | "hint"

export function blocking(issues: readonly Issue[]): Issue[] {
  return issues.filter((i) => (i.severity ?? "error") === "error")
}

// The sections the client knows how to validate.
//
// ★ The **manifest** is the truth about which sections exist and which
//   ones are editable — the server owns the partition table. A client that
//   keeps its own list is a second truth, and the day a partition is added
//   the client says it does not exist. So the callers pass the manifest's
//   editable categories in, and these are only the fallback for a caller
//   that has no manifest yet.
export type KnownSection = "agents" | "llm" | "server" | "tools" | "mcps"

export type Section = KnownSection | (string & {})

const ALLOWED_FILES: Partial<Record<KnownSection, readonly string[]>> = {
  agents: [],
  llm: [],
  server: ["config.yaml"],
  tools: ["http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml"],
  mcps: [],
}

export const EDITABLE_SECTIONS: readonly string[] = ["agents", "llm", "tools", "mcps"]

export function isEditable(section: string, editable: readonly string[] = EDITABLE_SECTIONS): boolean {
  return editable.includes(section)
}

// The server name is the file's name without the extension.
//
// ★ It may not contain a run of underscores: the exposed name
//   "mcp__a__b__x" could not be read back — is that the server "a" with
//   the tool "b__x", or the server "a__b" with the tool "x"?
const MCP_SERVER_NAME = /^[a-z0-9]+(_[a-z0-9]+)*$/

export function validatePath(path: string, editable: readonly string[] = EDITABLE_SECTIONS): Issue[] {
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
  if (!isEditable(section, editable)) {
    at(1, `${section}/ is not editable${section === "server" ? " — changes need a restart" : ""}`)
    return issues
  }
  if (file.startsWith(".")) {
    at(1, "file name must not start with a dot")
    return issues
  }

  const allowed = ALLOWED_FILES[section as KnownSection] ?? []
  if (allowed.length > 0 && !allowed.includes(file)) {
    at(1, `${section}/ only accepts ${allowed.join(", ")}`)
  }
  if (section === "agents" && !file.endsWith(".md")) {
    at(1, "agents/ files must end with .md")
  }
  if (section === "llm" && !file.endsWith(".yaml")) {
    at(1, "llm/ files must end with .yaml")
  }
  if (section === "mcps") {
    // ★ The file's name IS the server's name, so it has to be a name that
    //   could be read back.
    if (!file.endsWith(".yaml")) {
      at(1, "mcps/ files must end with .yaml")
    } else if (!MCP_SERVER_NAME.test(file.slice(0, -".yaml".length))) {
      at(1, "the server name must match ^[a-z0-9]+(_[a-z0-9]+)*$ — a run of underscores could not be read back")
    }
  }
  return issues
}

export function suggestPath(fileName: string, section: Section): string {
  let name = fileName.trim()
  if (!name) return ""
  if (section === "agents" && !name.endsWith(".md")) name = `${name}.md`
  if (section === "llm" && !name.endsWith(".yaml")) name = `${name}.yaml`
  if (section === "tools" && !name.endsWith(".yaml")) name = `${name}.yaml`
  if (section === "mcps" && !name.endsWith(".yaml")) name = `${name}.yaml`
  return `${section}/${name}`
}

// fileTemplate returns the template the server ships for one file, if any.
//
// ★ Only some files have one: tools/ ships a skeleton per tool, because the
//   file's content IS that tool's configuration. A file without a template
//   of its own inherits the section's — but the caller may want to know that
//   there was nothing to inherit, and keep what it already has.
export function fileTemplate(
  templates: Readonly<Record<string, string>> | undefined,
  path: string,
): string | undefined {
  if (!templates || !path) return undefined
  return templates[path]
}

// resolveTemplate picks the skeleton for a file: the file's own template
// when it has one, the section's otherwise.
//
// ★ The FILE's template wins over the section's. The section template
//   describes the directory ("tools/ one file = one tool"), and for tools/
//   it is nothing but comments — a file built from it has no YAML document
//   at all, and the server rejects it with a bare "EOF". The file template
//   is the one that carries the fields the file must have.
export function resolveTemplate(
  templates: Readonly<Record<string, string>> | undefined,
  path: string,
  section: Section,
): string {
  return fileTemplate(templates, path) ?? templates?.[section] ?? ""
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
    case "tools":
      return validateTool(path, content)
    case "mcps":
      return validateMCP(content)
    default:
      return []
  }
}

// ── mcps/<server>.yaml ────────────────────────────────────────────────────

function validateMCP(content: string): Issue[] {
  const issues: Issue[] = []
  if (!hasYamlDocument(content)) {
    // ★ A file under mcps/ **defines** a server, so a file with no
    //   document is not a server that is off — it is a configuration that
    //   does not load.
    issues.push({
      line: 1,
      message: "the file has no YAML content — a file under mcps/ defines a server, so it has to hold one",
    })
  }
  return issues
}

// ── tools/<tool>.yaml ─────────────────────────────────────────────────────

// hasYamlDocument reports whether the content carries a YAML document.
//
// ★ A file that is empty, blank, or nothing but comments has NO document:
//   the YAML decoder reports that as io.EOF, and the server's answer is a
//   bare "EOF" that says nothing. Catching it here turns a server round
//   trip into a line the user can act on.
function hasYamlDocument(content: string): boolean {
  return content.split("\n").some((line) => {
    const trimmed = line.trim()
    return trimmed !== "" && !trimmed.startsWith("#")
  })
}

function validateTool(path: string, content: string): Issue[] {
  const issues: Issue[] = []

  if (!hasYamlDocument(content)) {
    issues.push({
      line: 1,
      message: "the file has no YAML content — a tools file IS the tool's configuration",
    })
    return issues
  }

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

export interface ValidationResult {
  ok: boolean
  issues: Issue[]
  needsServer: boolean
}

export function check(
  path: string,
  content: string,
  catalog: ReasoningCatalog = {},
  editable: readonly string[] = EDITABLE_SECTIONS,
): ValidationResult {
  const pathIssues = validatePath(path, editable)
  if (pathIssues.length > 0) {
    return { ok: false, issues: pathIssues, needsServer: false }
  }
  const issues = validateContent(path, content, catalog)
  return { ok: blocking(issues).length === 0, issues, needsServer: true }
}
export interface Issue {
  line: number
  message: string
  /**
   * The file the issue is about, relative to the config root.
   *
   * ★ It is a **field**, not a prefix in the message: the server knows which
   *   file it is talking about, and the editor has to be able to say "that
   *   one is not this one". A message that carries the path as text cannot
   *   be told apart from one that does not — which is how a broken
   *   `agents/agent-expert.md` ended up on the screen of a user editing
   *   `agents/main.md`.
   *
   * Empty when the problem is about the tree as a whole.
   */
  path?: string
  /**
   * Severity, as the **server** decided it.
   *
   * ★ An error means the tree will not load; a hint means it loads and the
   *   author probably meant something else. The client does not grade its
   *   own findings any more — it renders the server's list, so the two
   *   cannot disagree about what blocks a save.
   *
   * Absent means "error": an answer that predates the field still blocks.
   */
  severity?: Severity
}

export type Severity = "error" | "hint"

export function blocking(issues: readonly Issue[]): Issue[] {
  return issues.filter((i) => (i.severity ?? "error") === "error")
}

/**
 * The three places an issue can belong to.
 *
 * ★ The file in front of the user is **not** the only file on the screen.
 *
 *   `own` — about the file being edited; it is the user's file, and it is
 *   what the editor is for.
 *   `tree` — about the tree as a whole (a rule that names no file); it
 *   blocks the save like any other error.
 *   `elsewhere` — about another file. It is shown under **that** file's
 *   name, because a problem in `agents/agent-expert.md` is not something
 *   the user can fix from `agents/main.md`.
 */
export interface IssueGroups {
  own: Issue[]
  tree: Issue[]
  elsewhere: Issue[]
}

export function groupIssues(issues: readonly Issue[], selected?: string): IssueGroups {
  const own: Issue[] = []
  const tree: Issue[] = []
  const elsewhere: Issue[] = []
  for (const i of issues) {
    if (!i.path) tree.push(i)
    else if (i.path === selected) own.push(i)
    else elsewhere.push(i)
  }
  return { own, tree, elsewhere }
}

/**
 * The line one issue reads as.
 *
 * ★ The path is prefixed only when it is **another** file's. The file being
 *   edited is named in the editor's own header, and repeating it on every
 *   line is noise.
 */
export function issueLabel(i: Issue, selected?: string): string {
  const base = issueLine(i)
  if (!i.path || i.path === selected) return base
  return `${i.path} · ${base}`
}

/**
 * What one issue reads as in a status line.
 *
 * ★ `line: 0` means "no line", not "line zero". A cross-file rule has no
 *   position to point at, and its message names the files instead — printing
 *   "line 0: agents/main.md references model …" would send the reader to the
 *   top of a file that is not the problem.
 */
export function issueLine(i: Issue): string {
  return i.line > 0 ? `line ${i.line}: ${i.message}` : i.message
}

export type KnownSection = "agents" | "llm" | "server" | "tools" | "mcps"

export type Section = KnownSection | (string & {})

/**
 * The editable sections, for a caller that has no manifest yet.
 *
 * ★ A fallback, not a truth. The **manifest** says which sections exist and
 *   which ones are editable — the server owns the partition table, and a
 *   client that keeps its own list drifts from it the day a partition is
 *   added.
 */
export const EDITABLE_SECTIONS: readonly string[] = ["agents", "llm", "tools", "mcps"]

export function isEditable(section: string, editable: readonly string[] = EDITABLE_SECTIONS): boolean {
  return editable.includes(section)
}

/**
 * suggestPath turns what the user typed into a path under the section.
 *
 * ★ This is string shaping, not validation. Whether a section accepts that
 *   name — the tools/ whitelist, the MCP server-name pattern, "this section
 *   needs a restart" — is `spec.ValidatePath`'s answer, and the server says
 *   it when the file is created. The dialog shows the path it would create,
 *   so the guess is visible instead of silent.
 */
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
//   at all, and the server rejects it. The file template is the one that
//   carries the fields the file must have.
export function resolveTemplate(
  templates: Readonly<Record<string, string>> | undefined,
  path: string,
  section: Section,
): string {
  return fileTemplate(templates, path) ?? templates?.[section] ?? ""
}

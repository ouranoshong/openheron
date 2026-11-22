// The project relation, as the client sees it.
//
// ★ The **server** owns this relation: the project table says which sessions
//
//	belong to which project, and the scope says how many groups the client
//	renders. The client never decides either — it reads both and groups what
//	it was given. A client that guessed would show a session under a project
//	the server does not have it in.
//
// ★ Everything here is a **pure function**: no fetch, no store, no React.
//	The grouping is the one piece of logic the sidebar needs, and a piece of
//	logic that cannot be run without a browser cannot be tested.
//
// ★ The copy these functions show — the two group headings, the sentences
//	that refuse a name or a root — comes from the catalog, so the
//	**translator is an argument**: a pure function cannot read the language
//	by itself, and one that did would be a function whose answer depends on
//	what happened before it was called.

import type { Translate } from "@/lib/i18n"
import type { DirectoryPick } from "@/lib/types"

/** One project, as the API shows it. */
export interface ProjectSummary {
  id: string
  name: string
  root: string
  order: number
  /** How many sessions the project holds. A count, not the list. */
  sessionCount: number
}

/**
 * The project this process focuses on.
 *
 * ★ Null is the multi-project mode: the process serves every project, and
 * the client renders every group.
 */
export interface ProjectScope {
  projectId: string
  name: string
  root: string
}

/**
 * The part of a session the grouping needs.
 *
 * ★ `pinned` is optional and **the server does not send it yet** — there is
 * no pin feature in the code. The group exists because the design calls for
 * it, and it stays empty until the flag arrives. It is not a lie: a missing
 * flag means "not pinned", which is the truth today.
 */
export interface GroupableSession {
  meta: {
    threadId: string
    projectId?: string
    pinned?: boolean
  }
}

/** One group of sessions, as the sidebar draws it. */
export interface SessionGroup<T> {
  /** A stable key: the project's id, or one of the two special keys. */
  key: string
  /** The heading the sidebar shows. */
  title: string
  sessions: T[]
  /**
   * ★ Whether the group is folded away — its heading is drawn, its
   *   conversations are not.
   *
   * ★ It is a **display** state, not a fact about the sessions: the server
   *   still holds them, and the group still exists.
   */
  folded?: boolean
}

/** The key of the pinned group. */
export const PINNED_GROUP = "pinned"

/** The key of the group for the sessions that belong to no project. */
export const NO_PROJECT_GROUP = "no-project"

/** The heading of the pinned group. */
// ★ The two headings are **copy**, not identifiers: they come from the
//   catalog (`projects.*`), because the interface says them in the reader's
//   language. ★ The **keys** stay what they were — a key is what the fold
//   preference is stored under, and renaming it would forget every user's
//   fold.
const PINNED_GROUP_TITLE_KEY = "projects.group.pinned" as const

/**
 * The heading of the group for the sessions that belong to no project.
 *
 * ★ It **is** the default project root — the directory those conversations
 * work in — so it is named after the place, not after the absence of a
 * project. ★ The **key** stays `no-project`: it is what the fold preference
 * is stored under, and renaming it would forget every user's fold.
 */
const DEFAULT_PROJECT_GROUP_TITLE_KEY = "projects.defaultName" as const

/**
 * groupSessions sorts the sessions into the groups the sidebar renders.
 *
 * ★ Two modes, and the **scope** says which one — the client does not
 * decide that by itself.
 *
 * - **No scope** (multi-project): the pinned group, then one group per
 *   project in the order the projects carry, then the group for the sessions
 *   that belong to no project.
 * - **A scope** (single-project): **one** group — the scope project, plus
 *   the sessions that belong to no project. ★ They join it because the
 *   process serves one directory, and those sessions have nowhere else to
 *   go; a second group would say "there is another place", and there is not.
 *
 * ★ A session is in **exactly one** group. A pinned session is in the pinned
 *   group and nowhere else — a conversation that shows up twice is a
 *   conversation the user cannot count.
 *
 * ★ **A project in the table is a group, even when it holds nothing.** ★ The
 *   sidebar is a **map** of the projects, and the actions on a project — the
 *   rename, the new folder, the removal — live on the project's own row.
 *   ★ A project that is not drawn has no row, and a project with no row
 *   cannot be managed at all: it would be a directory the user made and can
 *   neither rename nor throw away.
 *
 * ★ The **buckets** the sessions are sorted into are a different thing: the
 *   pinned group and the "no project" group hold conversations and nothing
 *   else, so an empty one is not returned — a heading with nothing under it
 *   is noise.
 */
export function groupSessions<T extends GroupableSession>(
  sessions: readonly T[],
  projects: readonly ProjectSummary[],
  scope: ProjectScope | null,
  t: Translate,
): SessionGroup<T>[] {
  if (scope) {
    const group: SessionGroup<T> = { key: scope.projectId, title: scope.name, sessions: [] }
    for (const s of sessions) {
      const id = s.meta.projectId
      // ★ The sessions of **other** projects are not in this group: the
      //   process focuses on one directory, and those conversations belong
      //   to a directory this process is not serving.
      if (!id || id === scope.projectId) {
        group.sessions.push(s)
      }
    }
    // ★ The scope project is in the table — the server could not report a
    //   scope otherwise — so it is drawn even when it holds nothing: ★ it is
    //   the one directory this process serves, and the map says so.
    if (group.sessions.length > 0 || projects.some((p) => p.id === scope.projectId)) {
      return [group]
    }
    return []
  }

  const pinned: T[] = []
  const rest: T[] = []
  for (const s of sessions) {
    if (s.meta.pinned) {
      pinned.push(s)
    } else {
      rest.push(s)
    }
  }

  const groups: SessionGroup<T>[] = []
  if (pinned.length > 0) {
    groups.push({ key: PINNED_GROUP, title: t(PINNED_GROUP_TITLE_KEY), sessions: pinned })
  }

  const ordered = sortProjects(projects)
  const known = new Set(ordered.map((p) => p.id))
  const buckets = new Map<string, T[]>()
  for (const s of rest) {
    const id = s.meta.projectId
    // ★ A project id the table does not carry lands in the "no project"
    //   group: the project was deleted, and a session must not vanish from
    //   the sidebar because its label is gone.
    const key = id && known.has(id) ? id : NO_PROJECT_GROUP
    const bucket = buckets.get(key)
    if (bucket) {
      bucket.push(s)
    } else {
      buckets.set(key, [s])
    }
  }

  for (const p of ordered) {
    // ★ Every project in the table is a group — ★ including one that holds no
    //   conversation: the row is where the project's own actions live, and a
    //   project with no row could not be renamed, moved or removed.
    groups.push({ key: p.id, title: p.name, sessions: buckets.get(p.id) ?? [] })
  }

  const orphans = buckets.get(NO_PROJECT_GROUP)
  if (orphans && orphans.length > 0) {
    groups.push({
      key: NO_PROJECT_GROUP,
      title: t(DEFAULT_PROJECT_GROUP_TITLE_KEY),
      sessions: orphans,
    })
  }
  return groups
}

/**
 * foldGroups marks the groups the user folded away.
 *
 * ★ A folded group **keeps its heading** — it still holds conversations, they
 *   are just not drawn. ★ The fold is a **display** state, not a fact about
 *   the conversations: the server still holds them, and the group still
 *   exists.
 *
 * ★ The **sessions stay in the group**: the fold is a display state, and the
 *   count of a folded group is still its count — ★ a group that says nothing
 *   about how much is in it is a group the user has to open to know.
 *
 * ★ The folded set holds **group keys**, not project ids: the "no project"
 *   group is folded like any other, and a key that names a group that is gone
 *   simply never matches.
 *
 * ★ A group with **nothing** in it is never folded: there is nothing to put
 *   away, and a heading that says "0" is a folder the user has to open to
 *   learn that it is empty.
 *
 * ★ It is a pure function so it can be tested without a browser — the folding
 *   is the only piece of the tree that is the client's own business.
 */
export function foldGroups<T>(
  groups: readonly SessionGroup<T>[],
  folded: ReadonlySet<string>,
): SessionGroup<T>[] {
  return groups.map((g) => ({ ...g, folded: g.sessions.length > 0 && folded.has(g.key) }))
}

/**
 * sortProjects puts the projects in the order the sidebar draws them.
 *
 * ★ The order is the **project's** own — it is what the user set — and the
 *   id breaks the ties, so two projects with the same order never swap
 *   places between two renders.
 */
export function sortProjects(projects: readonly ProjectSummary[]): ProjectSummary[] {
  return [...projects].sort((a, b) => a.order - b.order || a.id.localeCompare(b.id))
}

/**
 * resolveActiveProject says which project the composer works in.
 *
 * ★ The single-project mode has no choice: the process serves one directory,
 *   and a conversation created anywhere else would work in a place the user
 *   did not ask for.
 *
 * ★ The multi-project mode falls back to **no project** — the row the
 *   interface calls **Default Project**, because that *is* the **default
 *   project root**: the server accepts a conversation without a project,
 *   and it works there. ★ So the fallback is not "the first project in the
 *   table": that one is where the user put it in the list, not a directory
 *   they picked for this conversation, and starting the work there would be
 *   a guess.
 *
 * ★ A choice the user made is kept as long as it is still a project — a
 * project that was deleted must not stay selected, because the next
 * conversation would name a project the server does not have. ⇒ It falls
 * back to "no project" like everything that was not chosen.
 */
export function resolveActiveProject(
  current: string | null,
  projects: readonly ProjectSummary[],
  scope: ProjectScope | null,
): string {
  if (scope) return scope.projectId
  // ★ "Default Project" is a choice too — an empty id — and it is the
  //   fallback: null ("nothing was chosen yet") and a project the table no
  //   longer has both mean "the next conversation works in the default
  //   project root". ★ A refresh must not undo the user's own "Default
  //   Project".
  if (current === null) return ""
  if (current === "" || projects.some((p) => p.id === current)) return current
  return ""
}

/**
 * projectSwitch says what a switch to a project does.
 *
 * ★ A conversation's project is **immutable** — the server has no path that
 *   moves one (`PATCH /v2/sessions/:id` does not take a `projectId`) — so
 *   "switch to that project" can only mean "the next conversation is in that
 *   project". ★ When the conversation on screen belongs to another project,
 *   the composer **leaves** it: that conversation's work stays where it is
 *   (it is still in the sidebar), and what is on screen is what the composer
 *   is about to work in.
 *
 * ★ `sessionProject` is undefined when **no conversation is on screen** —
 *   then there is nothing to leave, and the switch is only a selection.
 */
export function projectSwitch(
  target: string,
  sessionProject: string | undefined,
): { projectId: string; leaveSession: boolean } {
  return {
    projectId: target,
    leaveSession: sessionProject !== undefined && sessionProject !== target,
  }
}

/**
 * projectNameError says why a name cannot be used.
 *
 * ★ A name is **one line**: the sidebar draws it as a heading, and a name
 *   with a newline in it breaks that heading into two.
 *
 * ★ An **empty** name is not an error here: a new project may take the
 *   directory's own name, and the server does that (`projects.Registry.Create`
 *   derives it from the root). ★ Renaming is the other case — see
 *   `projectRenameError`.
 */
export function projectNameError(name: string, t: Translate): string | undefined {
  if (/[\r\n]/.test(name.trim())) return t("projects.error.nameOneLine")
  return undefined
}

/**
 * projectRenameError says why a project cannot be renamed to this name.
 *
 * ★ Renaming is the one place where the name is **required**: the project
 *   already has one, and an empty name would take it away. ★ The server says
 *   the same thing (`projects.Registry.Update`: "the project name must not be
 *   empty") — the client says it first, so the field is not sent at all.
 */
export function projectRenameError(name: string, t: Translate): string | undefined {
  if (name.trim() === "") return t("projects.error.nameNotEmpty")
  return projectNameError(name, t)
}

/**
 * projectRootError says why a root cannot be used.
 *
 * ★ The root is the **only** required field of a new project. A project *is*
 *   a directory, and the name is optional — the server derives it from the
 *   directory when it is empty.
 *
 * ★ The directory is not checked for existence here: the server is the one
 *   that resolves the path, and a client-side copy of that rule would drift.
 */
export function projectRootError(root: string, t: Translate): string | undefined {
  if (root.trim() === "") return t("projects.error.rootNotEmpty")
  return undefined
}

/**
 * directoryPick reads the server's answer to a folder chooser.
 *
 * ★ Three outcomes, and the middle one is the interesting one: a chooser
 *   that was **closed** is not a failure — it is how a person says "not
 *   now" — so it must not be reported as one.
 *
 * ★ "Picked" without a path is a contradiction, and the server would be
 *   wrong: the client says so rather than filling in an empty root.
 */
export function directoryPick(
  answer: { picked: boolean; path?: string },
  t: Translate,
): DirectoryPick {
  if (!answer.picked) return { kind: "cancelled" }
  const path = (answer.path ?? "").trim()
  if (path === "") {
    return { kind: "failed", reason: t("projects.pick.noDirectory") }
  }
  return { kind: "picked", path }
}

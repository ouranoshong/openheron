import type { OperationView, RunsView, ThreadMeta } from "@/lib/api"
import type { AuthorizationMode } from "@/lib/authorization"
import type { AgEvent } from "@/lib/events"
import type { ProjectScope, ProjectSummary } from "@/lib/projects"
import type { SessionFact } from "@/lib/session-events"
import type { ConversationState, DirectoryPick, Interrupt } from "@/lib/types"

export interface Attachment {
  name: string
  // path is relative to the session's workspace — it is the name the server
  // resolves, and the name the client sends back with the next question.
  path: string
  size: number
  // mime is the content type the **server** derived from the file's name.
  mime: string
}

export interface SessionEntry {
  meta: ThreadMeta
  convo?: ConversationState
  loaded: boolean
  // operations is the session's operation log, as the server last reported it.
  //
  // ★ It is absent until the log has been read: the client keeps no copy of
  //   its own, and "nothing has been read yet" is not the same fact as
  //   "nothing happened".
  operations?: OperationView[]
}

export interface StoreState {
  sessions: Record<string, SessionEntry>
  order: string[]
  attachments: Record<string, Attachment[]>
  uploading: boolean
  activeId: string | null
  pendingAgentId: string | null
  // pendingAuthorizationMode is the mode the next conversation is born in.
  //
  // ★ It is the **staging** the agent choice uses, and it exists for the
  //   same reason: a conversation that does not exist yet has no mode of
  //   its own, so the choice is kept until the server can take it. ★ null
  //   means "nothing was chosen" — the conversation is born in the default
  //   mode, the way it always was.
  pendingAuthorizationMode: AuthorizationMode | null
  // pendingAuthorizationConfirmed says the person already confirmed full
  // authorization for the conversation that is about to be born.
  //
  // ★ It is the **client's** word, not the server's fact: the server has
  //   no session to record it on until the conversation exists, so it
  //   travels with the creation request and the server decides.
  pendingAuthorizationConfirmed: boolean
  loadingList: boolean
  listError?: string
  agentFilter: string | null
  setAgentFilter(agent: string | null): void

  // ── projects ────────────────────────────────────────────────────────────
  //
  // ★ The project table and the scope are the **server's** answers. The
  //   client keeps them as they are and groups the sessions by them; it
  //   never decides which project a session belongs to.
  projects: ProjectSummary[]
  // projectScope is the project this process focuses on; null is the
  // multi-project mode.
  projectScope: ProjectScope | null
  // projectsKnown says whether this server knows about projects at all.
  //
  // ★ A server that does not answer with a table is a server from **before**
  //   the projects, and the interface has to say so instead of offering an
  //   entry that would fail. ★ A transport failure is not that: the server
  //   may know perfectly well, and the next refresh says so.
  projectsKnown: boolean
  // activeProjectId is the project the composer works in.
  //
  // ★ It is **one** truth: the sidebar's picker, the button under the input
  //   box and the @-mention picker all read it, and they must never
  //   disagree — two controls that name different projects make the
  //   interface argue with itself.
  //
  // ★ In the single-project mode it is the scope — the process serves that
  //   directory and nothing else. ★ In the multi-project mode **null** and
  //   **""** both mean "no project", whose conversations work in the default
  //   project root; "" is the deliberate fallback and the value the store
  //   settles on once the table is read.
  activeProjectId: string | null
  refreshProjects(): Promise<void>
  // switchProject moves the composer into a project.
  //
  // ★ A conversation's project is immutable, so switching **leaves** a
  //   conversation that belongs to another project: the composer comes back
  //   to an empty one, and the next question creates a conversation in the
  //   project that was picked.
  switchProject(id: string): void
  // startThreadInProject opens a new conversation in a project.
  //
  // ★ It is the "+" next to a project in the sidebar: "a new conversation
  //   here". ★ A new conversation is never the one that is already open —
  //   not even when that one belongs to the same project — so the composer
  //   always comes back empty.
  startThreadInProject(id: string): void
  // ★ The three write paths on the project table. ★ Each returns the
  //   **server's** sentence when it refuses, and undefined when it did the
  //   thing — so a dialog can stay open with the reason on it, and a form
  //   does not have to keep its own copy of the rules.
  createProject(name: string, root: string): Promise<string | undefined>
  renameProject(id: string, name: string): Promise<string | undefined>
  // updateProject changes what the server already holds: the name, the
  // directory or the order.
  //
  // ★ The server takes all three in one patch (`PATCH /v2/projects/{id}`),
  //   so the client has one path for them too — a second way to rename
  //   would be a second place to keep the rules.
  updateProject(
    id: string,
    patch: { name?: string; root?: string; order?: number },
  ): Promise<string | undefined>
  deleteProject(id: string): Promise<string | undefined>
  // pickDirectory opens the operating system's folder chooser.
  //
  // ★ It answers with three outcomes, not two: a directory was chosen, the
  //   chooser was closed, or the machine could not open one. ★ A closed
  //   chooser is not a failure — it is how a person says "not now".
  pickDirectory(start: string): Promise<DirectoryPick>

  // ── actions ────────────────────────────────────────────────────────────
  refreshList(): Promise<void>
  openSession(id: string): Promise<void>
  // loadOperations reads the session's operation log and keeps it with the
  // session.
  //
  // ★ The log is the **server's** record: the client reads it and shows it,
  //   and it never writes a line of its own.
  loadOperations(id: string, opts?: { after?: number; limit?: number }): Promise<void>
  // startNewThread lets go of the conversation on screen — and of its
  // project.
  //
  // ★ The two go together: the project was that conversation's, and a new
  //   conversation has none until the user picks one. ★ The button under the
  //   input box says "Default Project" — that is the default project root —
  //   and the conversation that is started from there really has none.
  //   ★ In the single-project mode the scope stays: the process serves one
  //   directory, and there is nowhere else to go.
  startNewThread(): void
  renameSession(id: string, title: string): Promise<void>
  deleteSession(id: string): Promise<void>
  switchAgent(agentId: string): Promise<void>
  // setAuthorizationMode changes the authorization mode of the session on
  // screen.
  //
  // ★ The server's answer is the truth: the mode on the screen is the mode
  // the server holds, and a refused change leaves the old one standing.
  // ★ `confirm` is the second half of the full-authorization handshake — the
  //   server refuses `full` with 409 `confirmation_required` until the
  //   request carries the confirmation.
  // ★ **Without a session the choice is staged**: there is nothing to
  //   change yet, and the mode belongs to the conversation that is about to
  //   be born — so it goes with that conversation when it is created.
  setAuthorizationMode(mode: AuthorizationMode, confirm?: boolean): Promise<void>

  send(text: string): Promise<void>
  // reattach comes back to a run after the connection dropped. The run
  // outlives the connection, so a broken stream is not a lost turn.
  //
  // ★ The run may be named: the caller that read the ledger knows which
  //   run is live, and that is the authority — the conversation's own run
  //   id is only what this surface saw last.
  reattach(id: string, message: string, runId?: string): Promise<void>
  // recoverRound comes back to a round whose stream ended without the
  // round's last frame.
  //
  // ★ The wire cannot tell a finished round and a cut stream apart, and the
  //   difference decides whether an approval is shown or lost — so the
  //   recovery asks the ledger, and the transcript answers.
  //
  // ★ The refused run may be named: the server says a run is not live
  //   before its approval is on disk, so the recovery has to wait for the
  //   ledger's barrier before it reads the transcript.
  recoverRound(id: string, refused?: string): Promise<void>
  // reloadHistory re-reads the session's transcript — what a client does
  // when it learns a run is over.
  reloadHistory(id: string): Promise<void>
  // scanBusy decides whether the session is busy, from the run ledger. The
  // busy flag may only be cleared on the word of a scan.
  scanBusy(id: string): Promise<void>
  // applyScan records what a scan said. It is the scan's own reading, so
  // it may set or clear the busy flag.
  applyScan(id: string, runs: RunsView): void
  // attachToLiveRun follows the run that owns the session — what a second
  // tab does when it opens a session with a live turn.
  attachToLiveRun(id: string): Promise<void>
  // watchSession follows the session's announcements — the channel that
  // tells this surface that an approval was answered somewhere else.
  watchSession(id: string | null): void
  // applySessionFact folds one announcement of the session into the view.
  applySessionFact(id: string, fact: SessionFact): void
  // followRun attaches to the run the session named.
  followRun(id: string, runId: string): void
  addFiles(files: File[]): Promise<void>
  // removeFile takes a file out of the pending list, and off the disk.
  //
  // ★ The path is the name relative to the session's workspace — it is what
  //   the server resolves, and what it deletes.
  removeFile(sessionId: string, path: string): Promise<void>
  answer(interrupts: Interrupt[], approved: boolean, reason?: string): Promise<void>
  stop(): Promise<void>
  compact(): Promise<void>
  clearNotice(id: string): void
  applyEvent(id: string, ev: AgEvent): void
  loadError(id: string, message: string): void
  // markBusy records that the session has a live turn this surface does not
  // own, and rolls back the message the server refused.
  markBusy(id: string, message: string): void
}

export const MAIN_AGENT = "main"

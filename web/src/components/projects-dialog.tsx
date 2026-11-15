import { FolderIcon, Loader2Icon, PencilIcon, Trash2Icon } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { projectNameError, projectRenameError, projectRootError, sortProjects } from "@/lib/projects"
import { selectActiveProjectId, useStore } from "@/lib/store"
import { cn } from "@/lib/utils"

/**
 * ProjectsButton is the way into the project table.
 *
 * ★ It sits **below the input box**, next to the line that says which agent
 *   is answering: a project is what a conversation belongs to, and that is
 *   decided before the first word is typed. ★ It is only there when there is
 *   a choice — the single-project mode serves one directory, and a button
 *   that opens a list of projects the process cannot switch between is a
 *   decoration.
 *
 * ★ It is also only there when the **server** has a table. A server from
 *   before the projects answers 503, and offering an entry that would fail
 *   is worse than not offering it.
 *
 * ★ The button **names the project the composer works in** — it is the
 *   switcher, not just a way in: ★ the value is the same one the @-mention
 *   picker reads, so the files it lists and the project named here are the
 *   same directory. ★ A button that said "Project" no matter what the
 *   composer was in would say nothing about where the work happens.
 */
export function ProjectsButton() {
  const [open, setOpen] = useState(false)
  const scope = useStore((s) => s.projectScope)
  const known = useStore((s) => s.projectsKnown)
  const projects = useStore((s) => s.projects)
  const currentId = useStore(selectActiveProjectId)
  const current = projects.find((p) => p.id === currentId)

  if (scope || !known) return null

  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="text-muted-foreground hover:text-foreground ms-auto h-6 gap-1 px-1.5 text-[11px]"
        onClick={() => setOpen(true)}
        data-testid="projects-button"
        aria-label={current ? `Project: ${current.name}` : "Project"}
        title="The project this conversation works in. Click to switch to another one, or to make a new one."
      >
        <FolderIcon aria-hidden="true" className="size-3" />
        {current ? current.name : "No project"}
      </Button>
      <ProjectsDialog open={open} onOpenChange={setOpen} />
    </>
  )
}

/**
 * ProjectsDialog is the whole write surface of the project table.
 *
 * ★ It lists the projects, and it creates, renames and deletes them. ★ The
 *   table is the **server's**, so every change goes through the server and
 *   the list that comes back is the one that is shown — the client never
 *   keeps a second copy of the table.
 *
 * ★ Deleting a project does not delete its conversations: they fall back to
 *   "no project" and keep their history. Removing a label must not remove
 *   the work.
 *
 * ★ **Clicking a project switches to it.** ★ The composer moves into that
 *   project — and since a conversation's project is immutable, a
 *   conversation that belongs to another project is left behind (it stays in
 *   the sidebar). ★ The row that is the current one says so: the button
 *   under the input box names it, and the two must not disagree.
 */
export function ProjectsDialog({
  open,
  onOpenChange,
}: {
  open?: boolean
  onOpenChange?: (v: boolean) => void
}) {
  const projects = useStore((s) => s.projects)
  const createProject = useStore((s) => s.createProject)
  const renameProject = useStore((s) => s.renameProject)
  const deleteProject = useStore((s) => s.deleteProject)
  const pickDirectory = useStore((s) => s.pickDirectory)
  const switchProject = useStore((s) => s.switchProject)
  // ★ The project the composer works in — the one the button under the input
  //   box names and the @-mention picker lists the files of.
  const currentId = useStore(selectActiveProjectId)

  const [name, setName] = useState("")
  const [root, setRoot] = useState("")
  const [refusal, setRefusal] = useState<string | undefined>()
  const [busy, setBusy] = useState(false)
  // picking says the chooser is open on the server's machine.
  const [picking, setPicking] = useState(false)
  // editing is the project whose name is open in an input; null is none.
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState("")

  // ★ The **name** is optional and the **root** is not: a project *is* a
  //   directory, and the server derives the name from it when the name is
  //   empty. ★ So an empty name is not what stops the create — an empty root
  //   is.
  const nameError = projectNameError(name)
  const rootError = projectRootError(root)
  const canCreate = nameError === undefined && rootError === undefined

  const reset = () => {
    setName("")
    setRoot("")
    setRefusal(undefined)
    setEditing(null)
    setDraft("")
  }

  const submit = async () => {
    if (!canCreate) return
    setBusy(true)
    const reason = await createProject(name, root)
    setBusy(false)
    // ★ A refused create keeps the dialog open with everything the user
    //   typed: closing it on failure sends them back to a list that does not
    //   contain the project, with the reason behind the dialog.
    if (reason !== undefined) {
      setRefusal(reason)
      return
    }
    reset()
  }

  const commitRename = async (id: string) => {
    const error = projectRenameError(draft)
    if (error) {
      setRefusal(error)
      return
    }
    setBusy(true)
    const reason = await renameProject(id, draft)
    setBusy(false)
    if (reason !== undefined) {
      setRefusal(reason)
      return
    }
    setEditing(null)
    setDraft("")
    setRefusal(undefined)
  }

  const remove = async (id: string) => {
    setBusy(true)
    const reason = await deleteProject(id)
    setBusy(false)
    if (reason !== undefined) {
      setRefusal(reason)
      return
    }
    setRefusal(undefined)
  }

  // chooseDirectory opens the file browser and puts the answer in the field.
  //
  // ★ The chooser opens **on the machine the server runs on**, so the answer
  //   comes back over the wire — and the field keeps whatever was typed
  //   until a directory is actually chosen.
  const chooseDirectory = async () => {
    if (picking) return
    setPicking(true)
    setRefusal(undefined)
    const answer = await pickDirectory(root)
    setPicking(false)
    if (answer.kind === "picked") {
      setRoot(answer.path)
      return
    }
    if (answer.kind === "failed") {
      setRefusal(answer.reason)
    }
    // ★ "cancelled" says nothing: the chooser was closed, and that is how a
    //   person says "not now".
  }

  // choose moves the composer into a project and closes the dialog.
  //
  // ★ The dialog is a switch, not only a list: picking a project is how the
  //   user says "work here now". ★ A conversation's project is immutable, so
  //   the switch leaves a conversation that belongs to another project — it
  //   stays in the sidebar, and the composer comes back empty.
  const choose = (id: string) => {
    switchProject(id)
    onOpenChange?.(false)
  }

  if (open === undefined) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="sm:max-w-lg"
        data-testid="projects-dialog"
        onEscapeKeyDown={(e) => {
          // ★ While a name is being edited, Escape cancels the rename and
          //   **not** the dialog. ★ The dialog's own Escape handling runs on
          //   the document in the **capture** phase — before any React
          //   handler sees the key — so `preventDefault` in the input is too
          //   late, and this prop is the only place that can still say no.
          if (editing !== null) e.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>Projects</DialogTitle>
          <DialogDescription>
            A project is a directory. The conversations that belong to one work in its directory, and
            they are listed under it in the sidebar.
          </DialogDescription>
        </DialogHeader>

        {sortProjects(projects).length === 0 ? (
          <p className="text-muted-foreground text-xs" data-testid="projects-empty">
            No projects yet. The conversations you start now work in the default project root.
          </p>
        ) : (
          <ul className="flex flex-col gap-1" data-testid="projects-list">
            {sortProjects(projects).map((p) => {
              const isCurrent = p.id === currentId
              return (
                <li
                  key={p.id}
                  data-testid="project-row"
                  data-project-id={p.id}
                  data-active={isCurrent ? "true" : "false"}
                  className={cn(
                    "flex items-center gap-2 rounded-md border px-2 py-1.5",
                    // ★ The current project is marked: the button under the
                    //   input box names it, and the list must say the same
                    //   thing.
                    isCurrent && "border-primary/60 bg-primary/5",
                  )}
                  onClick={() => {
                    // ★ While a name is being edited the row is **not** a
                    //   switch — the click belongs to the input, and a
                    //   rename that also moved the composer would be two
                    //   things at once.
                    if (editing !== null) return
                    choose(p.id)
                  }}
                >
                  <div className="min-w-0 flex-1">
                    {editing === p.id ? (
                      <Input
                        autoFocus
                        value={draft}
                        onChange={(e) => setDraft(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            e.preventDefault()
                            void commitRename(p.id)
                          }
                          if (e.key === "Escape") {
                            // ★ Escape cancels the rename and leaves the dialog
                            //   open — the dialog's own Escape is held back by
                            //   `onEscapeKeyDown` above while a name is being
                            //   edited.
                            e.preventDefault()
                            setEditing(null)
                            setDraft("")
                          }
                        }}
                        aria-label={`Name of ${p.name}`}
                        data-testid="project-rename-input"
                        className="h-7 text-sm"
                      />
                    ) : (
                      // ★ The name is the button: the row is a switch, and
                      //   this is the part of it that a keyboard can reach.
                      <button
                        type="button"
                        onClick={(e) => {
                          // ★ The row handles the click too — this one must
                          //   not do it twice.
                          e.stopPropagation()
                          choose(p.id)
                        }}
                        aria-current={isCurrent ? "true" : undefined}
                        data-testid="project-name"
                        className="block max-w-full truncate text-left text-sm font-medium hover:underline"
                      >
                        {p.name}
                      </button>
                    )}
                    <div className="text-muted-foreground truncate font-mono text-[11px]" title={p.root}>
                      {p.root}
                    </div>
                  </div>
                  {isCurrent && (
                    <span
                      data-testid="project-current"
                      className="text-primary shrink-0 text-[11px]"
                      title="The project this conversation works in"
                    >
                      Current
                    </span>
                  )}
                  <span className="text-muted-foreground shrink-0 text-[11px]">
                    {p.sessionCount} {p.sessionCount === 1 ? "session" : "sessions"}
                  </span>
                  <button
                    type="button"
                    aria-label={`Rename ${p.name}`}
                    onClick={(e) => {
                      // ★ The row is a switch — the rename button is not.
                      e.stopPropagation()
                      setEditing(p.id)
                      setDraft(p.name)
                      setRefusal(undefined)
                    }}
                    className="hover:bg-muted grid size-6 shrink-0 place-items-center rounded-md"
                  >
                    <PencilIcon className="size-3.5" />
                  </button>
                  <button
                    type="button"
                    aria-label={`Delete ${p.name}`}
                    title="The conversations are kept — they fall back to no project."
                    onClick={(e) => {
                      // ★ The row is a switch — the delete button is not.
                      e.stopPropagation()
                      void remove(p.id)
                    }}
                    className="hover:bg-muted text-destructive grid size-6 shrink-0 place-items-center rounded-md"
                  >
                    <Trash2Icon className="size-3.5" />
                  </button>
                </li>
              )
            })}
          </ul>
        )}

        <div className="flex flex-col gap-2 border-t pt-3">
          <span className="text-xs font-medium">New project</span>
          <Input
            value={name}
            onChange={(e) => {
              setName(e.target.value)
              setRefusal(undefined)
            }}
            placeholder="Name (optional — the directory's own name is used)"
            aria-label="Project name"
            data-testid="project-name-input"
            className="h-8 text-sm"
          />
          <div className="flex items-center gap-2">
            <Input
              value={root}
              onChange={(e) => {
                setRoot(e.target.value)
                setRefusal(undefined)
              }}
              placeholder="Root directory"
              aria-label="Project root"
              data-testid="project-root-input"
              className="h-8 font-mono text-xs"
            />
            {/* ★ The file browser. ★ It is the **server's** dialog: a browser
                will not tell a page where a chosen directory lives, so the
                machine the server runs on is the one that is asked. */}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void chooseDirectory()}
              disabled={picking}
              data-testid="project-pick-directory"
              aria-label="Choose a directory"
              title="Open the file browser and choose a directory"
              className="h-8 shrink-0 gap-1.5 px-2 text-xs"
            >
              {picking ? (
                <Loader2Icon aria-hidden="true" className="size-3.5 animate-spin" />
              ) : (
                <FolderIcon aria-hidden="true" className="size-3.5" />
              )}
              Browse
            </Button>
          </div>
          {refusal !== undefined && (
            <span className="text-destructive text-[11px]" data-testid="project-refusal">
              {refusal}
            </span>
          )}
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange?.(false)}>
            Close
          </Button>
          <Button
            type="button"
            disabled={!canCreate || busy}
            onClick={() => void submit()}
            data-testid="project-create"
          >
            Create project
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

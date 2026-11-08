
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { SectionPicker } from "@/components/settings/file-list"
import { useSettings } from "@/lib/settings-store"
import { fileTemplate, resolveTemplate, suggestPath, type Section } from "@/lib/settings-validate"

export function NewFileButton() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button type="button" variant="ghost" size="sm" className="w-full justify-start text-xs" onClick={() => setOpen(true)} data-testid="settings-new-file">
        + New file
      </Button>
      <NewFileDialog open={open} onOpenChange={setOpen} />
    </>
  )
}

export function NewFileDialog({ open, onOpenChange }: { open?: boolean; onOpenChange?: (v: boolean) => void }) {
  const templates = useSettings((s) => s.manifest?.templates)
  const modelRefs = useSettings((s) => s.manifest?.modelRefs)
  const newFile = useSettings((s) => s.newFile)

  const [section, setSection] = useState<Section>("agents")
  const [name, setName] = useState("")
  const [content, setContent] = useState("")
  // edited records that the user has typed in the content box. Until then the
  // content is still the template, and a new file name may replace it with
  // that file's own template.
  const [edited, setEdited] = useState(false)
  // ★ The refusal is the **server's sentence**, kept here rather than derived
  //   from a copy of its rules. The name is not checked in this file at all:
  //   `spec.ValidatePath` is the only thing that knows the tools/ whitelist
  //   and the MCP server-name pattern, and a second copy here drifts.
  const [refusal, setRefusal] = useState<string | undefined>()

  const path = suggestPath(name, section)
  const named = name.trim() !== ""

  const reset = () => {
    setSection("agents")
    setName("")
    setContent("")
    setEdited(false)
    setRefusal(undefined)
  }

  const seed = (s: Section) => {
    setSection(s)
    setContent(templates?.[s] ?? "")
    setEdited(false)
    setRefusal(undefined)
  }

  const submit = async () => {
    const reason = await newFile(path, content)
    // ★ A refused create keeps the dialog open with everything the user
    //   typed. Closing it on failure sends them back to a list that does not
    //   contain the file, with the reason in a status line behind the dialog.
    if (reason !== undefined) {
      setRefusal(reason)
      return
    }
    reset()
    onOpenChange?.(false)
  }

  if (open === undefined) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        // ★ Wider than a form needs to be: the skeleton of an agent file is
        //   a dozen lines of YAML, and at 32rem it was edited in a column
        //   that folded every key onto two lines. ★ The height cap keeps it
        //   inside the window — the dialog scrolls, the page does not.
        className="sm:max-w-3xl max-h-[85vh] overflow-y-auto"
        data-testid="new-file-dialog"
      >
        <DialogHeader>
          <DialogTitle>New file</DialogTitle>
          <DialogDescription>
            The skeleton is intentionally incomplete — the server rejects it until you fill in what it requires.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">Section</span>
            <SectionPicker
              value={section}
              onChange={(v) => {
                seed(v as Section)
                setName("")
              }}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label htmlFor="new-file-name" className="text-xs font-medium">
              File name
            </label>
            <input
              id="new-file-name"
              value={name}
              onChange={(e) => {
                const nextName = e.target.value
                setName(nextName)
                // ★ The skeleton follows the FILE, not the section: the
                //   section template for tools/ is a comment block, and a
                //   comment-only file has no YAML document at all — the
                //   server rejects it with a bare "EOF". Once the name is
                //   known, the file's own template takes over — unless the
                //   user has already typed something.
                //
                //   A file with no template of its own keeps what it has:
                //   the section template is the right start for it, and
                //   swapping it back in would throw the user's work away.
                if (edited) return
                const nextTemplate = fileTemplate(templates, suggestPath(nextName, section))
                if (nextTemplate !== undefined) setContent(nextTemplate)
              }}
              placeholder={
                section === "agents"
                  ? "researcher"
                  : section === "llm"
                    ? "deepseek"
                    : section === "mcps"
                      ? "github"
                      : section === "tools"
                        ? "http"
                        : "config"
              }
              className="bg-background rounded-md border px-2 py-1.5 font-mono text-xs"
              data-testid="new-file-name"
            />
            {path && (
              <span className="text-muted-foreground font-mono text-[11px]">{path}</span>
            )}
            {refusal !== undefined && (
              <span className="text-destructive text-[11px]" data-testid="new-file-refusal">
                {refusal}
              </span>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium">Content</span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="text-[11px]"
                onClick={() => {
                  setContent(resolveTemplate(templates, path, section))
                  setEdited(false)
                }}
              >
                Reset to template
              </Button>
            </div>
            <textarea
              value={content}
              onChange={(e) => {
                setContent(e.target.value)
                setEdited(true)
              }}
              rows={12}
              spellCheck={false}
              className="bg-background rounded-md border p-2 font-mono text-xs"
              data-testid="new-file-content"
            />
            {section === "agents" && (modelRefs?.length ?? 0) > 0 && (
              <span className="text-muted-foreground text-[11px]">available models: {modelRefs!.join(", ")}</span>
            )}
          </div>

        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange?.(false)}>
            Cancel
          </Button>
          <Button type="button" disabled={!named} onClick={() => void submit()} data-testid="new-file-create">
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
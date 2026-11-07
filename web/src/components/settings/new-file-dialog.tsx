
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { SectionPicker } from "@/components/settings/file-list"
import { selectEditableSections, useSettings } from "@/lib/settings-store"
import { fileTemplate, resolveTemplate, suggestPath, validatePath, type Section } from "@/lib/settings-validate"
import { useShallow } from "zustand/react/shallow"

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
  // ★ The manifest says which sections may be edited — the server owns the
  //   partition table, and a client that keeps its own list drifts from it.
  const editable = useSettings(useShallow(selectEditableSections))
  const modelRefs = useSettings((s) => s.manifest?.modelRefs)
  const newFile = useSettings((s) => s.newFile)

  const [section, setSection] = useState<Section>("policy")
  const [name, setName] = useState("")
  const [content, setContent] = useState("")
  // edited records that the user has typed in the content box. Until then the
  // content is still the template, and a new file name may replace it with
  // that file's own template.
  const [edited, setEdited] = useState(false)

  const path = suggestPath(name, section)
  const pathIssues = path ? validatePath(path, editable) : [{ line: 1, message: "enter a file name" }]

  const reset = () => {
    setSection("policy")
    setName("")
    setContent("")
    setEdited(false)
  }

  const seed = (s: Section) => {
    setSection(s)
    setContent(templates?.[s] ?? "")
    setEdited(false)
  }

  const submit = async () => {
    await newFile(path, content)
    reset()
    onOpenChange?.(false)
  }

  if (open === undefined) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg" data-testid="new-file-dialog">
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
                    : section === "policy"
                      ? "permissions / limits"
                      : "config"
              }
              className="bg-background rounded-md border px-2 py-1.5 font-mono text-xs"
              data-testid="new-file-name"
            />
            {path && pathIssues.length === 0 && (
              <span className="text-muted-foreground font-mono text-[11px]">{path}</span>
            )}
            {pathIssues.length > 0 && (
              <span className="text-destructive text-[11px]">{pathIssues[0]!.message}</span>
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
              rows={8}
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
          <Button type="button" disabled={pathIssues.length > 0 || !path} onClick={() => void submit()} data-testid="new-file-create">
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
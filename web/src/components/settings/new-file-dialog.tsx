
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { SectionPicker } from "@/components/settings/file-list"
import { useSettings } from "@/lib/settings-store"
import { suggestPath, validatePath, type Section } from "@/lib/settings-validate"

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

  const [section, setSection] = useState<Section>("policy")
  const [name, setName] = useState("")
  const [content, setContent] = useState("")

  const path = suggestPath(name, section)
  const template = (path ? templates?.[path] : undefined) ?? templates?.[section] ?? ""
  const pathIssues = path ? validatePath(path) : [{ line: 1, message: "enter a file name" }]

  const reset = () => {
    setSection("policy")
    setName("")
    setContent("")
  }

  const seed = (s: Section) => {
    setSection(s)
    setContent(templates?.[s] ?? "")
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
                setName(e.target.value)
                if (!content && template) setContent(template)
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
              <Button type="button" variant="ghost" size="sm" className="text-[11px]" onClick={() => setContent(templates?.[section] ?? "")}>
                Reset to template
              </Button>
            </div>
            <textarea
              value={content}
              onChange={(e) => setContent(e.target.value)}
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
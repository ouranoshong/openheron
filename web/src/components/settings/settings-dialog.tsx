
import { XIcon } from "lucide-react"
import { useSyncExternalStore } from "react"
import { useShallow } from "zustand/react/shallow"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfigEditor } from "@/components/settings/config-editor"
import { SettingsFileList } from "@/components/settings/file-list"
import { SettingsFooter } from "@/components/settings/footer"
import { ConflictDialog } from "@/components/settings/conflict-dialog"
import { useSettings } from "@/lib/settings-store"
import { selectCategories, selectIsEditable } from "@/lib/settings-store"
import { isDark, subscribeTheme } from "@/lib/theme"

export function SettingsDialog() {
  const open = useSettings((s) => s.open)
  const close = useSettings((s) => s.closeSettings)
  const selected = useSettings((s) => s.selected)
  const draft = useSettings((s) => s.draft)
  const loading = useSettings((s) => s.loading)
  const loadError = useSettings((s) => s.loadError)
  const edit = useSettings((s) => s.edit)

  const categories = useSettings(useShallow(selectCategories))
  const isEditable = useSettings(selectIsEditable)

  // ★ The editor's colors follow the theme, so this has to be a **live**
  //   read: a toggle that happened while the dialog was open would
  //   otherwise leave the editor on the old palette.
  const dark = useSyncExternalStore(subscribeTheme, isDark, isDark)

  return (
    <>
      <Dialog open={open} onOpenChange={(v) => (!v ? close() : undefined)}>
        <DialogContent
          className="sm:max-w-6xl h-[85vh] flex flex-col gap-3 p-4"
          data-testid="settings-dialog"
          showCloseButton={false}
        >
          <DialogHeader className="flex-row items-center justify-between gap-4 space-y-0">
            <div className="min-w-0">
              <DialogTitle>Settings</DialogTitle>
              <DialogDescription className="truncate">{selected ?? "no file selected"}</DialogDescription>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <DialogClose asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Close settings">
                  <XIcon className="size-4" />
                </Button>
              </DialogClose>
            </div>
          </DialogHeader>

          <div className="flex min-h-0 flex-1 gap-4">
            <SettingsFileList />

            <div className="flex min-w-0 flex-1 flex-col gap-2">
              {loadError ? (
                <p className="text-destructive text-sm" role="alert">
                  {loadError}
                </p>
              ) : selected ? (
                <>
                  {categories.find((c) => !c.editable && selected.startsWith(c.id)) && (
                    <p className="text-muted-foreground bg-muted rounded-md px-3 py-2 text-xs" role="status">
                      This section is not hot-reloaded; changes apply after a restart.
                    </p>
                  )}
                  <ConfigEditor
                    path={selected}
                    value={draft}
                    onChange={edit}
                    readOnly={!isEditable}
                    dark={dark}
                  />
                </>
              ) : (
                <div className="text-muted-foreground grid flex-1 place-items-center text-sm">
                  {loading ? "Loading…" : "Select a file"}
                </div>
              )}
            </div>
          </div>

          <SettingsFooter />
        </DialogContent>
      </Dialog>
      <ConflictDialog />
    </>
  )
}

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
import { SecretsPanel } from "@/components/settings/secrets-panel"
import { ConflictDialog } from "@/components/settings/conflict-dialog"
import { useSettings } from "@/lib/settings-store"
import { selectCategories, selectIsEditable, type SettingsTab } from "@/lib/settings-store"
import { isDark, subscribeTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"

export function SettingsDialog() {
  const open = useSettings((s) => s.open)
  const close = useSettings((s) => s.closeSettings)
  const selected = useSettings((s) => s.selected)
  const draft = useSettings((s) => s.draft)
  const loading = useSettings((s) => s.loading)
  const loadError = useSettings((s) => s.loadError)
  const edit = useSettings((s) => s.edit)
  const tab = useSettings((s) => s.tab)
  const setTab = useSettings((s) => s.setTab)

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
              <DialogDescription className="truncate">
                {tab === "secrets" ? "the keys, and where each one comes from" : (selected ?? "no file selected")}
              </DialogDescription>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <TabBar tab={tab} onChange={setTab} />
              <DialogClose asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Close settings">
                  <XIcon className="size-4" />
                </Button>
              </DialogClose>
            </div>
          </DialogHeader>

          {tab === "secrets" ? (
            <SecretsPanel />
          ) : (
            <>
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
            </>
          )}
        </DialogContent>
      </Dialog>
      <ConflictDialog />
    </>
  )
}

/**
 * The two surfaces of the dialog.
 *
 * ★ The secrets are **configuration**, so they belong in the place the
 *   configuration is edited — not in the file tree (a secret has no path,
 *   and that tree is built from the paths the server sends) and not in a
 *   second entry in the sidebar (the configuration is in one place).
 */
function TabBar({ tab, onChange }: { tab: SettingsTab; onChange(t: SettingsTab): void }) {
  const tabs: { id: SettingsTab; label: string }[] = [
    { id: "files", label: "Files" },
    { id: "secrets", label: "Secrets" },
  ]
  return (
    <div role="tablist" aria-label="Settings sections" className="flex items-center gap-1">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={tab === t.id}
          data-testid={`settings-tab-${t.id}`}
          onClick={() => onChange(t.id)}
          className={cn(
            "rounded-md border px-2.5 py-1 text-xs",
            tab === t.id && "bg-accent",
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

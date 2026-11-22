
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { useSettings } from "@/lib/settings-store"
import { useT } from "@/lib/use-t"

export function ConflictDialog() {
  const conflict = useSettings((s) => s.conflict)
  const reload = useSettings((s) => s.reloadFromDisk)
  const t = useT()

  return (
    <Dialog open={conflict !== undefined} onOpenChange={() => (conflict ? reload() : undefined)}>
      <DialogContent className="sm:max-w-md" data-testid="conflict-dialog">
        <DialogHeader>
          <DialogTitle>{t("settings.conflict.title")}</DialogTitle>
          <DialogDescription>{t("settings.conflict.description")}</DialogDescription>
        </DialogHeader>

        <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt>{t("settings.conflict.yourVersion")}</dt>
          <dd className="text-foreground font-mono">{conflict?.revision ?? ""}</dd>
          <dt>{t("settings.conflict.onDisk")}</dt>
          <dd className="text-foreground font-mono">{t("settings.conflict.changedByOther")}</dd>
        </dl>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => void reload()} data-testid="conflict-reload">
            {t("settings.conflict.reload")}
          </Button>
          <Button type="button" variant="destructive" onClick={() => useSettings.setState({ conflict: undefined })} data-testid="conflict-dismiss">
            {t("settings.conflict.keepEditing")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
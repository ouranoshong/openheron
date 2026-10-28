import { useEffect } from "react"

import { Button } from "@/components/ui/button"
import { useSettings } from "@/lib/settings-store"

export function SettingsEntry({ className }: { className?: string }) {
  const openSettings = useSettings((s) => s.openSettings)
  const open = useSettings((s) => s.open)
  const close = useSettings((s) => s.closeSettings)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === ",") {
        e.preventDefault()
        if (useSettings.getState().open) close()
        else void openSettings()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [open, close, openSettings])

  return (
    <Button
      type="button"
      variant="ghost"
      className={className}
      onClick={() => void openSettings()}
      data-testid="settings-entry"
    >
      Settings
      <kbd className="text-muted-foreground ml-auto text-[10px]">⌘,</kbd>
    </Button>
  )
}
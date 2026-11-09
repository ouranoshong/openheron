import { MoonIcon, SunIcon } from "lucide-react"
import { useSyncExternalStore } from "react"

import { isDark, subscribeTheme, toggleTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"

// ★ The dark palette was in the stylesheet all along and nothing ever
//   turned it on — a second set of colors that no user could reach and
//   nobody had to keep true. The toggle is what makes it real.
export function ThemeToggle({ className }: { className?: string }) {
  const dark = useSyncExternalStore(subscribeTheme, isDark, isDark)

  return (
    <button
      type="button"
      data-testid="theme-toggle"
      onClick={() => toggleTheme()}
      aria-label={dark ? "Switch to light theme" : "Switch to dark theme"}
      title={dark ? "Switch to light theme" : "Switch to dark theme"}
      className={cn(
        "hover:bg-accent hover:text-accent-foreground grid size-8 shrink-0 place-items-center rounded-md",
        className,
      )}
    >
      {dark ? <SunIcon className="size-4" /> : <MoonIcon className="size-4" />}
    </button>
  )
}

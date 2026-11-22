import { useSyncExternalStore } from "react"

import { locale, setLocale, subscribeLocale } from "@/lib/i18n"
import { useT } from "@/lib/use-t"
import { cn } from "@/lib/utils"

// ★ The language is a **preference**, like the theme: it is remembered on
//   this machine, and it beats the browser's guess.
//
// ★ The button names the language it switches **to**, in that language's own
//   name: a reader who does not read the current language still recognizes
//   "中" and "EN". ★ The label says it in words too, because an icon alone
//   is not an instruction.
export function LocaleToggle({ className }: { className?: string }) {
  const t = useT()
  const loc = useSyncExternalStore(subscribeLocale, locale, locale)
  const next = loc === "zh" ? "en" : "zh"
  const label = next === "zh" ? t("header.locale.toZh") : t("header.locale.toEn")

  return (
    <button
      type="button"
      data-testid="locale-toggle"
      onClick={() => setLocale(next)}
      aria-label={label}
      title={label}
      className={cn(
        "hover:bg-accent hover:text-accent-foreground grid size-8 shrink-0 place-items-center rounded-md text-xs font-medium",
        className,
      )}
    >
      {next === "zh" ? "中" : "EN"}
    </button>
  )
}

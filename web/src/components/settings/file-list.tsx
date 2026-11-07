
import { useShallow } from "zustand/react/shallow"

import { cn } from "@/lib/utils"
import {
  selectCategories,
  selectItems,
  useSettings,
  type SettingsState,
} from "@/lib/settings-store"
import { NewFileButton } from "@/components/settings/new-file-dialog"

const LABEL: Record<string, string> = {
  agents: "Agents",
  llm: "Providers",
  server: "Server",
  tools: "Tools",
  mcps: "MCP servers",
}

export function SettingsFileList() {
  const items = useSettings(useShallow(selectItems))
  const categories = useSettings(useShallow(selectCategories))
  const selected = useSettings((s) => s.selected)
  const select = useSettings((s) => s.select)

  const visible = items.filter((i) => {
    const section = i.path.split("/")[0] ?? ""
    return categories.some((c) => c.id === section)
  })

  return (
    <div className="flex w-56 shrink-0 flex-col gap-2 overflow-y-auto">
      <ul className="flex flex-col gap-0.5" data-testid="settings-file-list">
        {visible.map((item) => (
          <li key={item.path}>
            <button
              type="button"
              onClick={() => void select(item.path)}
              aria-current={selected === item.path}
              className={cn(
                "hover:bg-accent flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs",
                selected === item.path && "bg-accent",
              )}
            >
              <span className="min-w-0 flex-1 truncate">{item.path}</span>
              {!item.hot && <Badge title="requires a restart">restart</Badge>}
            </button>
          </li>
        ))}
        {visible.length === 0 && (
          <li className="text-muted-foreground px-2 py-1.5 text-xs">No files yet</li>
        )}
      </ul>

      <div className="mt-auto pt-2">
        <NewFileButton />
      </div>
    </div>
  )
}

function Badge({ children, title }: { children: React.ReactNode; title: string }) {
  return (
    <span title={title} aria-label={title} className="bg-muted text-muted-foreground rounded px-1 text-[10px]">
      {children}
    </span>
  )
}

export function SectionPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const categories = useSettings(useShallow(selectCategories))
  const editable = categories.filter((c: SettingsState["manifest"] extends undefined ? never : NonNullable<SettingsState["manifest"]>["categories"][number]) => c.editable)
  return (
    <div className="flex flex-wrap gap-1" role="group" aria-label="Config section">
      {editable.map((c) => (
        <button
          key={c.id}
          type="button"
          onClick={() => onChange(c.id)}
          aria-pressed={value === c.id}
          className={cn("rounded-md border px-2 py-1 text-xs", value === c.id && "bg-accent")}
        >
          {LABEL[c.id] ?? c.id}
        </button>
      ))}
    </div>
  )
}
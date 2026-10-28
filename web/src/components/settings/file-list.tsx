/**
 * 分类分段控件 + 文件列表。
 *
 * ★ 两栏而非三栏：分类只有四个且名称短，分段控件横排比竖列省空间，
 *   左栏就全给文件列表。三栏要吃掉 240+200 的固定宽度，在 1280px 屏上
 *   编辑器只剩 500px 出头 —— 而 agents/*.md 的正文是要长看的。
 */

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
  policy: "Approval policy",
  agents: "Agents",
  llm: "Providers",
  server: "Server",
}

export function SettingsFileList() {
  // ★ 两个都返回新数组（filter），必须配 useShallow —— 见 store.ts 的纪律。
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

/** Badge 是一个极小的标记 —— 它必须能被读屏念出来，所以带 title。 */
function Badge({ children, title }: { children: React.ReactNode; title: string }) {
  return (
    <span title={title} aria-label={title} className="bg-muted text-muted-foreground rounded px-1 text-[10px]">
      {children}
    </span>
  )
}

/** SectionPicker 供 NewFileDialog 用：列出可写入的分区。 */
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
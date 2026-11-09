import { useMemo } from "react"
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
  skills: "Skills",
  tools: "Tools",
  mcps: "MCP servers",
}

// ★ The list is a **file tree**, and the tree is built from the paths the
//   server sends — not from a shape this file knows. A partition that holds a
//   folder per entity (`skills/<name>/SKILL.md`) needs no branch here: split
//   the path, group by folder, render.
type Node = {
  name: string
  path: string
  children: Node[]
  leaf?: SettingsState["manifest"] extends undefined
    ? never
    : NonNullable<SettingsState["manifest"]>["items"][number]
}

function buildTree(paths: { path: string; hot: boolean }[]): Node[] {
  const roots: Node[] = []
  for (const item of paths) {
    const segs = item.path.split("/")
    let level = roots
    let prefix = ""
    segs.forEach((seg, i) => {
      prefix = prefix ? `${prefix}/${seg}` : seg
      const last = i === segs.length - 1
      let node = level.find((n) => n.name === seg)
      if (!node) {
        node = { name: seg, path: prefix, children: [] }
        level.push(node)
      }
      if (last) node.leaf = item as Node["leaf"]
      level = node.children
    })
  }
  const sort = (nodes: Node[]): Node[] => {
    nodes.sort((a, b) => {
      // A folder before a file, then by name — the order of a file tree
      // everywhere else, so a reader does not have to learn this one.
      const af = a.children.length > 0 ? 0 : 1
      const bf = b.children.length > 0 ? 0 : 1
      return af - bf || a.name.localeCompare(b.name)
    })
    nodes.forEach((n) => sort(n.children))
    return nodes
  }
  return sort(roots)
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

  const tree = useMemo(() => buildTree(visible), [visible])

  return (
    <div className="flex w-56 shrink-0 flex-col gap-2 overflow-y-auto">
      <ul className="flex flex-col gap-0.5" data-testid="settings-file-list">
        {tree.map((node) => (
          <TreeBranch key={node.path} node={node} depth={0} selected={selected} onSelect={select} />
        ))}
        {tree.length === 0 && (
          <li className="text-muted-foreground px-2 py-1.5 text-xs">No files yet</li>
        )}
      </ul>

      <div className="mt-auto pt-2">
        <NewFileButton />
      </div>
    </div>
  )
}

// ★ A folder row is a **heading**, not a control: it does not collapse, and
//   it is not selectable. Collapsing is a state this dialog would have to keep
//   across files, and a tree of six folders reads faster than one that hides
//   half of itself. What it does carry is the partition's label — the same
//   word the section picker uses — so "llm" reads as Providers in both places.
function TreeBranch({
  node,
  depth,
  selected,
  onSelect,
}: {
  node: Node
  depth: number
  selected?: string
  onSelect: (path: string) => void
}) {
  if (node.children.length > 0) {
    const label = depth === 0 ? (LABEL[node.name] ?? node.name) : node.name
    // ★ The top-level heading is a **word** — "Providers" for `llm/` — so it
    //   is set as a small-caps label. A folder *inside* a partition is an
    //   identifier the user typed and the agent writes in `skills:`, so it is
    //   shown exactly as it is on disk: upper-casing `code-review` would
    //   display a name that does not exist.
    const heading =
      depth === 0
        ? "text-muted-foreground text-[11px] font-medium tracking-wide uppercase"
        : "text-muted-foreground font-mono text-xs"
    return (
      <li data-testid={`settings-tree-folder-${node.path}`}>
        <div
          className={cn("flex items-center gap-1 px-2 pt-2 pb-1", heading)}
          style={{ paddingLeft: `${0.5 + depth * 0.75}rem` }}
        >
          {label}
        </div>
        <ul className="flex flex-col gap-0.5">
          {node.children.map((child) => (
            <TreeBranch
              key={child.path}
              node={child}
              depth={depth + 1}
              selected={selected}
              onSelect={onSelect}
            />
          ))}
        </ul>
      </li>
    )
  }

  // ★ The row shows the file's own name, indented under its folder: the path
  //   is already implied by where the row sits, and repeating it in every row
  //   is what made the old flat list hard to scan.
  //
  // ★ Its accessible name is still the **whole path**. A row called "main.md"
  //   tells a reader who cannot see the indent nothing about which partition
  //   it is in, and the path is also what every other part of the dialog calls
  //   this file.
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(node.path)}
        aria-current={selected === node.path}
        aria-label={node.path}
        title={node.path}
        className={cn(
          "hover:bg-accent flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs",
          selected === node.path && "bg-accent",
        )}
        style={{ paddingLeft: `${0.5 + depth * 0.75}rem` }}
      >
        <span className="min-w-0 flex-1 truncate font-mono">{node.name}</span>
        {node.leaf && !node.leaf.hot && <Badge title="requires a restart">restart</Badge>}
      </button>
    </li>
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

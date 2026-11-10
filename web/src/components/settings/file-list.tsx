import { useMemo, useState } from "react"
import { ChevronRightIcon } from "lucide-react"
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

// ★ Every folder above a path, outermost first: "skills/code-review/SKILL.md"
//   becomes ["skills", "skills/code-review"]. Used to reveal the selection.
function ancestorsOf(path: string | undefined): string[] {
  if (!path) return []
  const segs = path.split("/")
  const out: string[] = []
  for (let i = 1; i < segs.length; i++) out.push(segs.slice(0, i).join("/"))
  return out
}

export function SettingsFileList() {
  const items = useSettings(useShallow(selectItems))
  const categories = useSettings(useShallow(selectCategories))
  const selected = useSettings((s) => s.selected)
  const select = useSettings((s) => s.select)

  const visible = useMemo(
    () =>
      items.filter((i) => {
        const section = i.path.split("/")[0] ?? ""
        return categories.some((c) => c.id === section)
      }),
    [items, categories],
  )

  const tree = useMemo(() => buildTree(visible), [visible])

  // ★ Folders start closed — six rows instead of a scroll through every skill.
  //   The one exception is the selection: the dialog opens with a file already
  //   chosen (`firstEditable`), and a tree that hides the chosen file reads as
  //   broken. The initial state is computed, not set in an effect, so the
  //   first paint is already correct.
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(ancestorsOf(selected)))

  // ★ The selection also moves from outside the tree: `openSettings` picks the
  //   first file, `newFile` selects what it just created. React's "adjust the
  //   state when a prop changed" pattern opens its ancestors in the same
  //   paint — an effect would show the tree closed for one frame first.
  const [revealedFor, setRevealedFor] = useState(selected)
  if (selected !== revealedFor) {
    setRevealedFor(selected)
    setExpanded((prev) => new Set([...prev, ...ancestorsOf(selected)]))
  }

  const toggle = (path: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })

  return (
    <div className="flex w-56 shrink-0 flex-col gap-2 overflow-y-auto">
      <ul className="flex flex-col gap-0.5" data-testid="settings-file-list">
        {tree.map((node) => (
          <TreeBranch
            key={node.path}
            node={node}
            depth={0}
            selected={selected}
            expanded={expanded}
            onSelect={select}
            onToggle={toggle}
          />
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

// ★ A folder row is a **control**: the chevron says it opens, `aria-expanded`
//   says whether it is open, and the whole row is the hit target. It used to
//   be a heading, on the argument that six folders read faster than a tree
//   that hides half of itself — true for six, wrong once every skill is a
//   folder. Closed by default, and the selection's ancestors are opened for it
//   (see SettingsFileList), so nothing the reader is looking at is hidden.
//
// ★ The label keeps the partition's word at the top level — "Providers" for
//   `llm/` — so the section picker and the tree agree. A folder *inside* a
//   partition is an identifier the user typed and the agent writes in
//   `skills:`, so it is shown exactly as it is on disk.
function TreeBranch({
  node,
  depth,
  selected,
  expanded,
  onSelect,
  onToggle,
}: {
  node: Node
  depth: number
  selected?: string
  expanded: Set<string>
  onSelect: (path: string) => void
  onToggle: (path: string) => void
}) {
  if (node.children.length > 0) {
    const label = depth === 0 ? (LABEL[node.name] ?? node.name) : node.name
    const open = expanded.has(node.path)
    const heading =
      depth === 0
        ? "text-muted-foreground text-[11px] font-medium tracking-wide uppercase"
        : "text-muted-foreground font-mono text-xs"
    return (
      <li data-testid={`settings-tree-folder-${node.path}`}>
        <button
          type="button"
          data-testid={`settings-tree-toggle-${node.path}`}
          aria-expanded={open}
          onClick={() => onToggle(node.path)}
          className={cn(
            "hover:bg-accent flex w-full items-center gap-1 rounded-md px-2 py-1 text-left",
            heading,
          )}
          style={{ paddingLeft: `${0.5 + depth * 0.75}rem` }}
        >
          <ChevronRightIcon
            className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")}
          />
          <span data-testid={`settings-tree-label-${node.path}`} className="min-w-0 flex-1 truncate">
            {label}
          </span>
        </button>
        {open && (
          <ul className="flex flex-col gap-0.5">
            {node.children.map((child) => (
              <TreeBranch
                key={child.path}
                node={child}
                depth={depth + 1}
                selected={selected}
                expanded={expanded}
                onSelect={onSelect}
                onToggle={onToggle}
              />
            ))}
          </ul>
        )}
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
  //
  // ★ The empty icon slot keeps a file's name in the same column as its
  //   folder's label; without it the two would disagree by the width of the
  //   chevron and the indent would read as a wobble.
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(node.path)}
        aria-current={selected === node.path}
        aria-label={node.path}
        title={node.path}
        className={cn(
          "hover:bg-accent flex w-full items-center gap-1 rounded-md px-2 py-1.5 text-left text-xs",
          selected === node.path && "bg-accent",
        )}
        style={{ paddingLeft: `${0.5 + depth * 0.75}rem` }}
      >
        <span aria-hidden className="size-3 shrink-0" />
        <span
          data-testid={`settings-tree-label-${node.path}`}
          className="min-w-0 flex-1 truncate font-mono"
        >
          {node.name}
        </span>
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

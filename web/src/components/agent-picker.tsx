/**
 * Agent 选择器。
 *
 * ★ 列**全部** agent（每个声明了的都能当会话的 agent，装配按它自己的配置
 *   来）；点击之后是否真的允许由 PATCH 回答。
 *
 * ★ 运行中 / 待审批时整列禁用并说明原因，而不是让用户点一下再收 409：
 *   后者会让人以为是网络问题，而真实原因是「这一轮还没收尾」。
 */

import { CheckIcon } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import type { AgentView } from "@/lib/api"
import { teamSize } from "@/lib/agent-info"
import { cn } from "@/lib/utils"

export interface AgentPickerProps {
  open: boolean
  onClose(): void
  agents: readonly AgentView[]
  currentId: string
  busy: boolean
  /** 打开时预填的过滤词（`/agents t` 的 t）。 */
  filter?: string
  error?: string
  onPick(agentId: string): void | Promise<void>
}

export function AgentPicker({
  open,
  onClose,
  agents,
  currentId,
  busy,
  filter,
  error,
  onPick,
}: AgentPickerProps) {
  // ★ 组件只在打开时挂载（见 composer）：过滤词因此可以直接用作初值，
  //   不需要「打开时重置」的 effect —— 而 effect 里同步 setState 会多一次
  //   渲染，oxlint 也会把它标成 cascading renders。
  const [query, setQuery] = useState(filter ?? "")
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLUListElement>(null)

  const items = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return [...agents]
    return agents.filter(
      (a) =>
        a.id.toLowerCase().includes(q) ||
        (a.description ?? "").toLowerCase().includes(q) ||
        `${a.model?.provider ?? ""}/${a.model?.model ?? ""}`.toLowerCase().includes(q),
    )
  }, [agents, query])

  // 过滤后条数会变少：游标必须跟着夹住，否则 Enter 会落到 undefined。
  const cursor = items.length === 0 ? -1 : Math.min(active, items.length - 1)

  // ★ 高亮的那一项要**滚进视野**。
  //
  //   列表是 `max-h-72 overflow-y-auto`：agent 多的时候它会滚。而键盘上下
  //   移动只改游标，不改滚动位置 —— 于是光标走到了看不见的地方：用户按
  //   Enter 选中了一个自己都看不到的 agent。
  //
  //   `block: "nearest"` 只在必要时滚动，且不动外层（弹窗本身不该被带着
  //   滚）。鼠标悬停也会改游标，但悬停得到的就是看得见的项，滚动是空操作。
  useEffect(() => {
    const list = listRef.current
    if (!list || cursor < 0) return
    const option = list.querySelectorAll<HTMLElement>('[data-testid="agent-option"]')[cursor]
    option?.scrollIntoView({ block: "nearest" })
  }, [cursor, items])

  const pick = (agent: AgentView) => {
    if (busy) return
    void onPick(agent.id)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => (!v ? onClose() : undefined)}>
      <DialogContent data-testid="agent-picker" className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Switch agent</DialogTitle>
          <DialogDescription>
            {busy
              ? "A run is in progress — finish or stop it before switching."
              : "The selected agent runs the next turn as the root."}
          </DialogDescription>
        </DialogHeader>

        <Input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setActive(0)
          }}
          placeholder="Filter agents"
          aria-label="Filter agents"
          data-testid="agent-picker-filter"
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault()
              if (items.length === 0) return
              setActive((i) =>
                (Math.min(i, items.length - 1) + (e.key === "ArrowDown" ? 1 : items.length - 1)) %
                items.length,
              )
              return
            }
            if (e.key === "Enter") {
              e.preventDefault()
              const chosen = cursor >= 0 ? items[cursor] : undefined
              if (chosen) pick(chosen)
            }
          }}
        />

        {error ? (
          <p data-testid="agent-picker-error" className="text-destructive text-xs">
            {error}
          </p>
        ) : null}

        <ul
          ref={listRef}
          role="listbox"
          aria-label="Agents"
          data-testid="agent-picker-list"
          className="max-h-72 overflow-y-auto"
        >
          {items.map((agent, i) => {
            const current = agent.id === currentId
            const model = `${agent.model?.provider ?? "unknown"}/${agent.model?.model ?? "unknown"}`
            const team = teamSize(agent)
            return (
              <li key={agent.id} role="presentation">
                <button
                  type="button"
                  role="option"
                  aria-selected={current}
                  data-testid="agent-option"
                  data-agent-id={agent.id}
                  disabled={busy}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => pick(agent)}
                  className={cn(
                    "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm disabled:opacity-40",
                    i === cursor && "bg-accent",
                  )}
                >
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1 font-medium">
                      {agent.id}
                      {current ? (
                        <CheckIcon className="size-3.5" aria-label="Current agent" />
                      ) : null}
                      {/* ★ 队伍规模：切到一个 agent 会带上它的下属 ——
                          切到一支队伍与切到一个光杆，是两件不同的事。 */}
                      {team > 0 ? (
                        <span
                          data-testid="agent-option-team"
                          className="text-muted-foreground rounded border px-1 text-[10px] leading-4"
                          title={`${team} sub-agent${team > 1 ? "s" : ""}`}
                        >
                          {team}
                        </span>
                      ) : null}
                    </span>
                    <span className="text-muted-foreground block truncate text-xs">
                      {model}
                      {agent.model?.reasoning ? ` · Reasoning: ${agent.model.reasoning}` : ""}
                      {team > 0 ? ` · ${team} sub-agent${team > 1 ? "s" : ""}` : ""}
                    </span>
                    {agent.description ? (
                      <span className="text-muted-foreground block truncate text-xs">
                        {agent.description}
                      </span>
                    ) : null}
                  </span>
                </button>
              </li>
            )
          })}
          {items.length === 0 ? (
            <li data-testid="agent-picker-empty" className="text-muted-foreground px-2 py-2 text-sm">
              No matching agents
            </li>
          ) : null}
        </ul>
      </DialogContent>
    </Dialog>
  )
}

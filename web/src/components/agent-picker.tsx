
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

  const cursor = items.length === 0 ? -1 : Math.min(active, items.length - 1)

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

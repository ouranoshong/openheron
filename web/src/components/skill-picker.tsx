import { useEffect, useMemo, useRef, useState } from "react"

import { cn } from "@/lib/utils"
import { skillModeLabel } from "@/lib/skill-info"
import type { SkillView } from "@/lib/api"

import { Input } from "@/components/ui/input"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

export interface SkillPickerProps {
  open: boolean
  onClose(): void
  skills: SkillView[]
  filter?: string
  error?: string
  onPick(skill: SkillView): void
}

export function SkillPicker({ open, onClose, skills, filter, error, onPick }: SkillPickerProps) {
  const [query, setQuery] = useState(filter ?? "")
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLUListElement>(null)

  const items = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return [...skills]
    return skills.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        (s.description ?? "").toLowerCase().includes(q) ||
        (s.agent ?? "").toLowerCase().includes(q),
    )
  }, [skills, query])

  const cursor = items.length === 0 ? -1 : Math.min(active, items.length - 1)

  useEffect(() => {
    const list = listRef.current
    if (!list || cursor < 0) return
    const option = list.querySelectorAll<HTMLElement>('[data-testid="skill-option"]')[cursor]
    option?.scrollIntoView({ block: "nearest" })
  }, [cursor, items])

  const pick = (skill: SkillView) => {
    void onPick(skill)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => (!v ? onClose() : undefined)}>
      <DialogContent data-testid="skill-picker" className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Use a skill</DialogTitle>
          <DialogDescription>
            The skill is read by the model before it works — pick one, then say what to do.
          </DialogDescription>
        </DialogHeader>

        <Input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setActive(0)
          }}
          placeholder="Filter skills"
          aria-label="Filter skills"
          data-testid="skill-picker-filter"
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
          <p data-testid="skill-picker-error" className="text-destructive text-xs">
            {error}
          </p>
        ) : null}

        <ul
          ref={listRef}
          role="listbox"
          aria-label="Skills"
          data-testid="skill-picker-list"
          className="max-h-72 overflow-y-auto"
        >
          {items.map((skill, i) => (
            <li key={skill.name} role="presentation">
              <button
                type="button"
                role="option"
                aria-selected={i === cursor}
                data-testid="skill-option"
                data-skill-name={skill.name}
                onMouseEnter={() => setActive(i)}
                onClick={() => pick(skill)}
                className={cn(
                  "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm",
                  i === cursor && "bg-accent",
                )}
              >
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1 font-medium">
                    {skill.name}
                    <span
                      data-testid="skill-option-mode"
                      className="text-muted-foreground rounded border px-1 text-[10px] leading-4"
                    >
                      {skillModeLabel(skill)}
                    </span>
                  </span>
                  {skill.description ? (
                    <span className="text-muted-foreground block truncate text-xs">
                      {skill.description}
                    </span>
                  ) : null}
                  {skill.agent ? (
                    <span className="text-muted-foreground block truncate text-xs">
                      agent: {skill.agent}
                    </span>
                  ) : null}
                </span>
              </button>
            </li>
          ))}
        </ul>

        {items.length === 0 ? (
          <p data-testid="skill-picker-empty" className="text-muted-foreground text-xs">
            No skill matches. Skills come from .openheron/skills/&lt;name&gt;/SKILL.md.
          </p>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

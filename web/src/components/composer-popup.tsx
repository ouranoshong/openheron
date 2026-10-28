import { useEffect, useRef } from "react"

export interface PopupItem {
  id: string
  label: string
  hint?: string
  description?: string
}

/**
 * Presentational list for @file and /command completion.
 *
 * It lives apart from the textarea so the caret/keyboard rules stay in
 * composer.tsx and the pure parsers, while this file only renders items.
 */
export function ComposerPopup({
  items,
  active,
  loading,
  emptyLabel,
  error,
  onPick,
  onHover,
}: {
  items: PopupItem[]
  active: number
  loading?: boolean
  emptyLabel: string
  error?: string
  onPick(index: number): void
  onHover(index: number): void
}) {
  const listRef = useRef<HTMLDivElement>(null)
  const activeId = items[active]?.id

  // Keep the highlighted option inside the scroll box. Manual math (instead
  // of scrollIntoView) prevents the page from moving when the popup is near
  // the viewport edge.
  useEffect(() => {
    const list = listRef.current
    const option = list?.querySelector<HTMLElement>('[role="option"][aria-selected="true"]')
    if (!list || !option) return
    const listRect = list.getBoundingClientRect()
    const optionRect = option.getBoundingClientRect()
    if (optionRect.top < listRect.top) {
      list.scrollTop -= listRect.top - optionRect.top
    } else if (optionRect.bottom > listRect.bottom) {
      list.scrollTop += optionRect.bottom - listRect.bottom
    }
  }, [active, activeId, items.length])

  return (
    <div
      ref={listRef}
      id="composer-popup"
      role="listbox"
      aria-label="Completions"
      data-testid="composer-popup"
      className="bg-popover absolute right-0 bottom-full left-0 z-20 mb-2 max-h-64 overflow-y-auto rounded-lg border p-1 shadow-md"
    >
      {loading && <div className="text-muted-foreground px-2 py-1.5 text-xs">Searching…</div>}
      {!loading && error && <div className="text-muted-foreground px-2 py-1.5 text-xs">{error}</div>}
      {!loading && !error && items.length === 0 && (
        <div className="text-muted-foreground px-2 py-1.5 text-xs">{emptyLabel}</div>
      )}
      {!loading &&
        !error &&
        items.map((item, i) => (
          <div
            key={item.id}
            id={`composer-option-${encodeURIComponent(item.id)}`}
            role="option"
            data-testid="composer-option"
            aria-selected={i === active}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onPick(i)}
            onMouseEnter={() => onHover(i)}
            className={`hover:bg-accent cursor-pointer rounded-md px-2 py-1.5 text-xs ${
              i === active ? "bg-accent" : ""
            }`}
          >
            <div className="flex min-w-0 items-baseline gap-2">
              <span className="truncate font-medium">{item.label}</span>
              {item.hint && <span className="text-muted-foreground shrink-0">{item.hint}</span>}
            </div>
            {item.description && (
              <div className="text-muted-foreground truncate opacity-80">{item.description}</div>
            )}
          </div>
        ))}
    </div>
  )
}

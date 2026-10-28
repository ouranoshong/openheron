import type { ComponentType } from "react"
import { XIcon } from "lucide-react"

const ICONS: Record<string, ComponentType<{ className?: string }>> = {
  XIcon,
}

export interface IconPlaceholderProps {
  lucide?: string
  tabler?: string
  hugeicons?: string
  phosphor?: string
  remixicon?: string
  className?: string
}

export function IconPlaceholder({
  lucide,
  className = "size-4",
}: IconPlaceholderProps) {
  const Icon = lucide ? ICONS[lucide] : undefined
  return Icon ? <Icon className={className} /> : null
}
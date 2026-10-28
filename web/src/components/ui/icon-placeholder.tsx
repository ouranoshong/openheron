import type { ComponentType } from "react"
import { XIcon } from "lucide-react"

/**
 * shadcn 的多图标库占位组件 —— 它为每个图标列出 lucide / tabler / hugeicons /
 * phosphor / remixicon 五套名字，便于换库。registry 的 dialog.tsx 会 import 它。
 *
 * openheron 固定用 lucide，所以只实现 lucide 分支：按名字取组件，缺了就渲染空。
 * 换图标库时才需要补全其余分支。
 */
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
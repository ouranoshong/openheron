import { cn } from "@/lib/utils"

export function BrandMark({ className }: { className?: string }) {
  return <img src="/favicon.svg" alt="" className={cn("shrink-0", className)} />
}

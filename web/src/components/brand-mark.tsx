import { cn } from "@/lib/utils"
import { BrandMarkGlyph } from "./brand-mark.generated"

/**
 * The openheron mark.
 *
 * ★ It is inline SVG, not an image: an SVG loaded through an `img` element is
 *   a separate document, so the page's stylesheet cannot reach its parts — and
 *   the mark's entrance is made of exactly those parts.
 * ★ The drawing is not written here. It is generated from `public/favicon.svg`
 *   by `scripts/generate-brand-mark.ts`, so the browser's tab icon and the
 *   interface's mark are the same drawing rather than two that drift.
 */
export function BrandMark({
  className,
  decorative = true,
  play = false,
  testId,
}: {
  className?: string
  /** The mark is decorative wherever the word "openheron" is already next to it. */
  decorative?: boolean
  /** Play the one-shot entrance — only where the mark is big enough to read. */
  play?: boolean
  testId?: string
}) {
  return (
    <BrandMarkGlyph
      className={cn("shrink-0", play && "brand-mark--play", className)}
      decorative={decorative}
      testId={testId}
    />
  )
}

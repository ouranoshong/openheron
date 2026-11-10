/**
 * Generate the inline brand mark from public/favicon.svg.
 *
 * ★ One drawing, two artifacts. The browser's favicon and the interface's mark
 *   have to be the same drawing, and two hand-kept copies drift: someone moves
 *   the beak in the favicon and the interface keeps the old one, so the tab
 *   icon and the logo stop being the same thing. The SVG is therefore the
 *   source of truth, and this script is the only thing that writes
 *   src/components/brand-mark.generated.tsx.
 *
 * Usage:
 *   bun run scripts/generate-brand-mark.ts           write the generated file
 *   bun run scripts/generate-brand-mark.ts --check   fail when it is stale
 *
 * ★ The script is deliberately dumb: it parses the SVG and prints the same
 *   elements as JSX. It does not know what the mark looks like — that lives in
 *   the SVG, where a designer can change it without reading a line of
 *   TypeScript.
 */

import { readFile, writeFile } from "node:fs/promises"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const HERE = dirname(fileURLToPath(import.meta.url))
const WEB_ROOT = join(HERE, "..")
const SOURCE_PATH = join(WEB_ROOT, "public", "favicon.svg")
const TARGET_PATH = join(WEB_ROOT, "src", "components", "brand-mark.generated.tsx")

type Attr = { name: string; value: string }
type El = { tag: string; attrs: Attr[]; children: El[]; text: string }

/**
 * ★ The ids the source uses, and the suffix each one gets per instance.
 *   The gradient and the two masks are referenced by id, and the page can hold
 *   more than one mark (the header and the welcome screen) — so the ids have
 *   to be per instance, or the second mark resolves its mask to the first
 *   mark's.
 */
const ID_SUFFIX: Record<string, string> = {
  openheronBrand: "brand",
  openheronBody: "body",
  openheronHalo: "halo",
}

/** The SVG attribute names JSX spells differently. */
const ATTR_NAME: Record<string, string> = {
  class: "className",
  "clip-path": "clipPath",
  "clip-rule": "clipRule",
  "fill-opacity": "fillOpacity",
  "fill-rule": "fillRule",
  "font-family": "fontFamily",
  "mask-type": "maskType",
  "stop-color": "stopColor",
  "stop-opacity": "stopOpacity",
  "stroke-linecap": "strokeLinecap",
  "stroke-linejoin": "strokeLinejoin",
  "stroke-width": "strokeWidth",
  "text-anchor": "textAnchor",
}

const TAG_RE =
  /<!--[\s\S]*?-->|<\/\s*([A-Za-z][\w:.-]*)\s*>|<\s*([A-Za-z][\w:.-]*)((?:\s+[A-Za-z][\w:.-]*\s*=\s*"[^"]*")*)\s*(\/?)>/g
const ATTR_RE = /([A-Za-z][\w:.-]*)\s*=\s*"([^"]*)"/g
const URL_REF_RE = /^url\(#([A-Za-z][\w:.-]*)\)$/

/** Parse the elements of an SVG document. Comments are dropped. */
export function parseSvg(source: string): El[] {
  const roots: El[] = []
  const stack: El[] = []
  TAG_RE.lastIndex = 0
  let match: RegExpExecArray | null
  let cursor = 0
  while ((match = TAG_RE.exec(source)) !== null) {
    // The text between two tags belongs to the element that is still open.
    const text = source.slice(cursor, match.index).trim()
    if (text && stack.length > 0) {
      stack[stack.length - 1].text = text
    }
    cursor = match.index + match[0].length

    const [full, closeTag, openTag, attrText, selfClose] = match
    if (full.startsWith("<!--")) continue
    if (closeTag) {
      stack.pop()
      continue
    }
    const attrs: Attr[] = []
    ATTR_RE.lastIndex = 0
    let attr: RegExpExecArray | null
    while ((attr = ATTR_RE.exec(attrText ?? "")) !== null) {
      attrs.push({ name: attr[1], value: attr[2] })
    }
    const el: El = { tag: openTag as string, attrs, children: [], text: "" }
    const parent = stack[stack.length - 1]
    if (parent) parent.children.push(el)
    else roots.push(el)
    if (!selfClose) stack.push(el)
  }
  return roots
}

function jsxAttrName(name: string): string {
  return ATTR_NAME[name] ?? name
}

function jsxAttrValue(name: string, value: string): string {
  if (name === "id") {
    const suffix = ID_SUFFIX[value]
    if (suffix) return '{uid + "-' + suffix + '"}'
  }
  const ref = URL_REF_RE.exec(value)
  if (ref) {
    const suffix = ID_SUFFIX[ref[1]]
    if (suffix) return '{`url(#${uid}-' + suffix + ')`}'
  }
  return '"' + value + '"'
}

function render(el: El, indent: string): string {
  const attrs = el.attrs.map((a) => jsxAttrName(a.name) + "=" + jsxAttrValue(a.name, a.value))
  const head =
    attrs.length > 1
      ? indent + "<" + el.tag + "\n" + attrs.map((a) => indent + "  " + a).join("\n") + "\n" + indent + ">"
      : indent + "<" + el.tag + (attrs.length ? " " + attrs[0] : "") + ">"

  if (el.children.length === 0) {
    if (el.text) {
      const inline = attrs.length
        ? indent + "<" + el.tag + " " + attrs.join(" ") + ">"
        : indent + "<" + el.tag + ">"
      return inline + el.text + "</" + el.tag + ">"
    }
    return indent + "<" + el.tag + (attrs.length ? " " + attrs.join(" ") : "") + " />"
  }
  const body = el.children.map((c) => render(c, indent + "  ")).join("\n")
  return head + "\n" + body + "\n" + indent + "</" + el.tag + ">"
}

/** The file's own header — what a reader needs before touching the body. */
const FILE_HEADER = `// ★ GENERATED FILE — do not edit it by hand.
//
//   Source:      public/favicon.svg
//   Regenerate:  bun run gen:brand
//   Verify:      bun run gen:brand:check
//
// ★ The favicon and the interface's mark are one drawing. Two hand-kept copies
//   drift, so the SVG is the source and this file is written from it.
import { useId } from "react"

export function BrandMarkGlyph({
  className,
  decorative = false,
  testId,
}: {
  className?: string
  decorative?: boolean
  testId?: string
}) {
  // ★ The gradient and the two masks are referenced by id, and the page can
  //   hold more than one mark (the header and the welcome screen). React's
  //   useId gives every instance its own prefix, so the second mark does not
  //   resolve its mask to the first mark's.
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, "")
  return (
`

const FILE_FOOTER = `    </svg>
  )
}
`

/** Turn the source SVG into the generated component's source. */
export function generateBrandMark(svg: string): string {
  const roots = parseSvg(svg)
  const root = roots.find((el) => el.tag === "svg")
  if (!root) throw new Error("favicon.svg has no root svg element")
  const viewBox = root.attrs.find((a) => a.name === "viewBox")?.value ?? "0 0 48 48"

  const open =
    '    <svg\n' +
    '      xmlns="http://www.w3.org/2000/svg"\n' +
    '      viewBox="' +
    viewBox +
    '"\n' +
    "      role={decorative ? undefined : \"img\"}\n" +
    "      aria-hidden={decorative ? true : undefined}\n" +
    '      aria-label={decorative ? undefined : "openheron"}\n' +
    "      data-testid={testId}\n" +
    "      className={className}\n" +
    "    >"

  const body = root.children.map((c) => render(c, "      ")).join("\n")
  return FILE_HEADER + open + "\n" + body + "\n" + FILE_FOOTER
}

async function main(): Promise<void> {
  const check = process.argv.includes("--check")
  const svg = await readFile(SOURCE_PATH, "utf8")
  const generated = generateBrandMark(svg)

  if (check) {
    let current = ""
    try {
      current = await readFile(TARGET_PATH, "utf8")
    } catch {
      current = ""
    }
    if (current !== generated) {
      console.error(
        "✗ " +
          TARGET_PATH +
          " is out of date — run `bun run gen:brand` and commit the result",
      )
      process.exit(1)
    }
    console.log("✓ the brand mark is in sync with public/favicon.svg")
    return
  }

  await writeFile(TARGET_PATH, generated, "utf8")
  console.log("✓ wrote " + TARGET_PATH)
}

if (import.meta.main) {
  await main()
}

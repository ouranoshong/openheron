import { describe, expect, test } from "bun:test"

import { generateBrandMark } from "../scripts/generate-brand-mark"

// ★ The favicon and the interface's mark are one drawing. Two hand-kept
//   copies drift — someone moves the beak in the favicon and the interface
//   keeps the old one — so the SVG is the source and the component is
//   generated from it. These tests are the gate that keeps the two in step:
//   a generated file that is stale is a silent regression, and the interface
//   would show a mark the browser tab does not.

const SOURCE_PATH = new URL("../public/favicon.svg", import.meta.url)
const GENERATED_PATH = new URL("../src/components/brand-mark.generated.tsx", import.meta.url)

/** Drop the root svg element — the inline version is sized by CSS, not by the source's width and height. */
function stripRoot(source: string): string {
  const start = source.indexOf("<svg")
  if (start < 0) return source
  const end = source.indexOf(">", start)
  return source.slice(0, start) + source.slice(end + 1)
}

/**
 * ★ The comments go first: a comment may well mention an attribute
 *   (`pathLength="1"` does), and a comment is not part of the drawing.
 */
function stripComments(source: string): string {
  return source
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, "")
    .replace(/^\s*\/\/.*$/gm, "")
}

/** The geometry of every element, as a sorted list — the drawing itself, without the styling. */
function geometry(source: string): string[] {
  const out: string[] = []
  // ★ The leading whitespace matters: without it `stroke-width` would answer to
  //   `width`, and the source's `stroke-width="1.9"` would look like a
  //   geometry the generated file does not have.
  const re = /\s(d|x|y|width|height|r|rx|ry|cx|cy|pathLength|transform)="([^"]*)"/g
  let match: RegExpExecArray | null
  while ((match = re.exec(source)) !== null) out.push(match[1] + "=" + match[2])
  return out.sort()
}

/** The class of every element, as a sorted list — the parts' handles. */
function classes(source: string): string[] {
  const out: string[] = []
  const re = /\bclass(?:Name)?="([^"]*)"/g
  let match: RegExpExecArray | null
  while ((match = re.exec(source)) !== null) out.push(match[1])
  return out.sort()
}

describe("品牌标记的生成", () => {
  test("★ 生成物与源文件同步（漂移检查）", async () => {
    const source = await Bun.file(SOURCE_PATH).text()
    const generated = await Bun.file(GENERATED_PATH).text()
    expect(generateBrandMark(source)).toBe(generated)
  })

  test("★ 生成物的图形与源文件一致", async () => {
    const source = await Bun.file(SOURCE_PATH).text()
    const generated = await Bun.file(GENERATED_PATH).text()
    expect(geometry(stripRoot(stripComments(generated)))).toEqual(
      geometry(stripRoot(stripComments(source))),
    )
  })

  test("★ 部件的 class 一份不少地搬过去了", async () => {
    const source = await Bun.file(SOURCE_PATH).text()
    const generated = await Bun.file(GENERATED_PATH).text()
    expect(classes(stripRoot(stripComments(generated)))).toEqual(
      classes(stripRoot(stripComments(source))),
    )
  })

  test("★ 主体与描边各带一份同样的部件，于是它们一起动", async () => {
    const generated = await Bun.file(GENERATED_PATH).text()
    // two masks × one group of parts
    expect(generated.match(/className="mark-parts"/g)?.length).toBe(2)
    // the beak, the neck, the stem and the bar are in both masks
    for (const part of ["mark-beak", "mark-neck", "mark-stem", "mark-bar"]) {
      expect(generated.match(new RegExp(`className="${part}"`, "g"))?.length).toBe(2)
    }
    // the eye is a hole in the body only
    expect(generated.match(/className="mark-eye"/g)?.length).toBe(1)
    // the trace and the data point sit outside the masks
    expect(generated.match(/className="mark-trace"/g)?.length).toBe(1)
    expect(generated.match(/className="mark-dot"/g)?.length).toBe(2)
  })

  test("★ id 是每个实例一份，不是页面里一份", async () => {
    const generated = await Bun.file(GENERATED_PATH).text()
    // a literal id would be shared by the header's mark and the welcome
    // screen's mark, and the second one would resolve its mask to the
    // first one's
    expect(generated).not.toMatch(/id="openheron/)
    expect(generated).toContain('uid + "-body"')
    expect(generated).toContain('uid + "-halo"')
    expect(generated).toContain('uid + "-brand"')
  })

  test("★ 源文件仍然是一张 favicon：两个 mask、一条走线、一个数据点", async () => {
    const source = await Bun.file(SOURCE_PATH).text()
    expect(source.match(/<mask /g)?.length).toBe(2)
    expect(source).toContain('class="mark-trace"')
    expect(source).toContain('pathLength="1"')
  })
})

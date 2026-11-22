import { describe, expect, test } from "bun:test"
import { readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"

import { resolveLocale, translate, translator } from "../src/lib/i18n"
import { catalog, type MessageKey } from "../src/lib/locales"

const entries = Object.entries(catalog) as [MessageKey, (typeof catalog)[MessageKey]][]

// ★ The whole of `web/src`, read from the disk: the two checks below are
//   about the **source**, not about the catalog alone.
const SRC = join(import.meta.dir, "..", "src")

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === "locales") continue
      out.push(...sourceFiles(path))
    } else if (/\.tsx?$/.test(entry.name)) {
      out.push(path)
    }
  }
  return out
}

const sources = sourceFiles(SRC).map((path) => ({
  path: path.slice(SRC.length + 1),
  text: readFileSync(path, "utf-8"),
}))

// ★ The copy is **one object**: the two languages sit side by side, so they
//   cannot drift apart — there is no second file to forget.
describe("the interface's copy", () => {
  test("★ 每一条都有英文", () => {
    const empty = entries.filter(([, entry]) => !entry.en.trim()).map(([key]) => key)
    expect(empty).toEqual([])
  })

  test("★ 中文没写的那些，落在英文上（而不是空着）", () => {
    const zh = translator("zh")
    const untranslated = entries.filter(([, entry]) => !entry.zh.trim())
    for (const [key, entry] of untranslated) {
      expect(zh(key)).toBe(entry.en)
    }
  })

  test("★ 中英文的占位符一样", () => {
    const holes = (text: string) =>
      [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    for (const [key, entry] of entries) {
      if (!entry.zh.trim()) continue
      expect(holes(entry.zh), `the placeholders of ${key}`).toEqual(holes(entry.en))
    }
  })

  test("★ 键名是一串小写的段", () => {
    const bad = entries
      .map(([key]) => key)
      .filter((key) => !/^[a-z][A-Za-z0-9]*(\.[A-Za-z0-9]+)+$/.test(key))
    expect(bad).toEqual([])
  })

  test("★ 两种语言都有词了", () => {
    // ★ The counter used to be the phase-1 meter: every key is now written
    //   in both languages, so a new English-only key breaks the build.
    const total = entries.length
    const written = entries.filter(([, entry]) => entry.zh.trim()).length
    expect(written).toBe(total)
  })

  test("★ 没有哪条是白写的（目录里没有没人用的键）", () => {
    // ★ A key nothing reads is a sentence that will never be translated, and
    //   a translator will not know it exists. ★ A key can be read by a
    //   **table** — `tool-titles.ts` maps a tool name to `tool.title.ls` —
    //   so the check counts every occurrence of the key, not only the calls.
    const orphans = entries
      .map(([key]) => key)
      .filter((key) => !sources.some((s) => s.text.includes(`"${key}"`)))
    expect(orphans).toEqual([])
  })

  test("★ 界面里没有写死的英文了", () => {
    // ★ The copy lives in the catalog, so a sentence written straight into a
    //   component cannot be translated. ★ The **brand** is not copy: the
    //   product is called openheron in every language.
    const BRAND = new Set(["openheron"])
    // A JSX text node: `>text<`, on one line. ★ A text node is one line in
    //   this tree — a sentence that wraps is written as `{" "}` pieces.
    const TEXT = />([^<>{}]*[A-Za-z][^<>{}]*)</g
    // A string attribute a reader sees: the tooltip, the accessible name,
    // the placeholder, the alternative text.
    const PROP = /\b(?:title|aria-label|placeholder|alt|description)="([^"]{2,})"/g

    const left: string[] = []
    for (const file of sources) {
      if (!file.path.endsWith(".tsx")) continue
      for (const re of [TEXT, PROP]) {
        re.lastIndex = 0
        for (const m of file.text.matchAll(re)) {
          const text = m[1].trim()
          if (!text) continue
          if (BRAND.has(text)) continue
          // ★ Not copy: code, a path, a class name, a key.
          if (!/[a-z]/.test(text)) continue
          if (/[(){}[\]=<>|&;]/.test(text)) continue
          if (/^[A-Za-z0-9_.\-/]+$/.test(text)) continue
          left.push(`${file.path}: ${text}`)
        }
      }
    }
    expect(left).toEqual([])
  })
})

describe("resolveLocale", () => {
  test("★ 存过的偏好压过浏览器", () => {
    expect(resolveLocale("en", "zh-CN")).toBe("en")
    expect(resolveLocale("zh", "en-US")).toBe("zh")
  })

  test("★ 没存过就看浏览器", () => {
    expect(resolveLocale(null, "zh-CN")).toBe("zh")
    expect(resolveLocale(null, "zh")).toBe("zh")
    expect(resolveLocale(null, "en-US")).toBe("en")
    expect(resolveLocale(null, "fr-FR")).toBe("en")
  })

  test("★ 存了看不懂的值就当没存", () => {
    expect(resolveLocale("klingon", "zh-CN")).toBe("zh")
    expect(resolveLocale("", "en-US")).toBe("en")
  })
})

describe("translate", () => {
  test("★ 英文就是目录里的英文", () => {
    expect(translate("en", "header.locale.toEn")).toBe("Switch to English")
  })

  test("★ 占位符被换掉", () => {
    expect(translate("en", "thinking.doneFor", { duration: "3.2s" })).toBe("Thought for 3.2s")
  })

  test("★ 没有的参数就留着占位符", () => {
    expect(translate("en", "thinking.doneFor")).toBe("Thought for {duration}")
  })
})

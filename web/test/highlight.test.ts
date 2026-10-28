import { describe, expect, test } from "bun:test"

import {
  getHighlighter,
  highlightToHtml,
  langOf,
  looksLikeJson,
  SUPPORTED_LANGS,
} from "../src/lib/highlight"

describe("langOf", () => {
  test("从 react-markdown 的 className 里取语言", () => {
    expect(langOf("language-go")).toBe("go")
    expect(langOf("hljs language-python")).toBe("python")
  })

  test("没有语言标签时是 undefined", () => {
    expect(langOf(undefined)).toBeUndefined()
    expect(langOf("")).toBeUndefined()
    expect(langOf("some-other-class")).toBeUndefined()
  })
})

describe("getHighlighter", () => {
  test("并发调用共享同一个实例", async () => {
    const [a, b] = await Promise.all([getHighlighter(), getHighlighter()])
    expect(a).toBe(b)
    expect(a).toBe(await getHighlighter())
  })

  test("覆盖编程 agent 用得上的语言", async () => {
    const hl = await getHighlighter()
    const loaded = hl.getLoadedLanguages()
    for (const l of SUPPORTED_LANGS) {
      expect(loaded).toContain(l)
    }
  })

  test("★ 源码里没有引入 oniguruma（WASM）引擎", async () => {
    const src = await Bun.file(new URL("../src/lib/highlight.ts", import.meta.url)).text()
    expect(src).toMatch(/["']shiki\/engine\/javascript["']/)
    expect(src).not.toContain("engine/oniguruma")
  })

  test("★ shiki 核心是动态 import —— 它不进首屏 chunk", async () => {
    const src = await Bun.file(new URL("../src/lib/highlight.ts", import.meta.url)).text()
    expect(src).not.toMatch(/import\s+(?!type\s)\{[^}]*\}\s*from\s+["']shiki\/core["']/)
    expect(src).not.toMatch(
      /import\s+(?!type\s)\{[^}]*\}\s*from\s+["']shiki\/engine\/javascript["']/,
    )
    expect(src).toMatch(/import\(\s*["']shiki\/core["']\s*\)/)
    expect(src).toMatch(/import\(\s*["']shiki\/engine\/javascript["']\s*\)/)
  })
})

describe("highlightToHtml", () => {
  test("高亮出带颜色的 span", async () => {
    const html = await highlightToHtml('package main\n\nfunc main() {}', "go")
    expect(html).toBeTruthy()
    expect(html).toContain("<span")
    expect(html).toContain("--shiki-light")
  })

  test("双主题都输出，供 CSS 变量切换", async () => {
    const html = (await highlightToHtml("const a = 1", "typescript")) ?? ""
    expect(html).toContain("--shiki-light")
    expect(html).toContain("--shiki-dark")
  })

  test("★ 未知语言返回 null，调用方据此退回纯文本", async () => {
    expect(await highlightToHtml("whatever", "brainfuck")).toBeNull()
    expect(await highlightToHtml("whatever", undefined)).toBeNull()
  })

  test("★ 高亮失败不抛错", async () => {
    const nasty = "func\x00main() { \"\\u0000\" \t /* 注释 */ }\n".repeat(50)
    const html = await highlightToHtml(nasty, "go")
    expect(html === null || typeof html === "string").toBe(true)
  })

  test("★ 代码里的 HTML 被转义，不会变成标签", async () => {
    const html = (await highlightToHtml('<img src=x onerror=alert(1)>', "html")) ?? ""
    expect(html).not.toContain("<img")
  })
})

describe("looksLikeJson", () => {
  test("认得出真 JSON", () => {
    expect(looksLikeJson('{"a":1}')).toBe(true)
    expect(looksLikeJson("[1,2]")).toBe(true)
    expect(looksLikeJson("  {\"a\":1}  ")).toBe(true)
    expect(looksLikeJson("{}")).toBe(true)
    expect(looksLikeJson("[]")).toBe(true)
  })

  test("普通文本不是 JSON", () => {
    expect(looksLikeJson("package main\n\nfunc main() {}")).toBe(false)
    expect(looksLikeJson("a.ts\nb.ts\n")).toBe(false)
  })

  test("★ 坏掉的 JSON 不算", () => {
    expect(looksLikeJson('{"a":1')).toBe(false)
    expect(looksLikeJson("{not json}")).toBe(false)
  })

  test("标量不算 —— 上色会误导", () => {
    expect(looksLikeJson("123")).toBe(false)
    expect(looksLikeJson('"a string"')).toBe(false)
    expect(looksLikeJson("null")).toBe(false)
  })

  test("空输入不算", () => {
    expect(looksLikeJson("")).toBe(false)
    expect(looksLikeJson("   \n ")).toBe(false)
  })
})

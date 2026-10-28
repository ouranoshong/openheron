import { describe, expect, test } from "bun:test"

import {
  getHighlighter,
  highlightToHtml,
  langOf,
  looksLikeJson,
  SUPPORTED_LANGS,
} from "../src/lib/highlight"

/**
 * 语法高亮的契约测试。
 *
 * ★ 高亮失败必须退回 null 而不是抛错 —— 调用方据此显示纯文本。
 *   「代码块整个消失」比「代码块没有颜色」糟得多。
 */

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
    // 建两次引擎会白烧一份 CPU 与内存，而一条消息里常同时有好几个代码块。
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
    // oniguruma 会多带一个 wasm 文件并首次高亮时异步加载它；对这个体量的
    // 应用不值当，也和「高亮器失败就退回纯文本」的兜底相冲突 ——
    // wasm 加载失败时走的就是那条兜底路径，等于高亮永远不生效。
    //
    // 这条断言查的是**源码**，而不是引擎内部字段：shiki 没把 engine
    // 暴露在 highlighter 上，扒内部既脆又没有意义。真正要保证的是
    // 「产物里没有 WASM 依赖」，而它的唯一来源就是这行 import。
    //
    // ★ 断言写成「引用了 JS 引擎」而不是「静态 import 了 JS 引擎」：
    //   2026-10-23 起它改成了动态 import（见下一条用例），静态形式已不存在。
    const src = await Bun.file(new URL("../src/lib/highlight.ts", import.meta.url)).text()
    expect(src).toMatch(/["']shiki\/engine\/javascript["']/)
    expect(src).not.toContain("engine/oniguruma")
  })

  test("★ shiki 核心是动态 import —— 它不进首屏 chunk", async () => {
    // 「懒建引擎」只省了 CPU，**没省下载**：静态 import 会把 shiki 核心与
    // JS 正则引擎（≈300KB）打进首屏 chunk，于是每个用户都要为「也许永远
    // 看不到的代码块」下载它。动态 import 后它进独立 chunk，只有真的渲染
    // 出代码块才下载。
    //
    // ★ 断言查源码的 import 形态，而不是产物：产物断言要跑一次 build，
    //   单元测试里做不起（而且 dist/ 不一定存在）。
    const src = await Bun.file(new URL("../src/lib/highlight.ts", import.meta.url)).text()
    // 静态 import 的形态是 `import { … } from "shiki/core"`；动态的是
    // `import("shiki/core")`。
    // ★ `import type { … } from "shiki/core"` **不算** —— 类型在编译期就被
    //   抹掉，不产生任何运行时代码，也就进不了产物。
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
    // defaultColor: false → 颜色走 CSS 变量，不是 color 属性
    expect(html).toContain("--shiki-light")
  })

  test("双主题都输出，供 CSS 变量切换", async () => {
    const html = (await highlightToHtml("const a = 1", "typescript")) ?? ""
    // defaultColor: false → 两套颜色都用 CSS 变量表达
    expect(html).toContain("--shiki-light")
    expect(html).toContain("--shiki-dark")
  })

  test("★ 未知语言返回 null，调用方据此退回纯文本", async () => {
    expect(await highlightToHtml("whatever", "brainfuck")).toBeNull()
    expect(await highlightToHtml("whatever", undefined)).toBeNull()
  })

  test("★ 高亮失败不抛错", async () => {
    // 内容本身不该有影响：关键字、字符串、控制字符混在一起
    const nasty = "func\x00main() { \"\\u0000\" \t /* 注释 */ }\n".repeat(50)
    const html = await highlightToHtml(nasty, "go")
    // 要么高亮成功要么 null，绝不是异常
    expect(html === null || typeof html === "string").toBe(true)
  })

  test("★ 代码里的 HTML 被转义，不会变成标签", async () => {
    // shiki 的输出是它自己的 span 树，代码文本里的标签必须变成实体 ——
    // 这是 dangerouslySetInnerHTML 那一处唯一的安全前提。
    const html = (await highlightToHtml('<img src=x onerror=alert(1)>', "html")) ?? ""
    expect(html).not.toContain("<img")
  })
})

describe("looksLikeJson", () => {
  /**
   * ★ 工具结果**大多数不是 JSON** —— read_file 返回文件内容、execute 返回
   *   命令输出、ls 返回文件名列表。把它们按 JSON 着色只会得到一片误导性的
   *   颜色（字符串键、逗号都染上色，读起来像坏了）。
   */
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
    // 截断的流式输出在轮次中途就是这个状态，不能当 JSON 上色
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

import type { HighlighterCore } from "shiki/core"

/**
 * 语法高亮。
 *
 * ★ 为什么是 `shiki/core` + 精选语言，而不是 `import { codeToHtml } from "shiki"`：
 *   全量包带所有语言与主题，raw 体积数 MB。这个项目用得上的语言就 8 个，
 *   带上 JS 正则引擎（不用 oniguruma 的 WASM）之后约 300–500KB。
 *
 *   对编程 agent 来说代码块观感重要，但为一个高亮引入几 MB 不划算。
 *
 * ★ 为什么高亮器是懒的：
 *   建引擎要几十到几百毫秒。而**大多数对话里没有代码块** ——
 *   首屏就为看不见的东西付这个代价不合理。所以到第一次真的遇到代码块
 *   才开始建，之后复用同一个实例。
 *
 * ★ ★ 为什么 `shiki/core` 与正则引擎是**动态 import**（2026-10-23）：
 *   「懒建引擎」只省了 CPU，**没省下载** —— 静态 import 会把 shiki 核心与
 *   JS 正则引擎（≈300KB）打进**首屏 chunk**，于是每个用户都要为「也许永远
 *   看不到的代码块」下载它。改成动态 import 后它进独立的 chunk，
 *   只有真的渲染出代码块才下载。
 *   类型（`HighlighterCore`）仍用静态 `import type` —— 它在编译期被抹掉，
 *   不产生任何运行时代码。
 *
 * ★ 明暗双主题走 CSS 变量：
 *   `defaultColor: false` 让 shiki 同时输出两套颜色的 span，CSS 决定用哪套。
 *   这样切换主题不需要重新高亮，也不需要在 JS 里判断当前主题。
 */

const LANGS = [
  "typescript",
  "tsx",
  "javascript",
  "jsx",
  "json",
  "go",
  "python",
  "bash",
  "yaml",
  "diff",
] as const

let highlighter: Promise<HighlighterCore> | null = null

/**
 * getHighlighter 返回共享的高亮器实例。
 *
 * 并发调用共享同一个 Promise —— 建两次引擎会白烧一份 CPU 与内存，
 * 而代码块在一条消息里经常同时出现好几个。
 */
export function getHighlighter(): Promise<HighlighterCore> {
  highlighter ??= (async () => {
    // ★ 两个 import 都要动态：静态的话 shiki 核心会进首屏 chunk。
    //   并行加载，因为它们互不依赖。
    const [{ createHighlighterCore }, { createJavaScriptRegexEngine }] = await Promise.all([
      import("shiki/core"),
      import("shiki/engine/javascript"),
    ])
    return createHighlighterCore({
      themes: [
        import("@shikijs/themes/github-light"),
        import("@shikijs/themes/github-dark"),
      ],
      langs: [
        import("@shikijs/langs/typescript"),
        import("@shikijs/langs/tsx"),
        import("@shikijs/langs/javascript"),
        import("@shikijs/langs/jsx"),
        import("@shikijs/langs/json"),
        import("@shikijs/langs/go"),
        import("@shikijs/langs/python"),
        import("@shikijs/langs/bash"),
        import("@shikijs/langs/yaml"),
        import("@shikijs/langs/diff"),
      ],
      engine: createJavaScriptRegexEngine(),
    })
  })().catch((err) => {
    // ★ 失败要能重试：把缓存清掉。
    //   动态 import 会把「加载 chunk 失败」变成一个**被缓存的 rejected promise**
    //   —— 不清的话，一次网络抖动就让高亮在整个页面生命周期里永久失效。
    //   （改成动态之前这里是同步 throw，缓存不会被写进去。）
    highlighter = null
    throw err
  })
  return highlighter
}

/** 高亮器的语言集合，用于判断某段代码能不能高亮。 */
export const SUPPORTED_LANGS: ReadonlySet<string> = new Set<string>(LANGS)

/** 从 className 里取出语言标记：react-markdown 给的是 `language-go`。 */
export function langOf(className: string | undefined): string | undefined {
  const m = /(?:^|\s)language-([\w-]+)/.exec(className ?? "")
  return m?.[1]
}

/**
 * highlightToHtml 把代码高亮成**一串 span**。
 *
 * ★ ★ `structure: "inline"` 是必须的，不是风格选择：
 *   默认的 "classic" 返回的是完整的 `<pre class="shiki">…</pre>`，
 *   而调用方已经有自己的 `<pre>`（见 markdown.tsx 的 BlockCode），
 *   直接注入会得到 `<pre>` 套 `<pre>` —— 浏览器会把内层那个当文本，
 *   表现为代码块塌成一坨。
 *
 * ★ 高亮器还没就绪、或语言不认识时返回 null —— 由调用方退回纯文本块。
 *   **绝不能让高亮失败把代码块整个吞掉**：那比不高亮糟得多。
 *
 * ★ `defaultColor: false` → 两套颜色都用 CSS 变量（`--shiki-light` /
 *   `--shiki-dark`）表达，主题切换不需要重新高亮。
 */
export async function highlightToHtml(
  code: string,
  lang: string | undefined,
): Promise<string | null> {
  if (!lang || !SUPPORTED_LANGS.has(lang)) return null
  try {
    const hl = await getHighlighter()
    return hl.codeToHtml(code, {
      lang,
      themes: { light: "github-light", dark: "github-dark" },
      defaultColor: false,
      structure: "inline",
    })
  } catch {
    return null
  }
}

/**
 * looksLikeJson 判断一段文本是否**真的**是 JSON。
 *
 * ★ 给工具结果用：参数一定是 JSON，但结果不是 —— read_file 返回文件内容、
 *   execute 返回命令输出、ls 返回文件名列表。把它们当 JSON 高亮只会得到
 *   一片误导性的颜色（字符串键、逗号全染上色，读起来像坏了）。
 *
 * 判据：首字符是 `{` 或 `[`，且能解析成对象或数组。标量不算 ——
 * `123`、`"a string"`、`null` 上色同样会误导。
 */
export function looksLikeJson(text: string): boolean {
  const t = text.trim()
  if (!t || (t[0] !== "{" && t[0] !== "[")) return false
  try {
    const v = JSON.parse(t) as unknown
    return typeof v === "object" && v !== null
  } catch {
    return false
  }
}

import type { HighlighterCore } from "shiki/core"

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

export function getHighlighter(): Promise<HighlighterCore> {
  highlighter ??= (async () => {
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
    highlighter = null
    throw err
  })
  return highlighter
}

export const SUPPORTED_LANGS: ReadonlySet<string> = new Set<string>(LANGS)

export function langOf(className: string | undefined): string | undefined {
  const m = /(?:^|\s)language-([\w-]+)/.exec(className ?? "")
  return m?.[1]
}

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

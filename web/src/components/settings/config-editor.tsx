/**
 * 配置文件的 CodeMirror 6 编辑器。
 *
 * ★ 为什么是 CodeMirror 而不是 Monaco：
 *
 *   - `@codemirror/lang-yaml` 导出 `yamlFrontmatter`，对「带 YAML
 *     frontmatter 的文档」给两种语言各配各的高亮。`agents/*.md` 正是这个
 *     形状；Monaco 的 markdown 模式把 frontmatter 当正文，没有任何对应物。
 *   - 体积：CM6 逐包 tree-shake，Monaco 核心 ~800KB gzip 且不可 tree-shake。
 *     本项目既有纪律是「精选语言 + 懒加载」（见 highlight.ts 的注释），
 *     Monaco 只能整块关进 React.lazy。
 *   - 无 CDN、无 worker。monaco-react 默认从 jsDelivr 拉 monaco，而 openheron
 *     是本地单文件工具 —— 设置编辑器不能要求联网。
 *
 * ★ 它**永远不是真相源**：受控组件，状态在 settings-store。编辑器只管
 *   打字体验，校验一律走 lib/settings-validate.ts 与服务端试合并 ——
 *   那两处都不依赖这个组件，所以 bun test 仍能覆盖全部逻辑。
 */

import { useMemo } from "react"

import CodeMirror, { type ReactCodeMirrorRef } from "@uiw/react-codemirror"
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands"
import { EditorState, type Extension } from "@codemirror/state"
import { searchKeymap, highlightSelectionMatches } from "@codemirror/search"
import { yaml, yamlFrontmatter } from "@codemirror/lang-yaml"
import { markdown } from "@codemirror/lang-markdown"
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from "@codemirror/view"
import { HighlightStyle, syntaxHighlighting, indentUnit } from "@codemirror/language"
import { tags } from "@lezer/highlight"

import { cn } from "@/lib/utils"

/**
 * theTheme 把 openheron 的 CSS 变量接到 CodeMirror 上。
 *
 * ★ 必须显式声明：CodeMirror 不读 CSS 变量，它只认自己那份 token 规则。
 *   而 `highlight.ts` 的 defaultColor:false 那套（shiki 出两套颜色、
 *   CSS 决定用哪套）在编辑器里不成立 —— 编辑器是自己绘制的。
 *   暗色主题因此在这里**必须**有一份对应的 token 表，见 E2E「暗色主题下用
 *   另一套颜色」那条用例的同类问题。
 */
function theTheme(dark: boolean) {
  return EditorView.theme(
    {
      "&": { color: "var(--color-foreground)", backgroundColor: "transparent", height: "100%" },
      ".cm-content": { caretColor: "var(--color-foreground)", fontFamily: "var(--font-mono, monospace)" },
      ".cm-gutters": { backgroundColor: "transparent", color: "var(--color-muted-foreground)", border: "none" },
      ".cm-activeLine": { backgroundColor: dark ? "rgb(255 255 255 / 0.04)" : "rgb(0 0 0 / 0.04)" },
      ".cm-activeLineGutter": { backgroundColor: "transparent" },
      ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
        backgroundColor: dark ? "rgb(255 255 255 / 0.14)" : "rgb(0 0 0 / 0.10)",
      },
      ".cm-cursor": { borderLeftColor: "var(--color-foreground)" },
    },
    { dark },
  )
}

/** theHighlight 是随主题切换的 token 配色。 */
const theHighlight = (dark: boolean) =>
  HighlightStyle.define(
    [
      { tag: tags.comment, color: dark ? "#7d8590" : "#6e7781" },
      { tag: [tags.keyword, tags.modifier], color: dark ? "#ff7b72" : "#cf222e" },
      { tag: [tags.string, tags.special(tags.string)], color: dark ? "#a5d6ff" : "#0a3069" },
      { tag: [tags.number, tags.bool, tags.null], color: dark ? "#79c0ff" : "#0550ae" },
      { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: dark ? "#7ee787" : "#116329" },
      { tag: [tags.typeName, tags.className], color: dark ? "#d2a8ff" : "#8250df" },
      { tag: tags.meta, color: dark ? "#ffa657" : "#953800" },
    ],
    { themeType: dark ? "dark" : "light" },
  )

export interface ConfigEditorProps {
  path: string
  value: string
  onChange: (value: string) => void
  readOnly?: boolean
  dark?: boolean
}

/** sectionOf 从相对路径取分区：agents/main.md → "agents"。 */
function sectionOf(path: string): string {
  const i = path.indexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}

export function ConfigEditor({ path, value, onChange, readOnly = false, dark = false }: ConfigEditorProps) {
  const ref = useMemo(() => ({ current: null as ReactCodeMirrorRef | null }), [])

  const extensions = useMemo(() => {
    const section = sectionOf(path)
    // ★ agents/*.md 的正文是**系统提示词**，frontmatter 是配置 ——
    //   yamlFrontmatter 让两者各走各的语法，而不是把 8 行 YAML 当正文。
    //   纯 YAML 文件走 yaml()，不需要 frontmatter 包装。
    const lang: Extension[] =
      section === "agents"
        ? [yamlFrontmatter({ content: markdown() })]
        : section === "llm" || section === "policy"
          ? [yaml()]
          : []

    return [
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      highlightSelectionMatches(),
      history(),
      indentUnit.of("  "), // ★ yaml/md 惯例是两空格
      syntaxHighlighting(theHighlight(dark)),
      theTheme(dark),
      keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap]),
      ...lang,
      EditorView.lineWrapping,
      EditorState.readOnly.of(readOnly),
      EditorView.editable.of(!readOnly),
    ]
  }, [path, dark, readOnly])

  return (
    <div
      className="min-h-0 flex-1 overflow-hidden"
      // ★ data-testid 给 E2E 一个稳定钩子。语义定位（role / aria）在编辑器里
      //   不好用 —— CodeMirror 自己渲染 contenteditable，类名由它自己定。
      data-testid="config-editor"
    >
      <CodeMirror
        ref={ref}
        value={value}
        onChange={onChange}
        extensions={extensions}
        editable={!readOnly}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLine: true,
          highlightSelectionMatches: true,
          autocompletion: false, // ★ schema 补全是阶段二；开了没 schema 只会给噪音
          searchKeymap: true,
        }}
        height="100%"
        className={cn("h-full text-sm")}
      />
    </div>
  )
}
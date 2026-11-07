
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

function sectionOf(path: string): string {
  const i = path.indexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}

export function ConfigEditor({ path, value, onChange, readOnly = false, dark = false }: ConfigEditorProps) {
  const ref = useMemo(() => ({ current: null as ReactCodeMirrorRef | null }), [])

  const extensions = useMemo(() => {
    const section = sectionOf(path)
    const lang: Extension[] =
      section === "agents"
        ? [yamlFrontmatter({ content: markdown() })]
        : section === "llm"
          ? [yaml()]
          : []

    return [
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      highlightSelectionMatches(),
      history(),
      indentUnit.of("  "),
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
          autocompletion: false,
          searchKeymap: true,
        }}
        height="100%"
        className={cn("h-full text-sm")}
      />
    </div>
  )
}
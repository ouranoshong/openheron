// file-kind classifies a file for the interface.
//
// ★ This module is **pure**: it imports no framework and no component, so
//   `bun test` can prove it right on its own. The icon it names is a
//   **string**, not a React component — the component that draws the icon
//   lives in the component layer, and this file stays testable.
//
// ★ The classification answers one question: "how should this file be
//   looked at?" A picture is a picture, a PDF is a PDF, and a `.md` is
//   Markdown — and each of them has its own way of being shown.

import { SUPPORTED_LANGS } from "@/lib/highlight"

export type FileKind = "image" | "pdf" | "markdown" | "code" | "text" | "other"

export interface FileClass {
  kind: FileKind
  // icon is the **name** of a lucide icon, without the "Icon" suffix.
  icon: string
  // label is the English name of the kind. It is interface copy, so it is
  // English — the interface speaks English everywhere.
  label: string
}

// ★ The image types the server will serve inline.
//
//	SVG is deliberately **not** one of them: an SVG can carry a script, and
//	a browser renders it as a document — so the server serves it as a
//	download, and the interface treats it as a file, not a picture.
const IMAGE_MIMES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"])

const IMAGE_EXT = new Set(["png", "jpg", "jpeg", "gif", "webp"])

const MARKDOWN_EXT = new Set(["md", "markdown", "mdx"])

const TEXT_EXT = new Set([
  "txt",
  "text",
  "log",
  "csv",
  "tsv",
  "rst",
  "env",
  "conf",
  "cfg",
  "properties",
])

// ★ The languages the code block knows how to highlight.
//
//	A language that is not on this list is still **code** — it is simply
//	shown as plain text, because a highlighter that does not know the
//	language is worse than none.
const CODE_LANG_BY_EXT: Record<string, string> = {
  ts: "typescript",
  tsx: "tsx",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "jsx",
  json: "json",
  jsonc: "json",
  go: "go",
  py: "python",
  rb: "ruby",
  rs: "rust",
  java: "java",
  kt: "kotlin",
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  hpp: "cpp",
  cs: "csharp",
  php: "php",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  yml: "yaml",
  yaml: "yaml",
  toml: "toml",
  ini: "ini",
  css: "css",
  scss: "css",
  html: "html",
  htm: "html",
  xml: "xml",
  sql: "sql",
  diff: "diff",
  patch: "diff",
  graphql: "graphql",
}

// ★ The mime types that mean "this is source code" even when the name
//   says nothing useful.
const CODE_MIMES = new Set([
  "application/json",
  "application/ld+json",
  "application/xml",
  "application/yaml",
  "application/x-yaml",
  "application/javascript",
  "application/x-javascript",
  "application/x-ndjson",
  "application/toml",
])

// fileExt returns the lowercase extension of a name, without the dot.
//
// ★ A leading dot is not an extension: ".gitignore" is a name, and
//   splitting there would leave an empty extension and a file that looks
//   like it has no type at all.
export function fileExt(name: string): string {
  const base = name.slice(Math.max(name.lastIndexOf("/"), name.lastIndexOf("\\")) + 1)
  const idx = base.lastIndexOf(".")
  if (idx <= 0 || idx === base.length - 1) return ""
  return base.slice(idx + 1).toLowerCase()
}

// ★ The mime the **server** derived from the name.
//
//	★ The server's answer wins over the client's: the client may call a
//	  text file "image/png", and the model would then answer about a
//	  picture it never saw.
//
//	★ "application/octet-stream" is the server's "I do not know" — it
//	  carries no information, so the extension gets to decide. That is not
//	  the client talking; it is the only signal left.
export function effectiveMime(name: string, mime?: string): string {
  const m = (mime ?? "").trim().toLowerCase()
  if (m && m !== "application/octet-stream") return m
  const ext = fileExt(name)
  switch (ext) {
    case "png":
      return "image/png"
    case "jpg":
    case "jpeg":
      return "image/jpeg"
    case "gif":
      return "image/gif"
    case "webp":
      return "image/webp"
    case "pdf":
      return "application/pdf"
    case "md":
    case "markdown":
      return "text/markdown"
    case "txt":
      return "text/plain"
    case "csv":
      return "text/csv"
    case "json":
      return "application/json"
    case "xml":
      return "application/xml"
    case "yml":
    case "yaml":
      return "application/yaml"
    case "html":
    case "htm":
      return "text/html"
    case "svg":
      return "image/svg+xml"
    default:
      return m || "application/octet-stream"
  }
}

// isImage reports whether the file may be shown as a picture.
//
// ★ SVG is not a picture here, even though its mime says "image": a
//   browser renders an SVG as a document, and an SVG can carry a script.
export function isImage(name: string, mime?: string): boolean {
  return IMAGE_MIMES.has(effectiveMime(name, mime))
}

// isPdf reports whether the file is a PDF.
export function isPdf(name: string, mime?: string): boolean {
  return effectiveMime(name, mime) === "application/pdf"
}

// isTextual reports whether the file may be shown as text.
//
// ★ "text/html" counts: the **server** serves it as text/plain, so the
//   browser only ever sees characters. The interface renders it itself.
export function isTextual(name: string, mime?: string): boolean {
  const m = effectiveMime(name, mime)
  if (m.startsWith("text/")) return true
  return CODE_MIMES.has(m)
}

// fileKind classifies a file.
//
// ★ The mime comes first, the extension second — and the one exception is
//   documented in [effectiveMime]: "application/octet-stream" means the
//   server had nothing to say, so the extension decides.
export function fileKind(name: string, mime?: string): FileKind {
  const m = effectiveMime(name, mime)
  if (IMAGE_MIMES.has(m)) return "image"
  if (m === "application/pdf") return "pdf"
  if (m === "text/markdown" || m === "text/x-markdown") return "markdown"
  if (CODE_MIMES.has(m)) return "code"
  const ext = fileExt(name)
  if (IMAGE_EXT.has(ext)) return "image"
  if (ext === "pdf") return "pdf"
  if (MARKDOWN_EXT.has(ext)) return "markdown"
  if (ext in CODE_LANG_BY_EXT) return "code"
  if (TEXT_EXT.has(ext)) return "text"
  if (m.startsWith("text/")) return "text"
  return "other"
}

// fileLang returns the language a code block should be highlighted as.
//
// ★ It returns undefined for a language the highlighter does not know —
//   the caller then shows the text as it is, which is honest, where a
//   guessed language would be wrong.
export function fileLang(name: string, mime?: string): string | undefined {
  const ext = fileExt(name)
  const lang = CODE_LANG_BY_EXT[ext] ?? LANG_BY_MIME[effectiveMime(name, mime)]
  if (!lang) return undefined
  return SUPPORTED_LANGS.has(lang) ? lang : undefined
}

// ★ The languages the mime speaks of.
//
//	★ A `.config` file that the server called "application/yaml" is YAML,
//	  even though its name says nothing — the mime is the server's own
//	  reading of the file, and it knows the type better than the name does.
const LANG_BY_MIME: Record<string, string> = {
  "application/json": "json",
  "application/ld+json": "json",
  "application/xml": "xml",
  "application/yaml": "yaml",
  "application/x-yaml": "yaml",
  "application/javascript": "javascript",
  "application/x-javascript": "javascript",
}

// fileClass answers "what is this file, and how should it be shown".
export function fileClass(name: string, mime?: string): FileClass {
  const kind = fileKind(name, mime)
  const ext = fileExt(name)
  switch (kind) {
    case "image":
      return { kind, icon: "file-image", label: "Image" }
    case "pdf":
      return { kind, icon: "file-type", label: "PDF" }
    case "markdown":
      return { kind, icon: "file-text", label: "Markdown" }
    case "code":
      return { kind, icon: "file-code", label: "Code" }
    case "text":
      if (ext === "csv" || ext === "tsv") {
        return { kind, icon: "file-spreadsheet", label: "Table" }
      }
      return { kind, icon: "file-text", label: "Text" }
    default:
      if (ext === "zip" || ext === "tar" || ext === "gz" || ext === "tgz" || ext === "7z") {
        return { kind, icon: "file-archive", label: "Archive" }
      }
      return { kind, icon: "file", label: "File" }
  }
}

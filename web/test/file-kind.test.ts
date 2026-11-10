import { describe, expect, test } from "bun:test"

import {
  effectiveMime,
  fileClass,
  fileExt,
  fileKind,
  fileLang,
  isImage,
  isPdf,
  isTextual,
} from "../src/lib/file-kind"

describe("fileExt", () => {
  test("取扩展名并转成小写", () => {
    expect(fileExt("Shot.PNG")).toBe("png")
    expect(fileExt("archive.tar.gz")).toBe("gz")
  })

  test("没有扩展名时是空", () => {
    expect(fileExt("README")).toBe("")
  })

  test("前导的点不是扩展名", () => {
    expect(fileExt(".gitignore")).toBe("")
  })

  test("目录里的名字只看最后一段", () => {
    expect(fileExt("a/b/notes.md")).toBe("md")
    expect(fileExt("a\\b\\notes.md")).toBe("md")
  })
})

describe("effectiveMime", () => {
  test("服务端的 mime 说了算", () => {
    expect(effectiveMime("notes.md", "text/markdown")).toBe("text/markdown")
  })

  test("octet-stream 是「不知道」，于是看扩展名", () => {
    expect(effectiveMime("notes.md", "application/octet-stream")).toBe("text/markdown")
  })

  test("没有 mime 时看扩展名", () => {
    expect(effectiveMime("shot.png")).toBe("image/png")
  })

  test("真的什么都没有就是 octet-stream", () => {
    expect(effectiveMime("mystery")).toBe("application/octet-stream")
  })
})

describe("fileKind", () => {
  test("图片按图片归类", () => {
    expect(fileKind("shot.png", "image/png")).toBe("image")
    expect(fileKind("shot.jpg", "image/jpeg")).toBe("image")
    expect(fileKind("anim.gif", "image/gif")).toBe("image")
  })

  test("★ SVG 不是图片", () => {
    // SVG can carry a script, and a browser renders it as a document —
    // so the server serves it as a download, and so does the interface.
    expect(fileKind("logo.svg", "image/svg+xml")).not.toBe("image")
  })

  test("PDF 单独归类", () => {
    expect(fileKind("doc.pdf", "application/pdf")).toBe("pdf")
  })

  test("Markdown 单独归类", () => {
    expect(fileKind("notes.md", "text/markdown")).toBe("markdown")
  })

  test("代码按代码归类", () => {
    expect(fileKind("main.go", "application/octet-stream")).toBe("code")
    expect(fileKind("data.json", "application/json")).toBe("code")
  })

  test("普通文本归类为文本", () => {
    expect(fileKind("readme.txt", "text/plain")).toBe("text")
    expect(fileKind("table.csv", "text/csv")).toBe("text")
  })

  test("认不出的归到「其他」", () => {
    expect(fileKind("bundle.zip", "application/zip")).toBe("other")
    expect(fileKind("mystery", "application/octet-stream")).toBe("other")
  })

  test("★ mime 优先于扩展名", () => {
    // The server says it is a picture; the extension is not.
    expect(fileKind("shot.txt", "image/png")).toBe("image")
  })
})

describe("fileLang", () => {
  test("认得的语言给出来", () => {
    expect(fileLang("main.go")).toBe("go")
    expect(fileLang("app.tsx")).toBe("tsx")
    expect(fileLang("data.json")).toBe("json")
  })

  test("★ 高亮器不认的语言是 undefined", () => {
    // A guessed language is worse than none: it colours the wrong words.
    expect(fileLang("page.html")).toBeUndefined()
    expect(fileLang("notes.md")).toBeUndefined()
  })
})

describe("isImage / isPdf / isTextual", () => {
  test("图片只有那四种", () => {
    expect(isImage("shot.png", "image/png")).toBe(true)
    expect(isImage("logo.svg", "image/svg+xml")).toBe(false)
    expect(isImage("notes.md", "text/markdown")).toBe(false)
  })

  test("PDF", () => {
    expect(isPdf("doc.pdf", "application/pdf")).toBe(true)
    expect(isPdf("notes.md", "text/markdown")).toBe(false)
  })

  test("★ html 也算文本——服务端按 text/plain 返回", () => {
    expect(isTextual("page.html", "text/html")).toBe(true)
    expect(isTextual("bundle.zip", "application/zip")).toBe(false)
  })
})

describe("fileClass", () => {
  test("每一种都有自己的图标与名字", () => {
    expect(fileClass("shot.png", "image/png")).toEqual({
      kind: "image",
      icon: "file-image",
      label: "Image",
    })
    expect(fileClass("doc.pdf", "application/pdf").label).toBe("PDF")
    expect(fileClass("notes.md", "text/markdown").label).toBe("Markdown")
    expect(fileClass("main.go").label).toBe("Code")
    expect(fileClass("readme.txt", "text/plain").label).toBe("Text")
    expect(fileClass("bundle.zip", "application/zip").label).toBe("Archive")
    expect(fileClass("mystery", "application/octet-stream").label).toBe("File")
  })

  test("表格有自己的图标", () => {
    expect(fileClass("table.csv", "text/csv").icon).toBe("file-spreadsheet")
  })
})

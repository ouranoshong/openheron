import { expect, test, type Page } from "@playwright/test"

import {
  COMPOSER,
  MARKER_A,
  openApp,
  openThread,
  resetBackend,
  sendMessage,
} from "./fixtures"

// The two kinds of chip.
//
// ★ They are different on purpose: a file in the composer is **pending** —
//   it has not gone out with a question yet — and a file in a message is
//   **history**. A spec that counted the wrong one would pass without ever
//   looking at the thing it claims to test.
const PENDING_CHIP = '[data-testid="attachment-chip"]'
const MESSAGE_CHIP = '[data-testid="message-attachment"]'
const THUMB = '[data-testid="attachment-thumb"]'
const PREVIEW = '[data-testid="file-preview"]'

// ★ A real picture, one pixel wide.
//
//	The bytes have to decode: a chip that shows a thumbnail of something
//	that is not a picture would fall back to its icon, and the test would
//	pass without ever drawing a picture.
const PNG_1X1 = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
)

test.beforeEach(async () => {
  await resetBackend()
})

// runBodies collects the bodies of the run requests.
function runBodies(page: Page): unknown[] {
  const seen: unknown[] = []
  page.on("request", (r) => {
    if (r.method() !== "POST") return
    if (!new URL(r.url()).pathname.endsWith("/v2/run")) return
    const raw = r.postData()
    if (!raw) return
    try {
      seen.push(JSON.parse(raw))
    } catch {
      // A body that will not parse is a body worth failing on, not one to
      // skip silently — but the assertion says more than this catch does.
    }
  })
  return seen
}

// dropFile drops a file onto the composer.
//
// ★ The event has to be dispatched by hand: a real drop carries a
//   DataTransfer that no test driver will produce for us.
async function dropFile(page: Page, name: string, mime = "text/plain"): Promise<void> {
  await page.evaluate(
    ([fileName, fileMime]) => {
      const dt = new DataTransfer()
      dt.items.add(new File(["dropped body"], fileName, { type: fileMime }))
      const el = document.querySelector('[data-testid="composer-box"]')
      if (!el) throw new Error("no composer box")
      el.dispatchEvent(
        new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true }),
      )
    },
    [name, mime],
  )
}

// pasteFile pastes a file into the composer.
async function pasteFile(page: Page, name: string, mime = "image/png"): Promise<void> {
  await page.evaluate(
    ([fileName, fileMime]) => {
      const dt = new DataTransfer()
      dt.items.add(new File(["pasted body"], fileName, { type: fileMime }))
      const el = document.querySelector('[aria-label="Message input"]')
      if (!el) throw new Error("no composer")
      el.dispatchEvent(
        new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }),
      )
    },
    [name, mime],
  )
}

test.describe("附件随消息走", () => {
  test("★ 发出的那一轮带着附件", async ({ page }) => {
    const bodies = runBodies(page)
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("hello") },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} look at the attachment`)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    expect(bodies.length).toBeGreaterThan(0)
    const body = bodies[0] as { attachments?: { name: string; path: string }[] }
    expect(body.attachments ?? [], "the run carried no attachments").toHaveLength(1)
    expect(body.attachments?.[0]?.name).toBe("notes.md")
    // ★ The path is relative to the workspace: it is what the server
    //   resolves, and an absolute path is a fact about one machine. It
    //   also carries a hash, so a second upload of the same name does not
    //   overwrite this one.
    expect(body.attachments?.[0]?.path).toMatch(/^notes-[0-9a-f]{8}\.md$/)
  })

  test("★ 乐观气泡里就画着附件", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "shot.png", mimeType: "image/png", buffer: Buffer.from("not really a png") },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} here is a picture`)

    // ★ The user has to see what they sent, at the moment they sent it.
    await expect(page.locator(MESSAGE_CHIP)).toHaveCount(1)
    await expect(page.locator(MESSAGE_CHIP).first()).toContainText("shot.png")
  })

  test("★ 发完待发清单就空了", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "once.txt", mimeType: "text/plain", buffer: Buffer.from("x") },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} first question`)

    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })
    // ★ Leaving the file in the list would send it again with the next
    //   question — the user asked once.
    await expect(page.locator(PENDING_CHIP)).toHaveCount(0)
    await expect(page.locator(MESSAGE_CHIP)).toHaveCount(1)
  })

  test("★ 刷新之后历史里的附件还在", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "kept.md", mimeType: "text/markdown", buffer: Buffer.from("hello") },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} keep this file`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    await page.reload()
    await expect(page.locator(MESSAGE_CHIP).first()).toBeVisible({ timeout: 7000 })
    await expect(page.locator(MESSAGE_CHIP).first()).toContainText("kept.md")
    // ★ The file went out with that question, so it is not pending any
    //   more: putting it back would send it a second time.
    await expect(page.locator(PENDING_CHIP)).toHaveCount(0)
  })

  test("★ 刷新之后还没发出去的文件也还在", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "pending.md", mimeType: "text/markdown", buffer: Buffer.from("hello") },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await page.reload()

    // ★ The workspace is the server's account of the files, so the client
    //   can pick up what it had not sent yet.
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)
    await expect(page.locator(PENDING_CHIP).first()).toContainText("pending.md")
  })
})

test.describe("拖拽与粘贴", () => {
  test("★ 拖进来的文件成为附件", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await dropFile(page, "dropped.txt")

    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)
    await expect(page.locator(PENDING_CHIP).first()).toContainText("dropped.txt")
  })

  test("★ 粘贴进来的截图成为附件", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await pasteFile(page, "clipboard.png")

    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)
    await expect(page.locator(PENDING_CHIP).first()).toContainText("clipboard.png")
  })

  test("★ 粘贴截图不会把文件名塞进消息里", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await pasteFile(page, "clipboard.png")

    await expect(page.locator(COMPOSER)).toHaveValue("")
  })
})

test.describe("移除", () => {
  test("★ × 移除的是真文件", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "removable.txt", mimeType: "text/plain", buffer: Buffer.from("bye") },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await page.locator('[aria-label="Remove removable.txt"]').click()
    await expect(page.locator(PENDING_CHIP)).toHaveCount(0)

    // ★ The chip used to hide the file while the server kept it: the user
    //   was told the file was gone, and it was not.
    const res = await page.request.get("/v2/sessions")
    const { items } = await res.json()
    const alpha = items.find((t: { title: string }) => t.title === "Alpha conversation")
    const files = await (await page.request.get(`/v2/sessions/${alpha.threadId}/files`)).json()
    expect(files.files.map((f: { name: string }) => f.name)).not.toContain("removable.txt")
  })
})

test.describe("预览", () => {
  test("★ 待发的图片有缩略图", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "shot.png", mimeType: "image/png", buffer: PNG_1X1 },
    ])

    // ★ A paperclip cannot answer "did I send the right picture". A
    //   thumbnail can.
    await expect(page.locator(THUMB)).toHaveCount(1)
  })

  test("★ 历史里的图片也有缩略图", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "shot.png", mimeType: "image/png", buffer: PNG_1X1 },
    ])
    await expect(page.locator(THUMB)).toHaveCount(1)

    await sendMessage(page, `${MARKER_A} here is a picture`)
    await expect(page.getByText(/scripted answer/).first()).toBeVisible({ timeout: 7000 })

    await expect(page.locator(`${MESSAGE_CHIP} ${THUMB}`)).toHaveCount(1)
  })

  test("★ 点开是原图，Esc 关上", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "shot.png", mimeType: "image/png", buffer: PNG_1X1 },
    ])
    await expect(page.locator(THUMB)).toHaveCount(1)

    await page.locator('[aria-label="Preview shot.png"]').click()
    await expect(page.locator(PREVIEW)).toBeVisible()
    await expect(page.locator('[data-testid="file-preview-image"]')).toBeVisible()

    await page.keyboard.press("Escape")
    await expect(page.locator(PREVIEW)).toHaveCount(0)
  })

  test("★ 文本附件点开能看到内容", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      {
        name: "notes.md",
        mimeType: "text/markdown",
        buffer: Buffer.from("# Heading\n\nthe body of the note\n"),
      },
    ])
    await expect(page.locator(PENDING_CHIP)).toHaveCount(1)

    await page.locator('[aria-label="Preview notes.md"]').click()
    await expect(page.locator('[data-testid="file-preview-text"]')).toBeVisible()
    await expect(page.locator('[data-testid="file-preview-text"]')).toContainText("the body of the note")
  })

  test("★ 文件没了就回退成图标，不留一张碎图", async ({ page }) => {
    await openApp(page)
    await openThread(page, "Alpha conversation")

    // ★ The picture cannot be fetched: the file is gone — the workspace was
    //   cleaned, or the server cannot serve it any more.
    //
    //   ★ The interception is the honest way to stage this: deleting the
    //   file *after* the chip drew it changes nothing, because the browser
    //   has already decoded the picture and keeps showing it.
    await page.route("**/v2/sessions/*/files/raw*", (route) =>
      route.fulfill({ status: 404, body: "no such file" }),
    )

    await page.locator('[aria-label="Attach files"]').setInputFiles([
      { name: "shot.png", mimeType: "image/png", buffer: PNG_1X1 },
    ])

    // ★ A broken picture reads as a fault in the interface, and it is not
    //   one — so the chip falls back to its icon.
    await expect(page.locator(THUMB)).toHaveCount(0)
    await expect(page.locator(PENDING_CHIP).first()).toBeVisible()
    await expect(page.locator(PENDING_CHIP).first()).toContainText("shot.png")
  })
})

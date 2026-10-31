import { afterEach, describe, expect, test } from "bun:test"

import { runStream } from "../src/lib/api"

// ★ The fetch mock is global. It is put back after every test, so this
//
//	file cannot leak its mock into the next one.
const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
})

function sseResponse(events: unknown[]): Response {
  const encoder = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      for (const ev of events) c.enqueue(encoder.encode(`data:${JSON.stringify(ev)}\n\n`))
      // ★ The stream stays open: the test is about what arrives before
      //   the stream ends, not about what the stream eventually sends.
    },
  })
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  })
}

describe("流事件按帧合并", () => {
  test("事件一个不少，顺序也不变", async () => {
    const sent = [
      { type: "RUN_STARTED", threadId: "t1", runId: "r1" },
      { type: "TEXT_MESSAGE_START", messageId: "m-1" },
      ...Array.from({ length: 20 }, (_, i) => ({
        type: "TEXT_MESSAGE_CONTENT",
        messageId: "m-1",
        delta: `c${i}`,
      })),
      { type: "TEXT_MESSAGE_END", messageId: "m-1" },
      { type: "RUN_FINISHED", threadId: "t1", runId: "r1" },
    ]
    globalThis.fetch = (async () => sseResponse(sent)) as unknown as typeof fetch

    const got: unknown[] = []
    runStream({ threadId: "t1", query: "hi" }, { onEvent: (ev) => got.push(ev) })

    // ★ The stream is still open, so the handler has already seen
    //   everything that was sent — nothing is waiting on the frame timer.
    await new Promise((r) => setTimeout(r, 120))

    expect(got.length).toBe(sent.length)
    expect(got.map((e) => (e as { type: string }).type)).toEqual(sent.map((e) => e.type))
    const deltas = got.filter((e) => (e as { type: string }).type === "TEXT_MESSAGE_CONTENT")
    expect(deltas.map((e) => (e as { delta: string }).delta)).toEqual(
      Array.from({ length: 20 }, (_, i) => `c${i}`),
    )
  })

  test("紧急帧不等帧窗口", async () => {
    globalThis.fetch = (async () =>
      sseResponse([
        { type: "RUN_STARTED", threadId: "t1", runId: "r1" },
        { type: "TEXT_MESSAGE_CONTENT", messageId: "m-1", delta: "x" },
      ])) as unknown as typeof fetch

    const got: unknown[] = []
    runStream({ threadId: "t1", query: "hi" }, { onEvent: (ev) => got.push(ev) })

    // ★ RUN_STARTED is urgent: it is delivered as it arrives, not at the
    //   next frame. One tick is enough — a frame window would be 33ms.
    await new Promise((r) => setTimeout(r, 5))
    expect(got.map((e) => (e as { type: string }).type)).toContain("RUN_STARTED")
  })
})

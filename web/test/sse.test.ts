import { describe, expect, test } from "bun:test"

import { recordedFrames } from "../src/lib/fixtures/recorded-frames"
import { takeEvents } from "../src/lib/sse"

describe("真后端录制的帧", () => {
  const whole = recordedFrames.join("\n\n") + "\n\n"

  test("每一块都能解出 JSON", () => {
    const { events } = takeEvents(whole)
    expect(events).toHaveLength(recordedFrames.length)
    for (const ev of events) {
      expect(() => JSON.parse(ev.data)).not.toThrow()
    }
  })

  test("冒号后无空格的形式照样收得到", () => {
    const { events } = takeEvents('data:{"type":"RUN_STARTED"}\n\n')
    expect(events[0]!.data).toBe('{"type":"RUN_STARTED"}')
  })

  test("带空格的写法也认（兼容别的服务端）", () => {
    const { events } = takeEvents('data: {"type":"RUN_FINISHED"}\n\n')
    expect(events[0]!.data).toBe('{"type":"RUN_FINISHED"}')
  })

  test("类型与顺序原样保留", () => {
    const { events } = takeEvents(whole)
    const types = events.map((e) => JSON.parse(e.data).type)
    expect(types[0]).toBe("RUN_STARTED")
    expect(types.at(-1)).not.toBe("RUN_STARTED")
  })
})

describe("分片", () => {
  const frame = 'data:{"type":"TEXT_MESSAGE_CONTENT","messageId":"m1","delta":"hello"}\n\n'

  test("逐字节喂进去，结果与一次喂入相同", () => {
    let buffer = ""
    const got: string[] = []
    for (const ch of frame) {
      const { events, rest } = takeEvents(buffer + ch)
      buffer = rest
      for (const ev of events) got.push(ev.data)
    }
    expect(got).toHaveLength(1)
    expect(JSON.parse(got[0]!).delta).toBe("hello")
  })

  test("截断在 JSON 中间时不产生半个事件", () => {
    const cut = frame.slice(0, 30)
    const { events, rest } = takeEvents(cut)
    expect(events).toHaveLength(0)
    expect(rest).toBe(cut)
  })

  test("补齐后能解出完整事件", () => {
    const cut = frame.slice(0, 30)
    const first = takeEvents(cut)
    const second = takeEvents(first.rest + frame.slice(30))
    expect(second.events).toHaveLength(1)
  })

  test("随机切点下 200 次都不丢事件", () => {
    const stream = recordedFrames.join("\n\n") + "\n\n"
    for (let seed = 0; seed < 200; seed++) {
      let buffer = ""
      const got: string[] = []
      let i = 0
      while (i < stream.length) {
        const size = 1 + ((seed * 7919 + i * 31) % 17)
        const { events, rest } = takeEvents(buffer + stream.slice(i, i + size))
        buffer = rest
        for (const ev of events) got.push(ev.data)
        i += size
      }
      expect(got).toHaveLength(recordedFrames.length)
      got.forEach((d, k) => {
        expect(JSON.parse(d).type).toBe(JSON.parse(recordedFrames[k]!.slice(5)).type)
      })
    }
  })
})

describe("SSE 规范的边角", () => {
  test("注释行（心跳）被忽略", () => {
    const { events } = takeEvents(": keep-alive\n\ndata:{\"type\":\"RUN_FINISHED\"}\n\n")
    expect(events).toHaveLength(1)
    expect(JSON.parse(events[0]!.data).type).toBe("RUN_FINISHED")
  })

  test("多行 data 用 \\n 连接", () => {
    const { events } = takeEvents('data:{"a":\ndata:1}\n\n')
    expect(events[0]!.data).toBe('{"a":\n1}')
  })

  test("\\r\\n\\r\\n 分隔也能切", () => {
    const { events } = takeEvents('data:{"type":"A"}\r\n\r\ndata:{"type":"B"}\r\n\r\n')
    expect(events.map((e) => JSON.parse(e.data).type)).toEqual(["A", "B"])
  })

  test("空块不产生事件", () => {
    expect(takeEvents("\n\n\n\n").events).toHaveLength(0)
  })

  test("event: 字段被带出", () => {
    const { events } = takeEvents('event: ping\ndata:{"a":1}\n\n')
    expect(events[0]!.event).toBe("ping")
  })

  test("没有终止空行时留在 rest 里", () => {
    const { events, rest } = takeEvents('data:{"type":"A"}\n')
    expect(events).toHaveLength(0)
    expect(rest).toBe('data:{"type":"A"}\n')
  })
})

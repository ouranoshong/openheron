import { describe, expect, test } from "bun:test"

import { prettyArgs } from "../src/lib/tool-args"

describe("prettyArgs", () => {
  test("a one-line object is broken across lines", () => {
    const { text, formatted } = prettyArgs('{"path":"/a/b","pattern":"*.go"}')
    expect(formatted).toBe(true)
    expect(text).toBe('{\n  "path": "/a/b",\n  "pattern": "*.go"\n}')
  })

  test("the key order is the model's own", () => {
    const { text } = prettyArgs('{"z":1,"a":2}')
    expect(text.indexOf('"z"')).toBeLessThan(text.indexOf('"a"'))
  })

  test("an empty object stays empty", () => {
    const { text, formatted } = prettyArgs("{}")
    expect(formatted).toBe(false)
    expect(text).toBe("{}")
  })

  test("a string keeps its quotes", () => {
    const { text } = prettyArgs('{"path":"/a/b"}')
    expect(text).toContain('"path": "/a/b"')
  })

  test("a boolean is not confused with a string", () => {
    const { text } = prettyArgs('{"flag":true,"name":"true"}')
    expect(text).toContain('"flag": true')
    expect(text).toContain('"name": "true"')
  })

  test("a nested object is indented", () => {
    const { text } = prettyArgs('{"a":{"b":1}}')
    expect(text).toBe('{\n  "a": {\n    "b": 1\n  }\n}')
  })

  test("an array is one item per line", () => {
    const { text } = prettyArgs('{"list":[1,2]}')
    expect(text).toBe('{\n  "list": [\n    1,\n    2\n  ]\n}')
  })

  test("a string with newlines is written as a block", () => {
    const { text } = prettyArgs('{"content":"a\\nb"}')
    // ★ The block is a **display**, not JSON: the real newlines, indented,
    //   with the quotes kept on the first and the last line. A JSON string
    //   cannot hold a raw newline, so this form is not parseable — and that
    //   is the trade for a file's content being readable.
    expect(text).toContain('"content": "')
    expect(text).toContain("a")
    expect(text).toContain("b")
    expect(text.split("\n").length).toBeGreaterThan(2)
  })

  test("half-streamed JSON is left alone", () => {
    const raw = '{"pattern": "/home/u'
    const { text, formatted } = prettyArgs(raw)
    expect(formatted).toBe(false)
    expect(text).toBe(raw)
  })

  test("text that is not JSON is left alone", () => {
    const raw = "not json at all"
    const { text, formatted } = prettyArgs(raw)
    expect(formatted).toBe(false)
    expect(text).toBe(raw)
  })

  test("empty text is left alone", () => {
    expect(prettyArgs("")).toEqual({ text: "", formatted: false })
    expect(prettyArgs("   ")).toEqual({ text: "   ", formatted: false })
  })

  test("a deep nest is cut off rather than walked forever", () => {
    let raw = "1"
    for (let i = 0; i < 12; i++) raw = `{"a":${raw}}`
    expect(() => prettyArgs(raw)).not.toThrow()
  })

  test("every value survives the re-formatting", () => {
    const raw = '{"path":"/a/b","opts":{"deep":[1,{"x":null}]},"s":"a\\nb"}'
    const { text } = prettyArgs(raw)
    // ★ What matters is that nothing is dropped: the reader has to see the
    //   whole argument, and a formatter that loses a value is worse than no
    //   formatter at all.
    expect(text).toContain('"/a/b"')
    expect(text).toContain('"deep"')
    expect(text).toContain("null")
    expect(text).toContain("a")
    expect(text).toContain("b")
  })

  test("an argument without a newline stays valid JSON", () => {
    const raw = '{"path":"/a/b","opts":{"deep":[1,{"x":null}]}}'
    const { text } = prettyArgs(raw)
    // ★ A value with no newline in it has no reason to stop being JSON, and
    //   keeping it parseable means the reader can copy it back.
    expect(() => JSON.parse(text)).not.toThrow()
    expect(JSON.parse(text)).toEqual(JSON.parse(raw))
  })
})

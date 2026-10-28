import { describe, expect, test } from "bun:test"

import {
  applyTrigger,
  findTrigger,
  mentionInsertion,
  removeTrigger,
  type TriggerToken,
} from "../src/lib/composer-triggers"

describe("findTrigger", () => {
  test("mention at the caret", () => {
    expect(findTrigger("@comp", 5)).toEqual({
      kind: "mention",
      start: 0,
      end: 5,
      query: "comp",
      quoted: false,
    })
  })

  test("mention after whitespace", () => {
    expect(findTrigger(" @comp", 6)).toEqual({
      kind: "mention",
      start: 1,
      end: 6,
      query: "comp",
      quoted: false,
    })
  })

  test("email is not a mention", () => {
    expect(findTrigger("mail a@b.com", "mail a@b.com".length)).toBeNull()
  })

  test("quoted mention stays open until the closing quote", () => {
    const text = '@"web/src/a b.ts'
    expect(findTrigger(text, text.length)).toEqual({
      kind: "mention",
      start: 0,
      end: text.length,
      query: "web/src/a b.ts",
      quoted: true,
    })
  })

  test("closed quoted mention does not trigger", () => {
    const text = '@"a b"'
    expect(findTrigger(text, text.length)).toBeNull()
  })

  test("command at the start and after leading whitespace", () => {
    expect(findTrigger("/re", 3)).toEqual({
      kind: "command",
      start: 0,
      end: 3,
      query: "re",
      quoted: false,
    })
    expect(findTrigger("  /re", 5)).toMatchObject({ kind: "command", start: 2, query: "re" })
  })

  test("command only triggers at the start of the message", () => {
    expect(findTrigger("hello /re", 9)).toBeNull()
    expect(findTrigger("/review src", 11)).toBeNull()
  })

  test("bare slash opens the command menu", () => {
    expect(findTrigger("/", 1)).toMatchObject({ kind: "command", start: 0, query: "" })
  })

  test("caret is clamped to the text", () => {
    expect(findTrigger("@comp", 999)?.query).toBe("comp")
    expect(findTrigger("@comp", -3)).toBeNull()
  })
})

describe("applyTrigger", () => {
  test("inserts a mention plus one trailing space", () => {
    const token = findTrigger("@comp", 5)!
    const got = applyTrigger("@comp", token, "@web/src/a.ts")
    expect(got.text).toBe("@web/src/a.ts ")
    expect(got.caret).toBe("@web/src/a.ts ".length)
  })

  test("does not double the existing whitespace", () => {
    const token = findTrigger("@comp rest", 5)!
    const got = applyTrigger("@comp rest", token, "@web/src/a.ts")
    expect(got.text).toBe("@web/src/a.ts rest")
    expect(got.caret).toBe("@web/src/a.ts".length)
  })

  test("quoted paths keep spaces", () => {
    expect(mentionInsertion("web/src/a b.ts")).toBe('@"web/src/a b.ts"')
    expect(mentionInsertion("web/src/a.ts")).toBe("@web/src/a.ts")
  })
})

describe("removeTrigger", () => {
  const token: TriggerToken = {
    kind: "command",
    start: 0,
    end: 4,
    query: "new",
    quoted: false,
  }

  test("an action-only message becomes empty", () => {
    expect(removeTrigger("/new ", token)).toBe("")
  })

  test("other text is preserved", () => {
    expect(
      removeTrigger("hello /new", { ...token, start: 6, end: 10 }),
    ).toBe("hello ")
  })
})

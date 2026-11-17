import { describe, expect, test } from "bun:test"

import { envState, removeGroupMessage, sourceLabel, statusOf } from "@/lib/secrets"
import type { SecretGroup, SecretItem } from "@/lib/secrets-api"

// ★ The whole point of the pure layer: the wording and the states are
//   functions of the server's answer, so they can be checked without a
//   browser and without a server.

function item(over: Partial<SecretItem> = {}): SecretItem {
  return {
    name: "deepseek",
    stored: false,
    source: "",
    effective: "none",
    shadowed: false,
    envVar: "LLM_DEEPSEEK_API_KEY",
    ...over,
  }
}

describe("sourceLabel", () => {
  test("a place becomes a sentence", () => {
    expect(sourceLabel("env:LLM_DEEPSEEK_API_KEY")).toBe(
      "the environment variable LLM_DEEPSEEK_API_KEY",
    )
    expect(sourceLabel("keyring:llm:deepseek")).toBe("the keyring entry llm:deepseek")
    expect(sourceLabel("file:/etc/openheron/key")).toBe("the file /etc/openheron/key")
  })

  test("no key says so", () => {
    expect(sourceLabel("none")).toBe("nowhere — no key is in effect")
    expect(sourceLabel("")).toBe("nowhere — no key is in effect")
  })

  test("an unknown kind is passed through", () => {
    expect(sourceLabel("weird:thing")).toBe("weird:thing")
  })
})

describe("statusOf", () => {
  test("a stored key says where it is and which one is in force", () => {
    const s = statusOf(
      item({ stored: true, source: "keyring:llm:deepseek", effective: "keyring:llm:deepseek" }),
    )
    expect(s.state).toBe("stored")
    expect(s.label).toBe("stored")
    expect(s.effective).toBe("the keyring entry llm:deepseek")
    expect(s.warning).toBeUndefined()
  })

  test("a key that is not there says so", () => {
    const s = statusOf(item())
    expect(s.state).toBe("not set")
    expect(s.label).toBe("not set")
  })

  // ★ The trap of the whole design: the key was written, the write said
  //   "stored", and the value in use is a different one.
  test("a shadowed key warns", () => {
    const s = statusOf(
      item({
        stored: true,
        source: "keyring:llm:deepseek",
        effective: "env:LLM_DEEPSEEK_API_KEY",
        shadowed: true,
      }),
    )
    expect(s.state).toBe("stored")
    expect(s.effective).toBe("the environment variable LLM_DEEPSEEK_API_KEY")
    expect(s.warning).toContain("wins")
    expect(s.warning).toContain("LLM_DEEPSEEK_API_KEY")
  })
})

describe("envState", () => {
  test("the effective source says whether the variable holds anything", () => {
    expect(envState(item({ effective: "env:LLM_DEEPSEEK_API_KEY" }))).toBe("set")
    expect(envState(item({ effective: "file:/tmp/key" }))).toBe("set")
    expect(envState(item({ effective: "none" }))).toBe("not set")
    expect(envState(item({ effective: "keyring:llm:deepseek" }))).toBe("not set")
  })
})

describe("removeGroupMessage", () => {
  const group = (id: string, count: number): SecretGroup => ({
    id,
    label: id === "llm" ? "Providers" : "MCP servers",
    items: Array.from({ length: count }, (_, i) => item({ name: `s${i}` })),
  })

  test("the count and the scope are both named", () => {
    const msg = removeGroupMessage(group("llm", 2))
    expect(msg).toContain("Remove 2 provider secrets")
    expect(msg).toContain("Providers")
    expect(msg).toContain("MCP server secrets are untouched")
  })

  test("one is singular", () => {
    expect(removeGroupMessage(group("mcp", 1))).toContain("Remove 1 MCP server secret?")
  })

  test("the other group names the other scope", () => {
    const msg = removeGroupMessage(group("mcp", 3))
    expect(msg).toContain("provider secrets are untouched")
  })
})

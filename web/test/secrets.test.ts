import { describe, expect, test } from "bun:test"

import {
  envState,
  errorMessage,
  failureOf,
  groupLabel,
  removeGroupMessage,
  sourceLabel,
  statusOf,
} from "@/lib/secrets"
import { translator } from "@/lib/i18n"
import { ApiError } from "@/lib/api"
import { catalog, type MessageKey } from "@/lib/locales"
import type { SecretGroup, SecretItem } from "@/lib/secrets-api"

// ★ The whole point of the pure layer: the wording and the states are
//   functions of the server's answer, so they can be checked without a
//   browser and without a server.
//
// ★ The English translator: the catalog's English is the text the
//   interface showed before the copy moved into it.

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

const en = translator("en")
const zh = translator("zh")

describe("sourceLabel", () => {
  test("a place becomes a sentence", () => {
    expect(sourceLabel("env:LLM_DEEPSEEK_API_KEY", en)).toBe(
      "the environment variable LLM_DEEPSEEK_API_KEY",
    )
    expect(sourceLabel("keyring:llm:deepseek", en)).toBe("the keyring entry llm:deepseek")
    expect(sourceLabel("file:/etc/openheron/key", en)).toBe("the file /etc/openheron/key")
  })

  test("no key says so", () => {
    expect(sourceLabel("none", en)).toBe("no source — no key is in effect")
    expect(sourceLabel("", en)).toBe("no source — no key is in effect")
  })

  test("an unknown kind is passed through", () => {
    expect(sourceLabel("weird:thing", en)).toBe("weird:thing")
  })
})

describe("statusOf", () => {
  test("a stored key says where it is and which one is in force", () => {
    const s = statusOf(
      item({ stored: true, source: "keyring:llm:deepseek", effective: "keyring:llm:deepseek" }),

    en,

    )
    expect(s.state).toBe("stored")
    expect(s.label).toBe("stored")
    expect(s.effective).toBe("the keyring entry llm:deepseek")
    expect(s.warning).toBeUndefined()
  })

  test("a key that is not there says so", () => {
    const s = statusOf(item(), en)
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

    en,

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
    const msg = removeGroupMessage(group("llm", 2), en)
    expect(msg).toContain("Remove 2 provider secrets")
    expect(msg).toContain("Providers")
    expect(msg).toContain("MCP server secrets are untouched")
  })

  test("one is singular", () => {
    expect(removeGroupMessage(group("mcp", 1), en)).toContain("Remove 1 MCP server secret?")
  })

  test("the other group names the other scope", () => {
    const msg = removeGroupMessage(group("mcp", 3), en)
    expect(msg).toContain("provider secrets are untouched")
  })

  test("in Chinese the names are the reader's", () => {
    const msg = removeGroupMessage(group("llm", 2), zh)
    expect(msg).toContain("移除 2 个 服务商密钥？")
    expect(msg).toContain("服务商 分组")
    expect(msg).toContain("连接器密钥 保持不变")
    expect(msg).not.toContain("Providers")
    expect(msg).not.toContain("MCP server")
  })
})

describe("groupLabel", () => {
  const group = (id: string, label: string): SecretGroup => ({
    id,
    label,
    items: [],
  })

  test("the id decides the name", () => {
    expect(groupLabel(group("llm", "Providers"), en)).toBe("Providers")
    expect(groupLabel(group("mcp", "MCP servers"), en)).toBe("MCP servers")
    expect(groupLabel(group("llm", "Providers"), zh)).toBe("服务商")
    expect(groupLabel(group("mcp", "MCP servers"), zh)).toBe("连接器")
  })

  test("the name keeps its capitals", () => {
    expect(groupLabel(group("mcp", "MCP servers"), en)).toContain("MCP servers")
  })

  // ★ The defect the whole change is about: "MCP servers" was once
  //   lowercased into "mcp servers". ⇒ The name is never case-folded, in
  //   any language.
  test("★ 名字永远不会被小写化", () => {
    expect(groupLabel(group("mcp", "MCP servers"), en)).toBe("MCP servers")
    expect(groupLabel(group("mcp", "MCP servers"), en)).not.toContain("mcp servers")
    expect(groupLabel(group("llm", "Providers"), en)).not.toContain("providers")
  })

  test("an id the catalog does not know falls back to the server's label", () => {
    expect(groupLabel(group("other", "Something else"), en)).toBe("Something else")
    expect(groupLabel(group("other", "Something else"), zh)).toBe("Something else")
  })

  test("a missing label does not leave a blank heading", () => {
    expect(groupLabel(group("other", ""), en)).toBe("other")
  })

  // ★ The same, in the other language: an unknown id has no Chinese name,
  //   so the server's label is shown as it is — and an empty one leaves
  //   the id rather than a blank line.
  test("★ 未知 id 且没有 label：显示 id，不留空标题", () => {
    expect(groupLabel(group("other", ""), zh)).toBe("other")
    expect(groupLabel(group("other", "   "), zh)).toBe("other")
  })
})

describe("errorMessage", () => {
  test("a known code is said in the reader's language", () => {
    const f = { code: "secret_unavailable", message: "The keyring is not available." }
    expect(errorMessage(f, en)).toBe("The keyring is not available on this machine.")
    expect(errorMessage(f, zh)).toBe("本机密钥环不可用。")
  })

  test("an unknown code shows the server's sentence as it is", () => {
    const f = { code: "weird_code", message: "The vault is on fire." }
    expect(errorMessage(f, en)).toBe("The vault is on fire.")
    expect(errorMessage(f, zh)).toBe("The vault is on fire.")
  })

  test("a failure that never reached the server is not shown as English", () => {
    const f = failureOf(new TypeError("Failed to fetch"))
    expect(f.code).toBe("network")
    expect(errorMessage(f, zh)).toBe("无法连接到服务端。")
    expect(errorMessage(f, zh)).not.toContain("Failed to fetch")
  })

  test("a response that could not be parsed says so in the reader's language", () => {
    const f = failureOf(new SyntaxError("Unexpected token"))
    expect(f.code).toBe("unknown")
    expect(errorMessage(f, zh)).toBe("出了点问题。")
  })

  test("the server's envelope keeps its code", () => {
    const f = failureOf(new ApiError(503, { error: "keyring down", code: "secret_unavailable", retryable: true }))
    expect(f.code).toBe("secret_unavailable")
    expect(errorMessage(f, zh)).toBe("本机密钥环不可用。")
  })

  // ★ A5: the two codes the contract names, each of them in Chinese, and
  //   each of them **without** the server's English sentence.
  test("★ invalid_body 也是中文句，不带服务端英文原句", () => {
    const f = { code: "invalid_body", message: "no such secret" }
    expect(errorMessage(f, zh)).toBe("请求不合法。")
    expect(errorMessage(f, zh)).not.toContain("no such secret")
    expect(errorMessage(f, en)).toBe("The request was not valid.")
  })

  test("★ secret_unavailable 也不带服务端英文原句", () => {
    const f = { code: "secret_unavailable", message: "keyring down" }
    expect(errorMessage(f, zh)).toBe("本机密钥环不可用。")
    expect(errorMessage(f, zh)).not.toContain("keyring down")
  })

  // ★ The two codes the **client** synthesizes are in the table too, so
  //   they are said in the reader's language like any other failure.
  test("★ 客户端合成的两个码也是本地化的", () => {
    expect(errorMessage({ code: "network", message: "Failed to fetch" }, zh)).toBe("无法连接到服务端。")
    expect(errorMessage({ code: "unknown", message: "Unexpected token" }, zh)).toBe("出了点问题。")
    expect(errorMessage({ code: "network", message: "Failed to fetch" }, en)).toBe(
      "The server could not be reached.",
    )
    expect(errorMessage({ code: "unknown", message: "Unexpected token" }, en)).toBe("Something went wrong.")
  })
})

describe("failureOf", () => {
  test("★ 服务端的信封：码和句子都原样留着", () => {
    const f = failureOf(
      new ApiError(500, { error: "the vault is on fire", code: "vault_on_fire", retryable: true }),
    )
    expect(f.code).toBe("vault_on_fire")
    expect(f.message).toBe("the vault is on fire")
  })

  test("★ invalid_body 的码被保留，句子换成了读者的语言", () => {
    const f = failureOf(
      new ApiError(404, { error: "no such secret", code: "invalid_body", retryable: false }),
    )
    expect(f.code).toBe("invalid_body")
    expect(errorMessage(f, zh)).toBe("请求不合法。")
    expect(errorMessage(f, zh)).not.toContain("no such secret")
  })

  // ★ A body that could not be parsed is not a network failure: it is the
  //   "unknown" branch of the contract's table.
  test("★ 解析不了的响应：合成 unknown", () => {
    const f = failureOf(new SyntaxError("Unexpected token < in JSON"))
    expect(f.code).toBe("unknown")
    expect(errorMessage(f, zh)).toBe("出了点问题。")
    expect(errorMessage(f, zh)).not.toContain("Unexpected token")
  })

  // ★ Everything else that is not an ApiError is "the server could not be
  //   reached" — and its English text never reaches the reader.
  test("★ 连不上服务端：合成 network", () => {
    const f = failureOf(new TypeError("Failed to fetch"))
    expect(f.code).toBe("network")
    expect(errorMessage(f, zh)).toBe("无法连接到服务端。")
    expect(errorMessage(f, zh)).not.toContain("Failed to fetch")
  })

  test("★ 连抛出的都不是 Error：也是 network", () => {
    const f = failureOf("boom")
    expect(f.code).toBe("network")
    expect(f.message).toBe("boom")
    expect(errorMessage(f, zh)).toBe("无法连接到服务端。")
  })
})

// ★ A8: the catalog side of the contract — every key of the secrets panel
//   is written in Chinese, and what is written there is Chinese.
describe("the secrets catalog", () => {
  const secretEntries = Object.entries(catalog).filter(
    ([key]) => key.startsWith("secrets.") || key.startsWith("settings.secrets."),
  )

  test("★ 密钥面板的每一条都写了中文", () => {
    const empty = secretEntries.filter(([, entry]) => !entry.zh.trim()).map(([key]) => key)
    expect(empty).toEqual([])
  })

  test("★ 中文里没有残留的英文（专有名词 MCP 除外）", () => {
    const leftover: string[] = []
    for (const [key, entry] of secretEntries) {
      // ★ The placeholders are not copy: `{name}` is a hole, not a word.
      const text = entry.zh.replace(/\{[^}]*\}/g, "")
      for (const word of text.match(/[A-Za-z]+/g) ?? []) {
        if (word.toUpperCase() === "MCP") continue
        leftover.push(`${key}: ${word}`)
      }
    }
    expect(leftover).toEqual([])
  })

  test("★ 词条就是契约里写下的那些字", () => {
    const expected: Record<string, { en: string; zh: string }> = {
      "secrets.group.llm": { en: "Providers", zh: "服务商" },
      "secrets.group.mcp": { en: "MCP servers", zh: "连接器" },
      "secrets.error.secretUnavailable": {
        en: "The keyring is not available on this machine.",
        zh: "本机密钥环不可用。",
      },
      "secrets.error.invalidBody": { en: "The request was not valid.", zh: "请求不合法。" },
      "secrets.error.unknown": { en: "Something went wrong.", zh: "出了点问题。" },
      "secrets.error.network": { en: "The server could not be reached.", zh: "无法连接到服务端。" },
      "secrets.source.none": {
        en: "no source — no key is in effect",
        zh: "无来源 —— 没有生效的密钥",
      },
      "settings.secrets.setValue": { en: "Save key", zh: "保存密钥" },
      "settings.secrets.removeAll": { en: "Remove all {group}", zh: "移除全部 {group}" },
    }
    for (const [key, want] of Object.entries(expected)) {
      const entry = catalog[key as MessageKey]
      expect(String(entry.en), `the English of ${key}`).toBe(want.en)
      expect(String(entry.zh), `the Chinese of ${key}`).toBe(want.zh)
    }
  })

  // ★ S5: the four scope words are four keys, and they never mix: the
  //   provider one and the MCP one are different words in both languages.
  test("★ 四个范围词都在，而且互不混用", () => {
    expect(catalog["secrets.scope.providerOne"].en).toBe("provider secret")
    expect(catalog["secrets.scope.providerMany"].en).toBe("provider secrets")
    expect(catalog["secrets.scope.mcpOne"].en).toBe("MCP server secret")
    expect(catalog["secrets.scope.mcpMany"].en).toBe("MCP server secrets")
    expect(catalog["secrets.scope.providerOne"].zh).toBe("服务商密钥")
    expect(catalog["secrets.scope.providerMany"].zh).toBe("服务商密钥")
    expect(catalog["secrets.scope.mcpOne"].zh).toBe("连接器密钥")
    expect(catalog["secrets.scope.mcpMany"].zh).toBe("连接器密钥")
  })

  // ★ The scope words are **fragments of a sentence**
  //   ("Remove {count} {scope}?"), so the English ones are lowercase;
  //   "MCP" stays an acronym, but "mcp server secret" is wrong.
  test("★ 英文的范围词是句中片段：小写", () => {
    for (const key of ["secrets.scope.providerOne", "secrets.scope.providerMany"]) {
      const word = catalog[key as MessageKey].en
      expect(word[0]).toBe(word[0].toLowerCase())
      expect(word[0]).not.toBe(word[0].toUpperCase())
    }
    expect(catalog["secrets.scope.mcpOne"].en.startsWith("MCP server")).toBe(true)
    expect(catalog["secrets.scope.mcpMany"].en).not.toContain("mcp server")
  })
})

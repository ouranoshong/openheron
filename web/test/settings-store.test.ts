import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import {
  selectCanSave,
  selectCategories,
  selectDirty,
  selectIsEditable,
  selectItems,
  selectSelectedItem,
  selectServerIssues,
  useSettings,
  type SettingsState,
} from "../src/lib/settings-store"
import type { SettingsItem, SettingsManifest } from "../src/lib/settings-api"

const HTTP = "methods: [get]\nallows: [request_get]\n"
const PROVIDER = "type: openai\nbase_url: https://x\napi_key: ${KEY}\nmodels:\n  - id: fast\n"

const MAIN_MD = "---\nmodel: deepseek/flash\ndescription: root\nreasoning: high  # level\n---\nbody\n"

const ON_DISK: Record<string, string> = {
  "tools/http.yaml": HTTP,
  "agents/main.md": MAIN_MD,
  "llm/deepseek.yaml": PROVIDER,
  "server/config.yaml": "listen_addr: \":9099\"\n",
}

const item = (path: string, over: Partial<SettingsItem> = {}): SettingsItem => ({
  path,
  exists: true,
  hot: true,
  revision: "rev-" + path,
  ...over,
})

const manifest: SettingsManifest = {
  categories: [
    { id: "tools", editable: true, hot: true },
    { id: "agents", editable: true, hot: true },
    { id: "llm", editable: true, hot: true },
    { id: "server", editable: false, hot: false, note: "not hot-reloaded; changes apply on restart" },
  ],
  items: [
    item("tools/http.yaml"),
    item("agents/main.md"),
    item("llm/deepseek.yaml"),
    item("server/config.yaml", { hot: false }),
  ],
  templates: { tools: "# one file = one tool\n", agents: "---\nmodel: \n---\n" },
  modelRefs: ["deepseek/flash"],
  reasoningLevels: { "deepseek/flash": ["off", "low", "high", "max"] },
  configVersion: "v1",
}

type Call = { method: string; url: string; body: Record<string, string> }

let calls: Call[] = []
let disk: Record<string, string> = {}
let hooks: { onDelete?: () => Response } = {}

function install(): void {
  calls = []
  disk = { ...ON_DISK }
  hooks = {}

  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? "GET"
    const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, string>) : {}
    calls.push({ method, url, body })

    const json = (data: unknown, status = 200): Response =>
      ({ ok: status < 400, status, json: async () => data }) as Response

    if (url === "/v2/settings") return json(manifest)

    if (url.startsWith("/v2/settings/item?") && method === "DELETE") {
      const path = decodeURIComponent(url.split("path=")[1] ?? "")
      if (hooks.onDelete) return hooks.onDelete()
      delete disk[path]
      return json({ path, revision: "", configVersion: "v2", applied: true })
    }

    if (url.startsWith("/v2/settings/item?")) {
      const path = decodeURIComponent(url.split("path=")[1] ?? "")
      const exists = path in disk
      const found = manifest.items.find((i) => i.path === path)
      return json({
        path,
        content: exists ? disk[path]! : "",
        exists,
        revision: exists ? "rev-live" : "",
        hot: found?.hot ?? true,
      })
    }

    if (url === "/v2/settings/validate") {
      if (body.content!.includes("BROKEN_MERGE")) {
        return json({ error: "agents/main.md references model x/y not declared", code: "config_would_break_merge", retryable: false }, 422)
      }
      // ★ Two findings, so the store can be tested for what it does with a
      //   verdict — render it, block the write, replace it on the next
      //   answer. Which findings a real loader produces is agentkit/spec's
      //   business and is tested there.
      const issues: { line: number; severity: string; message: string }[] = []
      if (/^api_key:\s*sk-/m.test(body.content!)) {
        issues.push({
          line: 3,
          severity: "error",
          message: "api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path",
        })
      }
      const level = /^reasoning:\s*([a-z]+)/m.exec(body.content!)?.[1]
      if (level === "ultra") {
        issues.push({
          line: 4,
          severity: "error",
          message: 'reasoning "ultra" is not declared by deepseek/flash — the model declares: high, low, max, off',
        })
      }
      return json({ ok: issues.length === 0, issues, configVersion: "v1" })
    }

    if (url === "/v2/settings/item") {
      if (body.baseRevision === "stale") {
        return json({ error: "changed on disk", code: "config_write_conflict", retryable: false }, 409)
      }
      disk[body.path!] = body.content!
      return json({ path: body.path, revision: "rev-new", configVersion: "v2", applied: true })
    }

    throw new Error("unexpected fetch: " + method + " " + url)
  }) as typeof fetch
}

const s = (): SettingsState => useSettings.getState()

beforeEach(() => {
  install()
})

afterEach(() => {
  // ★ A debounced validation that is still pending would fire during the
  //   next test, against that test's fetch. closeSettings is what cancels it.
  s().closeSettings()
  useSettings.setState({
    open: false,
    manifest: undefined,
    loading: false,
    loadError: undefined,
    selected: undefined,
    saved: "",
    draft: "",
    revision: "",
    exists: false,
    hot: true,
    issues: [],
    serverIssues: [],
    saveState: "idle",
    applied: false,
    saveError: undefined,
    conflict: undefined,
  })
})

describe("opening", () => {
  test("loads the manifest and selects the first editable file", async () => {
    await s().openSettings()
    expect(s().open).toBe(true)
    expect(s().manifest?.items).toHaveLength(4)
    expect(s().selected).toBe("tools/http.yaml")
  })

  test("records a load failure instead of throwing", async () => {
    globalThis.fetch = (async () => {
      throw new Error("no config service")
    }) as unknown as typeof fetch
    await s().openSettings()
    expect(s().loading).toBe(false)
    expect(s().loadError).toContain("no config service")
  })

  test("closing discards the draft", async () => {
    await s().openSettings()
    s().edit("allow: [draft]\n")
    expect(selectDirty(s())).toBe(true)
    s().closeSettings()
    expect(s().draft).toBe("")
    expect(selectDirty(s())).toBe(false)
  })
})

describe("the one config root", () => {
  test("the manifest lists every file exactly once", async () => {
    await s().openSettings()
    const paths = s().manifest?.items.map((i) => i.path) ?? []
    expect(new Set(paths).size).toBe(paths.length)
  })

  test("reading a file asks for the path only — there is no level to pass", async () => {
    await s().openSettings()
    const lastGet = calls.filter((c) => c.url.startsWith("/v2/settings/item?")).at(-1)
    expect(lastGet?.url).toContain("path=tools%2Fhttp.yaml")
    expect(lastGet?.url).not.toContain("level=")
  })

  test("a write carries the path only", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    await s().save()
    const put = calls.find((c) => c.method === "PUT")
    expect(put?.body.path).toBe("tools/http.yaml")
    expect(put?.body.level).toBeUndefined()
  })
})

describe("editing", () => {
  test("dirty is derived, not stored", async () => {
    await s().openSettings()
    expect(selectDirty(s())).toBe(false)
    s().edit("allow: [x]\n")
    expect(selectDirty(s())).toBe(true)
    s().edit(s().saved)
    expect(selectDirty(s())).toBe(false)
  })

  test("a server verdict blocks the write, and the PUT never goes out", async () => {
    await s().openSettings()
    await s().select("llm/deepseek.yaml")
    s().edit("type: openai\nbase_url: https://x\napi_key: sk-leak\nmodels: []\n")

    await s().save()
    expect(s().saveState).toBe("error")
    expect(s().saveError).toContain("reference")
    expect(s().issues.some((i) => i.message.includes("reference"))).toBe(true)
    expect(calls.some((c) => c.method === "PUT")).toBe(false)
  })

  // ★ Validation is a round trip now, so it is debounced. Two keystrokes
  //   inside the window are one question — the answer is about the draft, and
  //   the draft has moved on.
  test("a burst of edits asks the server once, after the pause", async () => {
    await s().openSettings()
    await s().select("llm/deepseek.yaml")
    const before = calls.filter((c) => c.url === "/v2/settings/validate").length

    s().edit("type: openai\n")
    s().edit("type: openai\nbase_url: https://x\n")
    s().edit(PROVIDER)
    await new Promise((r) => setTimeout(r, 400))

    const validated = calls.filter((c) => c.url === "/v2/settings/validate").slice(before)
    expect(validated).toHaveLength(1)
    expect(validated[0]!.body.content).toBe(PROVIDER)
  })

  test("the answer replaces the previous verdict", async () => {
    await s().openSettings()
    await s().select("llm/deepseek.yaml")
    s().edit("api_key: sk-leak\n")
    await new Promise((r) => setTimeout(r, 400))
    expect(s().issues.some((i) => i.message.includes("reference"))).toBe(true)

    s().edit(PROVIDER)
    await new Promise((r) => setTimeout(r, 400))
    expect(s().issues).toEqual([])
    expect(s().serverIssues).toEqual([])
  })

  // ★ A save clicked inside the debounce window must not write on the
  //   strength of a verdict about older text: it asks again, now, and the
  //   pending question is cancelled so the two answers cannot cross.
  test("saving cancels the pending question and asks its own", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    const before = calls.filter((c) => c.url === "/v2/settings/validate").length
    await s().save()
    await new Promise((r) => setTimeout(r, 400))

    const validated = calls.filter((c) => c.url === "/v2/settings/validate").slice(before)
    expect(validated).toHaveLength(1)
    expect(s().saveState).toBe("saved")
  })
})

describe("saving", () => {
  test("an agent file with a legal reasoning level saves", async () => {
    await s().openSettings()
    await s().select("agents/main.md")
    expect(s().issues).toEqual([])

    s().edit(s().draft.replace("description: root", "description: root2"))
    await s().save()

    expect(calls.some((c) => c.method === "PUT" && c.url === "/v2/settings/item")).toBe(true)
  })

  test("an unknown reasoning level blocks the write and says what is available", async () => {
    await s().openSettings()
    await s().select("agents/main.md")
    s().edit(s().draft.replace("reasoning: high", "reasoning: ultra"))
    await s().save()

    expect(s().saveState).toBe("error")
    expect(s().saveError).toContain("ultra")
    expect(s().saveError).toContain("high, low, max, off")
    expect(calls.some((c) => c.method === "PUT")).toBe(false)
  })

  test("validates on the server before writing", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    await s().save()

    const order = calls.map((c) => c.url)
    const validateAt = order.indexOf("/v2/settings/validate")
    const putAt = order.findIndex((u, i) => u === "/v2/settings/item" && calls[i]!.method === "PUT")
    expect(validateAt).toBeGreaterThanOrEqual(0)
    expect(validateAt).toBeLessThan(putAt)
  })

  test("a rejected merge keeps the file untouched", async () => {
    await s().openSettings()
    s().edit("BROKEN_MERGE")
    await s().save()

    expect(s().serverIssues.length).toBeGreaterThan(0)
    expect(s().saveError).toBeUndefined()
    expect(calls.some((c) => c.method === "PUT")).toBe(false)
    expect(s().draft).toBe("BROKEN_MERGE")
  })

  test("after saving, the draft becomes the new saved value", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    await s().save()

    expect(s().saveState).toBe("saved")
    expect(selectDirty(s())).toBe(false)
    expect(selectCanSave(s())).toBe(false)
    expect(s().revision).toBe("rev-new")
  })

  test("sends the revision it read, so a stale edit cannot overwrite", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    await s().save()
    const put = calls.find((c) => c.method === "PUT")
    expect(put?.body.baseRevision).toBe("rev-live")
  })

  test("reports a conflict without merging anything", async () => {
    await s().openSettings()
    useSettings.setState({ revision: "stale" })
    s().edit("ask: [execute]\n")
    await s().save()

    expect(s().saveState).toBe("conflict")
    expect(s().conflict?.yours).toBe("ask: [execute]\n")
    expect(s().conflict?.revision).toBe("stale")
    expect(s().draft).toBe("ask: [execute]\n")
  })

  test("reloading after a conflict drops the draft", async () => {
    await s().openSettings()
    useSettings.setState({ revision: "stale" })
    s().edit("ask: [execute]\n")
    await s().save()
    expect(s().saveState).toBe("conflict")

    await s().reloadFromDisk()
    expect(s().conflict).toBeUndefined()
    expect(s().draft).toBe(s().saved)
  })

  test("reports whether the write will actually take effect", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    await s().save()
    expect(s().applied).toBe(true)
  })
})

describe("deleting", () => {
  test("clears the selection and refreshes the manifest", async () => {
    await s().openSettings()
    await s().remove()

    expect(s().selected).toBeUndefined()
    expect(s().draft).toBe("")
    expect(s().exists).toBe(false)
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1)
  })

  test("a rejected delete keeps the item selected", async () => {
    await s().openSettings()
    hooks.onDelete = () =>
      ({
        ok: false,
        status: 422,
        json: async () => ({ error: "agent main references openai/fast", code: "config_would_break_merge", retryable: false }),
      }) as Response

    await s().remove()
    expect(s().saveState).toBe("error")
    expect(s().serverIssues.join()).toContain("openai/fast")
    expect(s().selected).toBe("tools/http.yaml")
  })
})

// ── selectors ─────────────────────────────────────────────────────────────

describe("selectors", () => {
  test("return module-level constants instead of fresh arrays", async () => {
    await s().openSettings()
    const before = s().manifest
    useSettings.setState({ manifest: undefined })

    const a = selectItems(s())
    const b = selectItems(s())
    expect(a).toBe(b)
    expect(selectItems({ ...before, manifest: undefined } as unknown as SettingsState)).toBe(a)
  })

  test("the item list is a new array, so callers must useShallow", async () => {
    await s().openSettings()
    expect(selectItems(s()).length).toBeGreaterThan(0)
    expect(selectItems(s())).not.toBe(selectItems(s()))
  })

  test("selectSelectedItem finds the selected path", async () => {
    await s().openSettings()
    expect(selectSelectedItem(s())?.path).toBe("tools/http.yaml")
  })

  // ★ A dirty draft on a partition this client may not write must not make
  //   Save reachable. The editor also refuses to type into it; this is the
  //   second lock, and the one that survives a change to the first.
  test("a non-editable partition is never saveable, however dirty the draft", async () => {
    await s().openSettings()
    useSettings.setState({ selected: "server/config.yaml", saved: "a\n", draft: "b\n" })
    expect(selectDirty(s())).toBe(true)
    expect(selectCanSave(s())).toBe(false)

    useSettings.setState({ selected: "tools/http.yaml", saved: "a\n", draft: "b\n" })
    expect(selectCanSave(s())).toBe(true)
  })

  test("selectIsEditable reads the category, not the item", async () => {
    await s().openSettings()
    useSettings.setState({ selected: "server/config.yaml" })
    expect(selectIsEditable(s())).toBe(false)
    useSettings.setState({ selected: "agents/main.md" })
    expect(selectIsEditable(s())).toBe(true)
  })

  test("empty collections are shared constants", () => {
    const empty = { ...s(), manifest: undefined }
    expect(selectItems(empty)).toBe(selectItems({ ...empty }))
    expect(selectCategories(empty)).toBe(selectCategories({ ...empty }))
    expect(selectServerIssues(empty)).toBe(selectServerIssues({ ...empty }))
  })
})
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

const POLICY = "allow: [read_file]\n"
const PROVIDER = "type: openai\nbase_url: https://x\napi_key: ${KEY}\nmodels:\n  - id: fast\n"

const MAIN_MD = "---\nmodel: deepseek/flash\ndescription: root\nreasoning: high  # level\n---\nbody\n"

const ON_DISK: Record<string, string> = {
  "policy/permissions.yaml": POLICY,
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
    { id: "policy", editable: true, hot: true },
    { id: "agents", editable: true, hot: true },
    { id: "llm", editable: true, hot: true },
    { id: "server", editable: false, hot: false, note: "not hot-reloaded; changes apply on restart" },
  ],
  items: [
    item("policy/permissions.yaml"),
    item("agents/main.md"),
    item("llm/deepseek.yaml"),
    item("server/config.yaml", { hot: false }),
  ],
  templates: { policy: "allow: []\nask: []\ndeny: []\n", agents: "---\nmodel: \n---\n" },
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
      return json({ ok: true, configVersion: "v1" })
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
    expect(s().selected).toBe("policy/permissions.yaml")
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
    expect(lastGet?.url).toContain("path=policy%2Fpermissions.yaml")
    expect(lastGet?.url).not.toContain("level=")
  })

  test("a write carries the path only", async () => {
    await s().openSettings()
    s().edit("ask: [execute]\n")
    await s().save()
    const put = calls.find((c) => c.method === "PUT")
    expect(put?.body.path).toBe("policy/permissions.yaml")
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

  test("a local failure blocks saving without touching the network", async () => {
    await s().openSettings()
    await s().select("llm/deepseek.yaml")
    const before = calls.length
    s().edit("type: openai\nbase_url: https://x\napi_key: sk-leak\nmodels: []\n")

    expect(s().issues.length).toBeGreaterThan(0)
    expect(selectCanSave(s())).toBe(false)
    await s().save()
    expect(calls.length).toBe(before)
    expect(s().saveError).toContain("reference")
  })

  test("the rules follow the section of the selected file", async () => {
    await s().openSettings()
    await s().select("llm/deepseek.yaml")
    s().edit("api_key: sk-leak\n")
    expect(s().issues.some((i) => i.message.includes("reference"))).toBe(true)

    await s().select("policy/permissions.yaml")
    s().edit("api_key: sk-leak\n")
    expect(s().issues.some((i) => i.message.includes("unknown key"))).toBe(true)
  })

  test("editing clears the previous verdict", async () => {
    await s().openSettings()
    await s().select("llm/deepseek.yaml")
    s().edit("api_key: sk-leak\n")
    expect(s().issues.length).toBeGreaterThan(0)
    s().edit(PROVIDER)
    expect(s().issues).toEqual([])
    expect(s().serverIssues).toEqual([])
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
    expect(s().saveError).toContain("off, low, high, max")
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
    expect(s().selected).toBe("policy/permissions.yaml")
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
    expect(selectSelectedItem(s())?.path).toBe("policy/permissions.yaml")
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
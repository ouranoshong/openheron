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

/**
 * 配置编辑器的状态层。
 *
 * ★ 与 `store.test.ts` 同一套关注点：脚本级看不见、用户天天遇到的那几件。
 *   这里最要紧的三件是 ——
 *
 *   1. **换级别要重新读**。在用户级改一个被项目级遮蔽的文件是零效果的，
 *      而如果切级别只换列表不换内容，用户会以为改的是 A 其实是 B。
 *   2. **保存后 draft 必须跟着走**。不跟的话 dirty 永远为真、按钮永远亮着。
 *   3. **冲突不能自动合并**。两份都是对方在编辑，合并出来的第三份谁都没写。
 */

const POLICY = "allow: [read_file]\n"
const PROVIDER = "type: openai\nbase_url: https://x\napi_key: ${KEY}\nmodels:\n  - id: fast\n"

/** 出厂 agents/main.md 的真实形状 —— 含 `reasoning:` 与行尾注释。 */
const MAIN_MD = "---\nmodel: deepseek/flash\ndescription: root\nreasoning: high  # level\n---\nbody\n"

/** 磁盘上**真的存在**的那些文件。键是相对路径。 */
const ON_DISK: Record<string, string> = {
  "policy/permissions.yaml": POLICY,
  // ★ 夹具必须带 `reasoning:`：出厂配置就这么写。也正因为夹具里少了这一行，
  //   「合法的 reasoning 让文件存不进去」这个 bug 躲过了全部 17 条 E2E。
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
/** 后端那份「磁盘」—— 被写入改动。 */
let disk: Record<string, string> = {}
/** 覆盖某一类响应的钩子，默认全走真实路径。 */
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

    // ★ DELETE 的 URL 也带 query（?path=），所以必须**先**判方法 ——
    //   放在 startsWith 判据之后会落进读取分支，删不掉还返回 200。
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
  // 每个用例从同一个起点开始 —— 尤其 open / dirty 这些。
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
    // 一个空编辑器不如没有编辑器。
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
    // ★ 关掉时留着草稿看着贴心，实际后果是「我明明保存过，怎么变回去了」。
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
    // ★ 取消继承链之后没有「另一级的那份」了 —— 同一个路径出现两次
    //   就意味着还有一层没拆干净。
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
    // 明文密钥 —— 本地就该拦下，省一次往返。
    s().edit("type: openai\nbase_url: https://x\napi_key: sk-leak\nmodels: []\n")

    expect(s().issues.length).toBeGreaterThan(0)
    expect(selectCanSave(s())).toBe(false)
    await s().save()
    expect(calls.length).toBe(before)
    expect(s().saveError).toContain("reference")
  })

  test("the rules follow the section of the selected file", async () => {
    // ★ 同��串内容，选中 llm 与选中 policy 得到的是**不同的**规则 ——
    //   `api_key:` 在 policy 里是「未知键」，在 llm 里是「明文密钥」。
    //   判错分区会给出完全不相干的提示。
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
    // ★ 那个 bug 的验收。出厂 agents/main.md 就带 `reasoning: high`，
    //   它完全合法 —— 而原实现对它报一条 issue，save() 见 issue 就 return。
    //   症状不是「保存失败」而是「根本存不了」，用户唯一的出路是删掉
    //   那行正确配置。所以只测「保存成功」的 E2E 抓不到它。
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
    // ★ 跨文件的问题只有服务端看得见，所以这一步不是「优化掉」，是必须问。
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
    // ★ 草稿**留着** —— 用户要照着报错改它。
    expect(s().draft).toBe("BROKEN_MERGE")
  })

  test("after saving, the draft becomes the new saved value", async () => {
    // ★ 不跟的话 dirty 永远为真，按钮永远亮着 —— 而且第二次保存
    //   会把同一份内容再发一遍。
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
    // ★ 冲突不能自动合并：两份都是对方在编辑，合出来的第三份谁都没写过。
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
    // 「保存成功」与「已生效」在界面上长得一样，而那个区别是全部要害。
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
    // ★ 钩子必须在 openSettings **之后**设：install() 会把 hooks 清空。
    hooks.onDelete = () =>
      ({
        ok: false,
        status: 422,
        json: async () => ({ error: "agent main references openai/fast", code: "config_would_break_merge", retryable: false }),
      }) as Response

    await s().remove()
    expect(s().saveState).toBe("error")
    expect(s().serverIssues.join()).toContain("openai/fast")
    // ★ 被拒之后**不能**清掉选中项 —— 用户要照着报错去改它。
    expect(s().selected).toBe("policy/permissions.yaml")
  })
})

// ── selectors ─────────────────────────────────────────────────────────────

describe("selectors", () => {
  test("return module-level constants instead of fresh arrays", async () => {
    // ★ zustand 用 Object.is 比快照。空列表若每次造新数组，比较永远
    //   不相等 → 每次渲染都触发更新 → React 报
    //   "Maximum update depth exceeded"，整页白屏。
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
    // ★ 每次 filter 都返回**新数组** → 调用方必须 useShallow。
    //   这里只断言「它不是同一个引用」，不碰 React 的渲染行为。
    expect(selectItems(s())).not.toBe(selectItems(s()))
  })

  test("selectSelectedItem finds the selected path", async () => {
    await s().openSettings()
    expect(selectSelectedItem(s())?.path).toBe("policy/permissions.yaml")
  })

  test("selectIsEditable reads the category, not the item", async () => {
    // server 的条目 exists 且可读，但不可写 —— 依据是分区，不是文件。
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
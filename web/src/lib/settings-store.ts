/**
 * 配置编辑器的状态层。
 *
 * ★ 与 `store.ts` 分开是刻意的：会话 store 管的是「一份历史正在流式写进来」，
 *   而这里管的是「一个文件正在被改，还没保存」。两者的更新节奏与失败模式
 *   完全不同（前者每秒几十次 delta，后者一次一个用户动作），合成一个 store
 *   会让两边的 selector 都跟着对方变。
 *
 * ★ 草稿 (draft) 与已保存内容 (saved) **分开存**，且 `dirty` 由它们算出。
 *   不分开的话，「撤销」就无从实现 —— 而撤销是这个界面上最高频的补救动作。
 */

import { create } from "zustand"

import { ApiError } from "@/lib/api"
import {
  isSettingsError,
  settingsApi,
  SETTINGS_ERROR_CODES,
  type SettingsCategory,
  type SettingsItemBody,
  type SettingsItem,
  type SettingsManifest,
} from "@/lib/settings-api"
import { blocking, check, type Issue, type ReasoningCatalog } from "@/lib/settings-validate"

/** 保存的状态机。界面按它决定按钮与提示。 */
export type SaveState =
  | "idle"
  | "validating"
  | "saving"
  | "saved"
  | "conflict"
  | "error"

export interface SettingsState {
  open: boolean
  manifest?: SettingsManifest
  loading: boolean
  /** 清单本身拉不到时的错误（如未接线 → 503）。 */
  loadError?: string

  /** 当前选中的相对路径；null = 没选文件。 */
  selected?: string
  /** 服务端读到的内容（保存后的真值）。 */
  saved: string
  /** 用户正在编辑的内容。 */
  draft: string
  /** 读回时的 revision —— 保存时原样回传做乐观并发。 */
  revision: string
  exists: boolean
  hot: boolean

  /** 客户端预校验的结果（瞬时，不含跨文件判断）。 */
  issues: Issue[]
  /** 服务端试合并的报错（权威，但慢）。 */
  serverIssues: string[]

  saveState: SaveState
  /** 保存后是否已热生效；false 时界面必须说需重启。 */
  applied: boolean
  saveError?: string
  /** 冲突时的两版内容，供界面给「重载 / 覆盖」两个选择。 */
  conflict?: { yours: string; revision: string }

  // ── actions ────────────────────────────────────────────────────────────
  openSettings(): Promise<void>
  closeSettings(): void
  select(path: string): Promise<void>
  edit(text: string): void
  reloadFromDisk(): Promise<void>
  save(): Promise<void>
  remove(): Promise<void>
  newFile(path: string, content: string): Promise<void>
  clearMessages(): void
}

export const useSettings = create<SettingsState>((set, get) => ({
  open: false,
  loading: false,
  saved: "",
  draft: "",
  revision: "",
  exists: false,
  hot: true,
  issues: [],
  serverIssues: [],
  saveState: "idle",
  applied: false,

  async openSettings() {
    set({ open: true, loading: true, loadError: undefined })
    try {
      const manifest = await settingsApi.manifest()
      set({ manifest, loading: false })
      // 打开时若什么都没选，且有可编辑的文件，就选第一个 ——
      // 一个空编辑器不如没有编辑器。
      const first = firstEditable(manifest)
      if (first) await get().select(first)
    } catch (e) {
      set({ loading: false, loadError: describe(e) })
    }
  },

  closeSettings() {
    // ★ 关掉时**丢弃草稿**：下次打开要从磁盘重来。留着草稿看着贴心，
    //   实际后果是「我明明保存过，怎么又变回去了」。
    set({ open: false, selected: undefined, draft: "", saved: "", revision: "", issues: [], serverIssues: [], saveState: "idle" })
  },

  async select(path) {
    set({ selected: path, serverIssues: [], saveState: "idle", saveError: undefined })
    try {
      const item = await settingsApi.item(path)
      applyItem(set, path, item)
    } catch (e) {
      set({ saveState: "error", saveError: describe(e) })
    }
  },

  edit(text) {
    // ★ 必须用**函数式** set。
    //
    //   写成 `set({ draft: text, issues: currentIssues(get()) })` 时，
    //   对象字面量在 set 之前就求值完了，`get()` 拿到的是**旧** draft ——
    //   于是校验永远针对上一段内容跑，改完看不到任何报错。
    //   这个 bug 不会让界面崩，只会让校验「时灵时不灵」，极难定位。
    set((s) => ({
      draft: text,
      issues: s.selected ? check(s.selected, text, catalogOf(s)).issues : [],
      saveState: "idle",
      saveError: undefined,
      conflict: undefined,
    }))
  },

  async reloadFromDisk() {
    // ★ 冲突后的「重载」**丢掉草稿**而不是合并：两份都是对方在编辑，
    //   自动合并出来的第三份谁都没写过。
    await get().select(get().selected!)
    set({ conflict: undefined })
  },

  async save() {
    const { selected, draft, revision, issues } = get()
    if (!selected) return

    // ★ 本地没过就不发 —— 省一次往返，也避免把必然失败的请求打给服务端。
    //
    //   ★ 只看 **error**：hint 不阻断。这里原先判的是 `issues.length > 0`，
    //     而 Issue 当时没有严重级别 —— 于是**任何设了 `reasoning:` 的
    //     agents/*.md 都存不进去**，而那行配置完全合法。
    //     症状不是「保存失败」而是「根本存不了」，用户唯一的出路是
    //     删掉一行正确配置 —— 那是阻断，不是提示。
    const errors = blocking(issues)
    if (errors.length > 0) {
      set({
        saveState: "error",
        saveError: errors.map((i) => `line ${i.line}: ${i.message}`).join("\n"),
      })
      return
    }

    set({ saveState: "validating", serverIssues: [], saveError: undefined })
    try {
      await settingsApi.validate(selected, draft)
    } catch (e) {
      // ★ 跨文件的问题只有服务端能看见，所以这里不是「优化掉」，是必须问。
      set({ saveState: "error", serverIssues: [describe(e)], saveError: undefined })
      return
    }

    set({ saveState: "saving" })
    try {
      const res = get().exists
        ? await settingsApi.save(selected, draft, revision)
        : await settingsApi.create(selected, draft)

      set({
        saveState: "saved",
        applied: res.applied,
        revision: res.revision,
        saved: draft,
        // ★ draft 也跟着走：保存之后它就是新的「已保存」，否则 dirty
        //   会一直为真，按钮永远亮着。
        draft,
        serverIssues: [],
      })
      // 清单要跟着变：新建了一个文件，或某一级的存在性变了。
      await refreshManifest()
    } catch (e) {
      if (isSettingsError(e, SETTINGS_ERROR_CODES.conflict)) {
        set({ saveState: "conflict", conflict: { yours: draft, revision } })
        return
      }
      set({ saveState: "error", saveError: describe(e), serverIssues: [describe(e)] })
    }
  },

  async remove() {
    const { selected } = get()
    if (!selected) return
    set({ saveState: "saving", saveError: undefined })
    try {
      await settingsApi.remove(selected)
      set({ selected: undefined, draft: "", saved: "", revision: "", exists: false, saveState: "idle", applied: false })
      await refreshManifest()
    } catch (e) {
      set({ saveState: "error", saveError: describe(e), serverIssues: [describe(e)] })
    }
  },

  async newFile(path, content) {
    set({ saveState: "saving", saveError: undefined })
    try {
      await settingsApi.create(path, content)
      await refreshManifest()
      await get().select(path)
    } catch (e) {
      set({ saveState: "error", saveError: describe(e) })
    }
  },

  clearMessages() {
    set({ serverIssues: [], saveError: undefined, conflict: undefined })
  },
}))

// ── 内部 ──────────────────────────────────────────────────────────────────

type Set = (partial: Partial<SettingsState> | ((s: SettingsState) => Partial<SettingsState>)) => void

/**
 * catalogOf 取本次校验可用的思考档位词汇表。
 *
 * ★ 清单还没加载时返回模块级空常量 —— 不是 `{}` 字面量。
 *
 *   这个 store 的 selector 每次渲染都比引用；每次造新对象会让
 *   `Object.is` 永远判定「变了」，于是无限重渲染（AGENTS.md 记过这一类）。
 */
const NO_CATALOG: ReasoningCatalog = {}

/** catalogOf 取本次校验可用的思考档位词汇表。 */
function catalogOf(s: SettingsState): ReasoningCatalog {
  return s.manifest?.reasoningLevels ?? NO_CATALOG
}

/** applyItem 把服务端读到的一项铺进状态。 */
function applyItem(set: Set, path: string, item: SettingsItemBody): void {
  set((s) => ({
    saved: item.content,
    draft: item.content,
    revision: item.revision ?? "",
    exists: item.exists,
    hot: item.hot,
    issues: item.exists ? check(path, item.content, catalogOf(s)).issues : [],
    serverIssues: [],
    saveState: "idle",
    saveError: undefined,
    conflict: undefined,
  }))
}

/** firstEditable 找出第一个可编辑分区里的第一个文件。 */
function firstEditable(manifest: SettingsManifest | undefined): string | undefined {
  if (!manifest) return undefined
  const editable = new Set(manifest.categories.filter((c) => c.editable).map((c) => c.id))
  const item = manifest.items.find((i) => editable.has(i.path.split("/")[0] ?? ""))
  return item?.path
}

/** refreshManifest 重拉清单，让新建/删除后的列表与磁盘一致。 */
async function refreshManifest(): Promise<void> {
  const manifest = await settingsApi.manifest()
  useSettings.setState({ manifest })
}

function describe(e: unknown): string {
  if (e instanceof ApiError) return e.message
  return (e as Error).message ?? String(e)
}

// ── selectors ─────────────────────────────────────────────────────────────
//
// ★ 与 store.ts 同一纪律：每一条都只返回**引用稳定**的值。
//   zustand 用 Object.is 比较快照，selector 每次造新数组会永远不相等
//   → 每次渲染都触发更新 → React 报 "Maximum update depth exceeded"，白屏。

/** 模块级空常量。绝不在 selector 里写 `?? []`。 */
export const EMPTY_ITEMS: SettingsItem[] = []
export const EMPTY_CATEGORIES: SettingsCategory[] = []
const EMPTY_SERVER_ISSUES: string[] = []

export function selectItems(s: SettingsState): SettingsItem[] {
  if (!s.manifest) return EMPTY_ITEMS
  // 返回的是 filter 的结果，每次都是新数组 → 调用方**必须** useShallow。
  return s.manifest.items.filter((i) => i.exists)
}

export function selectCategories(s: SettingsState): SettingsCategory[] {
  return s.manifest?.categories ?? EMPTY_CATEGORIES
}

export function selectSelectedItem(s: SettingsState): SettingsItem | undefined {
  if (!s.selected) return undefined
  return s.manifest?.items.find((i) => i.path === s.selected)
}

export function selectServerIssues(s: SettingsState): string[] {
  return s.serverIssues.length > 0 ? s.serverIssues : EMPTY_SERVER_ISSUES
}

/** dirty —— 草稿与已保存内容不同。 */
export function selectDirty(s: SettingsState): boolean {
  return s.draft !== s.saved
}

/** canSave —— 本地**没有 error** 级问题、且没有正在保存。 */
export function selectCanSave(s: SettingsState): boolean {
  return (
    s.draft !== s.saved &&
    blocking(s.issues).length === 0 &&
    s.saveState !== "saving" &&
    s.saveState !== "validating"
  )
}

/** isEditableSelected —— 当前选中的分区能不能写。 */
export function selectIsEditable(s: SettingsState): boolean {
  const cat = s.manifest?.categories.find((c) => c.id === s.selected?.split("/")[0])
  return cat?.editable ?? false
}
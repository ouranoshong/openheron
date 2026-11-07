
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
import { blocking, check, EDITABLE_SECTIONS, type Issue, type ReasoningCatalog } from "@/lib/settings-validate"

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
  loadError?: string

  selected?: string
  saved: string
  draft: string
  revision: string
  exists: boolean
  hot: boolean

  issues: Issue[]
  serverIssues: string[]

  saveState: SaveState
  applied: boolean
  saveError?: string
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
      const first = firstEditable(manifest)
      if (first) await get().select(first)
    } catch (e) {
      set({ loading: false, loadError: describe(e) })
    }
  },

  closeSettings() {
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
    set((s) => ({
      draft: text,
      issues: s.selected ? check(s.selected, text, catalogOf(s), editableOf(s)).issues : [],
      saveState: "idle",
      saveError: undefined,
      conflict: undefined,
    }))
  },

  async reloadFromDisk() {
    await get().select(get().selected!)
    set({ conflict: undefined })
  },

  async save() {
    const { selected, draft, revision, issues } = get()
    if (!selected) return

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
        draft,
        serverIssues: [],
      })
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

type Set = (partial: Partial<SettingsState> | ((s: SettingsState) => Partial<SettingsState>)) => void

const NO_CATALOG: ReasoningCatalog = {}

function catalogOf(s: SettingsState): ReasoningCatalog {
  return s.manifest?.reasoningLevels ?? NO_CATALOG
}

// editableOf is the manifest's own answer to "which sections may be
// edited" — the server owns the partition table.
function editableOf(s: SettingsState): readonly string[] {
  const cats = s.manifest?.categories
  if (!cats) return EDITABLE_SECTIONS
  return cats.filter((c) => c.editable).map((c) => c.id)
}

function applyItem(set: Set, path: string, item: SettingsItemBody): void {
  set((s) => ({
    saved: item.content,
    draft: item.content,
    revision: item.revision ?? "",
    exists: item.exists,
    hot: item.hot,
    issues: item.exists ? check(path, item.content, catalogOf(s), editableOf(s)).issues : [],
    serverIssues: [],
    saveState: "idle",
    saveError: undefined,
    conflict: undefined,
  }))
}

function firstEditable(manifest: SettingsManifest | undefined): string | undefined {
  if (!manifest) return undefined
  const editable = new Set(manifest.categories.filter((c) => c.editable).map((c) => c.id))
  const item = manifest.items.find((i) => editable.has(i.path.split("/")[0] ?? ""))
  return item?.path
}

async function refreshManifest(): Promise<void> {
  const manifest = await settingsApi.manifest()
  useSettings.setState({ manifest })
}

function describe(e: unknown): string {
  if (e instanceof ApiError) return e.message
  return (e as Error).message ?? String(e)
}

// ── selectors ─────────────────────────────────────────────────────────────

export const EMPTY_ITEMS: SettingsItem[] = []
export const EMPTY_CATEGORIES: SettingsCategory[] = []
const EMPTY_SERVER_ISSUES: string[] = []

export function selectItems(s: SettingsState): SettingsItem[] {
  if (!s.manifest) return EMPTY_ITEMS
  return s.manifest.items.filter((i) => i.exists)
}

export function selectCategories(s: SettingsState): SettingsCategory[] {
  return s.manifest?.categories ?? EMPTY_CATEGORIES
}

/**
 * The sections the client may edit.
 *
 * ★ The **manifest** is the truth: the server owns the partition table, and
 *   a client that keeps its own list is a second truth — the day a
 *   partition is added, the client says it does not exist.
 *
 * The returned array is new on every call, so a component that selects it
 * needs `useShallow`.
 */
export function selectEditableSections(s: SettingsState): readonly string[] {
  const cats = s.manifest?.categories
  if (!cats) return EDITABLE_SECTIONS
  return cats.filter((c) => c.editable).map((c) => c.id)
}

export function selectSelectedItem(s: SettingsState): SettingsItem | undefined {
  if (!s.selected) return undefined
  return s.manifest?.items.find((i) => i.path === s.selected)
}

export function selectServerIssues(s: SettingsState): string[] {
  return s.serverIssues.length > 0 ? s.serverIssues : EMPTY_SERVER_ISSUES
}

export function selectDirty(s: SettingsState): boolean {
  return s.draft !== s.saved
}

export function selectCanSave(s: SettingsState): boolean {
  return (
    s.draft !== s.saved &&
    blocking(s.issues).length === 0 &&
    s.saveState !== "saving" &&
    s.saveState !== "validating"
  )
}

export function selectIsEditable(s: SettingsState): boolean {
  const cat = s.manifest?.categories.find((c) => c.id === s.selected?.split("/")[0])
  return cat?.editable ?? false
}
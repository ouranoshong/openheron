
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
import { blocking, EDITABLE_SECTIONS, issueLine, type Issue } from "@/lib/settings-validate"

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
  // newFile resolves to the server's sentence when the file was refused, and
  // to undefined when it was created. ★ The dialog needs the difference: it
  // must stay open on a refusal, and the reason for the refusal is the
  // server's (`spec.ValidatePath`), not a copy of it kept here.
  newFile(path: string, content: string): Promise<string | undefined>
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
    cancelPendingValidate()
    set({ open: false, selected: undefined, draft: "", saved: "", revision: "", issues: [], serverIssues: [], saveState: "idle" })
  },

  async select(path) {
    cancelPendingValidate()
    set({ selected: path, serverIssues: [], saveState: "idle", saveError: undefined })
    try {
      const item = await settingsApi.item(path)
      applyItem(set, item)
    } catch (e) {
      set({ saveState: "error", saveError: describe(e) })
    }
  },

  edit(text) {
    set({ draft: text, saveState: "idle", saveError: undefined, conflict: undefined })
    scheduleValidate(set, get)
  },

  async reloadFromDisk() {
    await get().select(get().selected!)
    set({ conflict: undefined })
  },

  async save() {
    const { selected, draft, revision } = get()
    if (!selected) return

    // ★ The debounced verdict may be older than the draft, or may not have
    //   arrived at all — a save clicked inside the debounce window would
    //   otherwise write whatever the last keystroke said was fine. So the
    //   save asks again, now, and cancels whatever was in flight.
    cancelPendingValidate()
    const seq = validateSeq

    set({ saveState: "validating", serverIssues: [], saveError: undefined })
    let issues: Issue[]
    try {
      const res = await settingsApi.validate(selected, draft)
      issues = res.issues ?? []
    } catch (e) {
      set({ saveState: "error", serverIssues: [describe(e)], saveError: undefined })
      return
    }
    if (seq !== validateSeq || get().selected !== selected) return
    set({ issues })

    const errors = blocking(issues)
    if (errors.length > 0) {
      set({ saveState: "error", saveError: errors.map(issueLine).join("\n") })
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
      return undefined
    } catch (e) {
      const reason = describe(e)
      set({ saveState: "error", saveError: reason })
      return reason
    }
  },

  clearMessages() {
    set({ serverIssues: [], saveError: undefined, conflict: undefined })
  },
}))

type Set = (partial: Partial<SettingsState> | ((s: SettingsState) => Partial<SettingsState>)) => void

// ── validation is a round trip ────────────────────────────────────────────

const VALIDATE_DEBOUNCE_MS = 250

// ★ A file that just loaded from disk is clean by construction: the tree it
// came from is the tree the process is running. Asking the server to say so
// again would spend a round trip per file opened, and the answer would be the
// same. The first keystroke is what can make it dirty.
function applyItem(set: Set, item: SettingsItemBody): void {
  set({
    saved: item.content,
    draft: item.content,
    revision: item.revision ?? "",
    exists: item.exists,
    hot: item.hot,
    issues: [],
    serverIssues: [],
    saveState: "idle",
    saveError: undefined,
    conflict: undefined,
  })
}

// validateSeq stamps every request; validateTimer is the one pending.
//
// ★ Both exist for the same reason: the answer to "is this file valid" is
//   about a **draft**, and by the time it arrives the draft may be gone. A
//   reply is applied only if it is still the newest (seq) and still about the
//   selected file (path). Without the stamp the slower of two replies wins,
//   which is the one about the older text.
let validateSeq = 0
let validateTimer: ReturnType<typeof setTimeout> | undefined

function cancelPendingValidate(): void {
  validateSeq++
  if (validateTimer !== undefined) {
    clearTimeout(validateTimer)
    validateTimer = undefined
  }
}

function scheduleValidate(set: Set, get: () => SettingsState): void {
  const path = get().selected
  if (!path) return
  cancelPendingValidate()
  const seq = validateSeq
  validateTimer = setTimeout(() => {
    validateTimer = undefined
    void requestValidate(set, get, path, seq)
  }, VALIDATE_DEBOUNCE_MS)
}

async function requestValidate(
  set: Set,
  get: () => SettingsState,
  path: string,
  seq: number,
): Promise<void> {
  const content = get().draft
  let issues: Issue[]
  try {
    const res = await settingsApi.validate(path, content)
    issues = res.issues ?? []
  } catch (e) {
    // ★ A refusal the endpoint itself could not take — a path outside the
    //   tree, most of all — is still an answer about this draft. Reporting it
    //   as an issue keeps one shape for "why can this not be saved".
    issues = [{ line: 0, message: describe(e), severity: "error" }]
  }
  if (seq !== validateSeq || get().selected !== path) return
  set({ issues })
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

/**
 * Whether the Save button is offered.
 *
 * ★ A partition this client may not write is not saveable, **however clean the
 *   draft looks**. `server/` is now readable (§ the read gate), so its content
 *   arrives in the editor and a keystroke would otherwise make `draft !==
 *   saved` and light up a button whose request the server refuses — an error
 *   the user did nothing to deserve. The category says which it is; the server
 *   is the one that decides, and it says "not editable".
 */
export function selectCanSave(s: SettingsState): boolean {
  return (
    selectIsEditable(s) &&
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
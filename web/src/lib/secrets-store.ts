
import { create } from "zustand"

import { ApiError } from "@/lib/api"
import { secretsApi, type SecretGroup, type SecretItem } from "@/lib/secrets-api"

export interface SecretsState {
  groups: SecretGroup[]
  loading: boolean
  error?: string

  /**
   * The entry the panel shows, as "group:name".
   *
   * ★ One key for both parts, because the pair **is** the identity: the
   *   name alone is not unique — a provider and an MCP server may share it,
   *   and the two live in different namespaces.
   */
  selected?: string

  /** The entry being written or removed, as "group:name". */
  busy?: string

  load(): Promise<void>
  select(key?: string): void
  set(group: string, name: string, value: string): Promise<void>
  remove(group: string, name: string): Promise<void>
  removeGroup(group: string): Promise<void>
  clearError(): void
}

export const EMPTY_SECRET_GROUPS: SecretGroup[] = []

export const useSecrets = create<SecretsState>((set, get) => ({
  groups: EMPTY_SECRET_GROUPS,
  loading: false,

  async load() {
    set({ loading: true, error: undefined })
    try {
      const res = await secretsApi.list()
      set({ groups: res.groups ?? EMPTY_SECRET_GROUPS, loading: false })
      // ★ The selection follows the list when it is gone: a panel that keeps
      //   showing an entry that no longer exists reads as a broken panel.
      const selected = get().selected
      if (selected && !findItem(res.groups ?? EMPTY_SECRET_GROUPS, selected)) {
        set({ selected: undefined })
      }
    } catch (e) {
      set({ loading: false, error: describe(e) })
    }
  },

  select(key) {
    set({ selected: key, error: undefined })
  },

  async set(group, name, value) {
    const key = `${group}:${name}`
    set({ busy: key, error: undefined })
    try {
      await secretsApi.set(group, name, value)
      await get().load()
    } catch (e) {
      set({ error: describe(e) })
    } finally {
      set({ busy: undefined })
    }
  },

  async remove(group, name) {
    const key = `${group}:${name}`
    set({ busy: key, error: undefined })
    try {
      await secretsApi.remove(group, name)
      await get().load()
    } catch (e) {
      set({ error: describe(e) })
    } finally {
      set({ busy: undefined })
    }
  },

  async removeGroup(group) {
    set({ busy: `${group}:`, error: undefined })
    try {
      await secretsApi.removeGroup(group)
      await get().load()
    } catch (e) {
      set({ error: describe(e) })
    } finally {
      set({ busy: undefined })
    }
  },

  clearError() {
    set({ error: undefined })
  },
}))

// ── selectors ─────────────────────────────────────────────────────────────

export function selectSecretGroups(s: SecretsState): SecretGroup[] {
  return s.groups
}

export function selectSelectedSecret(s: SecretsState): SecretItem | undefined {
  if (!s.selected) return undefined
  return findItem(s.groups, s.selected)?.item
}

export function selectSelectedGroup(s: SecretsState): SecretGroup | undefined {
  if (!s.selected) return undefined
  return findItem(s.groups, s.selected)?.group
}

export function selectSecretError(s: SecretsState): string | undefined {
  return s.error
}

// findItem locates one entry by its "group:name" key.
function findItem(
  groups: SecretGroup[],
  key: string,
): { group: SecretGroup; item: SecretItem } | undefined {
  const sep = key.indexOf(":")
  if (sep < 0) return undefined
  const groupId = key.slice(0, sep)
  const name = key.slice(sep + 1)
  const group = groups.find((g) => g.id === groupId)
  if (!group) return undefined
  const item = group.items.find((i) => i.name === name)
  if (!item) return undefined
  return { group, item }
}

function describe(e: unknown): string {
  if (e instanceof ApiError) return e.message
  return (e as Error).message ?? String(e)
}

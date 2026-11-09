
import { ApiError } from "@/lib/api"
import type { Issue as SettingsIssue } from "@/lib/settings-validate"

export interface SettingsCategory {
  id: string
  editable: boolean
  hot: boolean
  note?: string
}

export interface SettingsItem {
  path: string
  exists: boolean
  hot: boolean
  revision?: string
}

export interface SettingsManifest {
  categories: SettingsCategory[]
  items: SettingsItem[]
  templates: Record<string, string>
  modelRefs: string[]
  reasoningLevels: Record<string, string[]>
  configVersion: string
}

export interface SettingsItemBody {
  path: string
  content: string
  exists: boolean
  revision?: string
  hot: boolean
}

export interface SettingsWriteResult {
  path: string
  revision: string
  configVersion: string
  applied: boolean
  requiresRestart?: boolean
  note?: string
}

export interface SettingsValidateResult {
  ok: boolean
  // ★ `issues`, not a list of strings: the server says which line, and a
  //   client that only gets sentences cannot put a marker beside it.
  issues?: SettingsIssue[]
  configVersion: string
}

export const SETTINGS_ERROR_CODES = {
  pathInvalid: "config_path_invalid",
  secretLiteral: "config_secret_literal",
  wouldBreak: "config_would_break_merge",
  conflict: "config_write_conflict",
} as const

export function isSettingsError(err: unknown, code: string): boolean {
  return err instanceof ApiError && err.code === code
}

const query = (path: string): string => `?path=${encodeURIComponent(path)}`

async function call<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  })
  if (!res.ok) {
    let body: ConstructorParameters<typeof ApiError>[1]
    try {
      body = (await res.json()) as never
    } catch {
      body = { error: res.statusText, code: "unknown", retryable: res.status >= 500 }
    }
    throw new ApiError(res.status, body)
  }
  return (await res.json()) as T
}

export const settingsApi = {
  manifest: () => call<SettingsManifest>("/v2/settings"),

  item: (path: string) => call<SettingsItemBody>(`/v2/settings/item${query(path)}`),

  save: (path: string, content: string, baseRevision: string) =>
    call<SettingsWriteResult>("/v2/settings/item", {
      method: "PUT",
      body: JSON.stringify({ path, content, baseRevision }),
    }),

  create: (path: string, content: string) =>
    call<SettingsWriteResult>("/v2/settings/item", {
      method: "POST",
      body: JSON.stringify({ path, content, baseRevision: "" }),
    }),

  remove: (path: string) =>
    call<SettingsWriteResult>(`/v2/settings/item${query(path)}`, { method: "DELETE" }),

  validate: (path: string, content: string) =>
    call<SettingsValidateResult>("/v2/settings/validate", {
      method: "POST",
      body: JSON.stringify({ path, content }),
    }),
}
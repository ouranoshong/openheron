
import { ApiError } from "@/lib/api"

// The secret surface, as the client sees it.
//
// ★ Two namespaces, one shape: `llm` holds the model keys, `mcp` holds the
//   servers' tokens. The group is in the path, so "delete everything" is
//   scoped by the request itself and the client cannot get it wrong.
//
// ★ **No value, ever.** The list says where a key is and whether it is in
//   force. The write takes a value and the answer does not carry one back —
//   a response that echoed the secret would put it in the browser's memory,
//   in a devtools log, and in a screenshot.

export interface SecretItem {
  name: string
  /** The keyring holds it. */
  stored: boolean
  /** Where it is stored: "keyring:llm:deepseek". */
  source: string
  /** Which source is in force: "env:…", "keyring:…", "file:…" or "none". */
  effective: string
  /** The environment overrides a stored key. */
  shadowed: boolean
  /** The variable that overrides it: "LLM_DEEPSEEK_API_KEY". */
  envVar: string
}

export interface SecretGroup {
  id: string
  label: string
  items: SecretItem[]
}

export interface SecretList {
  groups: SecretGroup[]
}

export interface SecretWriteResult {
  group: string
  name: string
  stored: boolean
  source: string
  effective: string
  shadowed: boolean
  envVar: string
}

export interface SecretDeleteResult {
  group: string
  name?: string
  deleted: number
}

export const SECRETS_ERROR_CODES = {
  unavailable: "secret_unavailable",
  invalidBody: "invalid_body",
} as const

export function isSecretsError(err: unknown, code: string): boolean {
  return err instanceof ApiError && err.code === code
}

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

// ★ The value goes in the **body**, never in the path: a path lands in the
//   access log, and a secret in an access log is a secret in a file.
export const secretsApi = {
  list: () => call<SecretList>("/v2/secrets"),

  set: (group: string, name: string, value: string) =>
    call<SecretWriteResult>(`/v2/secrets/${encodeURIComponent(group)}/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify({ value }),
    }),

  remove: (group: string, name: string) =>
    call<SecretDeleteResult>(`/v2/secrets/${encodeURIComponent(group)}/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),

  removeGroup: (group: string) =>
    call<SecretDeleteResult>(`/v2/secrets/${encodeURIComponent(group)}`, { method: "DELETE" }),
}

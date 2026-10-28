/**
 * `/v2/settings` 的类型化客户端。
 *
 * ★ 与 `api.ts` 的会话那一套刻意分开：配置是低频、重状态的操作，
 *   它的错误分类（路径 / 密钥 / 合并 / 冲突）与会话那几个
 *   （模型错误 / 中断身份）没有任何交集。合成一个客户端会让
 *   `ApiError.code` 变成一张谁也读不完的表。
 */

import { ApiError } from "@/lib/api"

export interface SettingsCategory {
  id: string
  editable: boolean
  /** 保存后是否热生效。为 false 时界面必须提示需重启。 */
  hot: boolean
  note?: string
}

export interface SettingsItem {
  path: string
  /** 是否真有这个文件。为 false 时它是「可新建」而不是「读不出来」。 */
  exists: boolean
  hot: boolean
  /** 写入的乐观并发键；文件不存在时为空串。 */
  revision?: string
}

export interface SettingsManifest {
  categories: SettingsCategory[]
  items: SettingsItem[]
  /** 供「新建」用的骨架；键是分区 id。 */
  templates: Record<string, string>
  modelRefs: string[]
  /**
   * 每个 modelRef 声明的思考档位词汇表（键 = modelRef）。
   *
   * ★ 没有声明词汇表的模型**不在表里**（不是空数组）——
   *   「这个模型不支持思考」与「还没查到」是两种处置。
   */
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
  /** 保存后是否热生效 —— 由 watcher 负责应用，本端点只写不应用。 */
  applied: boolean
  requiresRestart?: boolean
  note?: string
}

export interface SettingsValidateResult {
  ok: boolean
  /** 每条都带文件与行号（单文件错误）或文件路径（跨文件错误）。 */
  errors?: string[]
  configVersion: string
}

/** 服务端的四类写入失败。判定必须走 code，不能匹配文本。 */
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

  /**
   * baseRevision 是**必填**语义：空串表示「文件当前不存在」即新建。
   *
   * ★ 调用方若图省事传 `undefined`，后端会把它当成「不检查并发」，
   *   于是用户手改的版本被静默覆盖 —— 而那正是它要防的事。
   *   所以这里没有可选参数。
   */
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
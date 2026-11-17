import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import { ApiError } from "../src/lib/api"
import {
  selectAuthorizationConfirmed,
  selectAuthorizationMode,
  useStore,
  type StoreState,
} from "../src/lib/store"

const A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

// held is what the server holds for the session: the mode, and whether this
// session has already confirmed the full one.
//
// ★ The client's copy is never the authority — every answer comes from here.
const held = new Map<string, { mode: "default" | "trusted" | "full"; confirmed: boolean }>()

const meta = (id: string) => ({
  threadId: id,
  title: "Alpha",
  createdAt: "2026-10-12T10:00:00Z",
  updatedAt: "2026-10-12T10:05:00Z",
  messageCount: 0,
  state: "idle" as const,
  hasPendingApproval: false,
  agentId: "main",
  projectId: "",
  authorizationMode: held.get(id)?.mode ?? ("default" as const),
  authorizationConfirmed: held.get(id)?.confirmed ?? false,
})

const store = new Map<string, string>()
;(globalThis as any).localStorage = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => store.clear(),
  key: (i: number) => [...store.keys()][i] ?? null,
  get length() {
    return store.size
  },
}

type Patch = { url: string; body: unknown }

// patches is every write the client made, in order.
let patches: Patch[] = []
// created is every session the client asked the server to open, in order.
let created: {
  agentId?: string
  projectId?: string
  authorizationMode?: string
  authorizationConfirmed?: boolean
}[] = []
// refuse makes the server refuse every write.
let refuse = false

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function installFetch(): void {
  patches = []
  created = []
  const real = globalThis.fetch
  globalThis.fetch = (async (input: any, init?: RequestInit) => {
    const url = typeof input === "string" ? input : String(input?.url ?? "")
    const method = init?.method ?? "GET"

    if (url === "/v2/sessions") {
      if (method === "POST") {
        const body = (init?.body ? JSON.parse(String(init.body)) : {}) as {
          agentId?: string
          projectId?: string
          authorizationMode?: string
          authorizationConfirmed?: boolean
        }
        created.push(body)
        // ★ The server checks the mode the way the real one does: a name it
        //   does not know is refused, and `full` needs the confirmation.
        const mode = body.authorizationMode ?? ""
        if (mode && mode !== "default" && mode !== "trusted" && mode !== "full") {
          return json({ error: `unknown mode: ${mode}`, code: "invalid_body", retryable: false }, 400)
        }
        if (mode === "full" && body.authorizationConfirmed !== true) {
          return json(
            {
              error: "full authorization needs an explicit confirmation",
              code: "confirmation_required",
              retryable: false,
            },
            409,
          )
        }
        const id = `created-${created.length}`
        held.set(id, {
          mode: (mode || "default") as "default" | "trusted" | "full",
          confirmed: mode === "full",
        })
        return json(meta(id), 201)
      }
      return json({ items: [meta(A)] })
    }
    if (url === "/v2/run") return json({}, 200)
    if (url.endsWith("/messages")) {
      return json({ threadId: A, state: "idle", messages: [] })
    }
    if (url.endsWith("/authorization") && method === "PATCH") {
      const id = url.split("/")[3]!
      const body = (init?.body ? JSON.parse(String(init.body)) : {}) as {
        mode?: string
        confirm?: boolean
      }
      patches.push({ url, body })
      if (refuse) {
        return json({ error: "the server refused the change", code: "internal", retryable: false }, 500)
      }
      const mode = body.mode ?? ""
      if (mode !== "default" && mode !== "trusted" && mode !== "full") {
        return json({ error: `unknown mode: ${mode}`, code: "invalid_body", retryable: false }, 400)
      }
      const current = held.get(id) ?? { mode: "default" as const, confirmed: false }
      // ★ The server refuses `full` until this session confirmed it.
      if (mode === "full" && !current.confirmed && body.confirm !== true) {
        return json(
          {
            error: "full authorization needs an explicit confirmation",
            code: "confirmation_required",
            retryable: false,
          },
          409,
        )
      }
      held.set(id, {
        mode: mode as "default" | "trusted" | "full",
        confirmed: current.confirmed || mode === "full",
      })
      return json(meta(id))
    }
    return real(input, init)
  }) as typeof fetch
}

function reset(): void {
  held.clear()
  held.set(A, { mode: "default", confirmed: false })
  refuse = false
  localStorage.clear()
  useStore.setState({
    sessions: { [A]: { meta: meta(A), loaded: true } },
    order: [A],
    activeId: A,
    pendingAuthorizationMode: null,
    pendingAuthorizationConfirmed: false,
  } as Partial<StoreState>)
}

// settle waits for the store to stop moving.
async function settle(): Promise<void> {
  for (let i = 0; i < 50; i++) {
    await new Promise((r) => setTimeout(r, 2))
  }
}

beforeEach(() => {
  installFetch()
  reset()
})
afterEach(() => reset())

describe("setAuthorizationMode —— 改档位", () => {
  test("改完之后 meta 跟着变", async () => {
    await useStore.getState().setAuthorizationMode("trusted")

    expect(useStore.getState().sessions[A]!.meta.authorizationMode).toBe("trusted")
    expect(selectAuthorizationMode(useStore.getState())).toBe("trusted")
    expect(patches).toHaveLength(1)
    expect(patches[0]!.url).toBe(`/v2/sessions/${A}/authorization`)
    expect(patches[0]!.body).toEqual({ mode: "trusted" })
  })

  test("★ 调用失败时旧值不变 —— 界面不自己乐观更新", async () => {
    refuse = true

    await expect(useStore.getState().setAuthorizationMode("trusted")).rejects.toThrow()

    expect(useStore.getState().sessions[A]!.meta.authorizationMode).toBe("default")
    expect(selectAuthorizationMode(useStore.getState())).toBe("default")
  })

  test("★ 409 之后带 confirm 重发 —— 确认过的那一次才落档", async () => {
    // The first attempt: no confirmation, so the server refuses it.
    let refused: unknown
    try {
      await useStore.getState().setAuthorizationMode("full")
    } catch (e) {
      refused = e
    }
    expect(refused).toBeInstanceOf(ApiError)
    expect((refused as ApiError).code).toBe("confirmation_required")
    expect((refused as ApiError).status).toBe(409)
    // ★ The refusal changed nothing: the mode is still the old one.
    expect(useStore.getState().sessions[A]!.meta.authorizationMode).toBe("default")
    expect(patches[0]!.body).toEqual({ mode: "full" })

    // The second attempt: the user confirmed it.
    await useStore.getState().setAuthorizationMode("full", true)

    expect(patches).toHaveLength(2)
    expect(patches[1]!.body).toEqual({ mode: "full", confirm: true })
    expect(useStore.getState().sessions[A]!.meta.authorizationMode).toBe("full")
    expect(selectAuthorizationConfirmed(useStore.getState())).toBe(true)
  })

  test("没有会话时不发请求", async () => {
    useStore.setState({ activeId: null })

    await useStore.getState().setAuthorizationMode("trusted")

    expect(patches).toHaveLength(0)
  })
})

describe("欢迎页上的档位 —— 还没有会话的时候", () => {
  test("★ 暂存下来，请求一个也不发", async () => {
    useStore.setState({ activeId: null })

    await useStore.getState().setAuthorizationMode("trusted")

    // ★ 没有会话就没有东西可改：请求一个也不该发。
    expect(patches).toHaveLength(0)
    expect(useStore.getState().pendingAuthorizationMode).toBe("trusted")
    // ★ 按钮上显示的就是暂存的那个档位。
    expect(selectAuthorizationMode(useStore.getState())).toBe("trusted")
  })

  test("★ 确认过的那一次也暂存下来", async () => {
    useStore.setState({ activeId: null })

    await useStore.getState().setAuthorizationMode("full", true)

    expect(useStore.getState().pendingAuthorizationMode).toBe("full")
    expect(useStore.getState().pendingAuthorizationConfirmed).toBe(true)
    expect(selectAuthorizationConfirmed(useStore.getState())).toBe(true)
  })

  test("★ 换了别的档位不收回确认 —— 人已经说过一次了", async () => {
    useStore.setState({ activeId: null })

    await useStore.getState().setAuthorizationMode("full", true)
    await useStore.getState().setAuthorizationMode("trusted")

    expect(useStore.getState().pendingAuthorizationMode).toBe("trusted")
    // ★ 确认是关于人的事实，不是关于某一个档位的事实。
    expect(useStore.getState().pendingAuthorizationConfirmed).toBe(true)
  })

  test("★ 建会话时把暂存的档位带上", async () => {
    useStore.setState({ activeId: null })
    await useStore.getState().setAuthorizationMode("trusted")

    await useStore.getState().send("hello")
    await settle()

    expect(created).toHaveLength(1)
    expect(created[0]!.authorizationMode).toBe("trusted")
    // ★ 暂存花掉了：这个会话带着自己的档位，下一个会话从头开始。
    expect(useStore.getState().pendingAuthorizationMode).toBeNull()
  })

  test("★ full 的确认跟着建会话的请求走", async () => {
    useStore.setState({ activeId: null })
    await useStore.getState().setAuthorizationMode("full", true)

    await useStore.getState().send("hello")
    await settle()

    expect(created[0]!.authorizationMode).toBe("full")
    expect(created[0]!.authorizationConfirmed).toBe(true)
  })

  test("★ 服务端说不行就不行 —— 没有确认的 full 建不出来", async () => {
    useStore.setState({ activeId: null })
    // 直接改暂存，绕开客户端的确认框：服务端照样要那个确认。
    useStore.setState({ pendingAuthorizationMode: "full" })

    await useStore.getState().send("hello")
    await settle()

    expect(created).toHaveLength(1)
    // ★ 服务端的理由落在那个还没出生的会话上。
    const failed = Object.values(useStore.getState().sessions).find((e) => e.convo?.error)
    expect(failed?.convo?.error?.message ?? "").toContain("confirmation")
  })
})

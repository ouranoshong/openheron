import { afterEach, beforeEach, describe, expect, test } from "bun:test"

import type { OperationView, ThreadMeta } from "../src/lib/api"
import { EMPTY_OPERATIONS, selectOperations, useStore, type StoreState } from "../src/lib/store"

const A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const B = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

const LINES: OperationView[] = [
  {
    seq: 1,
    at: "2026-11-16T12:39:50.1Z",
    kind: "session.created",
    actor: "system",
    agentId: "main",
  },
  {
    seq: 2,
    at: "2026-11-16T12:40:02.4Z",
    kind: "run.started",
    actor: "user",
    runId: "9c1d2e3f",
    turnKind: "query",
  },
]

function threadMeta(id: string): ThreadMeta {
  return {
    threadId: id,
    title: "Alpha",
    createdAt: "2026-11-16T12:00:00Z",
    updatedAt: "2026-11-16T12:00:00Z",
    messageCount: 0,
    state: "idle",
    hasPendingApproval: false,
    agentId: "main",
    projectId: "",
    authorizationMode: "default",
    authorizationConfirmed: false,
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

beforeEach(() => {
  globalThis.fetch = (async (input: any) => {
    const url = typeof input === "string" ? input : String(input?.url ?? "")
    if (url.endsWith("/operations")) {
      return json({ threadId: A, total: LINES.length, items: LINES })
    }
    return new Response(null, { status: 204 })
  }) as typeof fetch
  useStore.setState({
    sessions: {},
    order: [],
    activeId: null,
  } as Partial<StoreState>)
})

afterEach(() => {
  useStore.setState({ sessions: {}, order: [], activeId: null } as Partial<StoreState>)
})

describe("操作日志的 selector", () => {
  test("★ 同一个会话取两次，是同一个数组", () => {
    useStore.setState({
      sessions: { [A]: { meta: threadMeta(A), loaded: true, operations: LINES } },
    } as Partial<StoreState>)

    const first = selectOperations(useStore.getState(), A)
    const second = selectOperations(useStore.getState(), A)

    // ★ A selector that built a new array on every call would make
    //   Object.is report "changed" every time, and the store would
    //   re-render forever.
    expect(Object.is(first, second)).toBe(true)
    expect(first).toHaveLength(2)
  })

  test("★ 没有日志时，答案是模块级那个空常量", () => {
    useStore.setState({
      sessions: { [B]: { meta: threadMeta(B), loaded: true } },
    } as Partial<StoreState>)

    const first = selectOperations(useStore.getState(), B)
    const second = selectOperations(useStore.getState(), B)

    expect(Object.is(first, EMPTY_OPERATIONS)).toBe(true)
    expect(Object.is(first, second)).toBe(true)
  })

  test("★ 会话根本不在 store 里，也是同一个空常量", () => {
    expect(Object.is(selectOperations(useStore.getState(), "nope"), EMPTY_OPERATIONS)).toBe(true)
  })
})

describe("读日志", () => {
  test("★ 服务器给的行落在会话自己身上", async () => {
    useStore.setState({
      sessions: { [A]: { meta: threadMeta(A), loaded: true } },
    } as Partial<StoreState>)

    await useStore.getState().loadOperations(A)

    expect(selectOperations(useStore.getState(), A)).toHaveLength(2)
    expect(selectOperations(useStore.getState(), A)[0]?.kind).toBe("session.created")
  })
})

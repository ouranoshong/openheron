import { describe, expect, test } from "bun:test"

import { reduceEvent, type AgEvent } from "../src/lib/events"
import { seedFromHistory } from "../src/lib/store"
import { selectActiveContext, type StoreState } from "../src/lib/store"
import { emptyConversation, type ConversationState } from "../src/lib/types"

function contextEvent(value: Record<string, unknown>): AgEvent {
  return { type: "CUSTOM", name: "context_usage", value } as AgEvent
}

// The meter only moves if the live event reaches the reducer.
test("context_usage 事件记下该 agent 的占用", () => {
  const state = reduceEvent(emptyConversation(), contextEvent({
    agent: "main",
    provider: "deepseek",
    model: "flash",
    used_tokens: 18422,
    context_window: 131072,
    summarize_at: 104858,
    updated_at: "2026-10-29T13:04:11.512Z",
  }))

  const usage = state.context?.main
  expect(usage?.usedTokens).toBe(18422)
  expect(usage?.contextWindow).toBe(131072)
  expect(usage?.summarizeAt).toBe(104858)
  expect(usage?.provider).toBe("deepseek")
  expect(usage?.model).toBe("flash")
})

// One agent's event must not touch the others.
test("一个 agent 的事件不动别人的占用", () => {
  let state: ConversationState = reduceEvent(emptyConversation(), contextEvent({
    agent: "main",
    used_tokens: 100,
    context_window: 1000,
  }))
  state = reduceEvent(state, contextEvent({
    agent: "coder",
    used_tokens: 200,
    context_window: 2000,
  }))

  expect(state.context?.main?.usedTokens).toBe(100)
  expect(state.context?.coder?.usedTokens).toBe(200)
})

// A window of zero means "not configured" — it must not become a ratio.
test("没有窗口时占用照报，窗口是零", () => {
  const state = reduceEvent(emptyConversation(), contextEvent({
    agent: "main",
    used_tokens: 4200,
  }))
  expect(state.context?.main?.usedTokens).toBe(4200)
  expect(state.context?.main?.contextWindow).toBe(0)
})

// An event without an agent says nothing about any agent.
test("没有 agent 的事件被忽略", () => {
  const state = reduceEvent(emptyConversation(), contextEvent({ used_tokens: 42 }))
  expect(state.context).toBeUndefined()
})

// The history carries the same numbers, so a refresh does not empty the
// meter.
test("历史水合把占用带进来", () => {
  const convo = seedFromHistory([], [], {
    promptTokens: 100,
    completionTokens: 20,
    totalTokens: 120,
    context: {
      main: {
        agent: "main",
        usedTokens: 120,
        contextWindow: 131072,
      },
    },
  } as never)

  expect(convo.context?.main?.usedTokens).toBe(120)
  expect(convo.context?.main?.contextWindow).toBe(131072)
})

// The selector hands back the map the store already holds: a new object here
// would make every render look like a change.
describe("selectActiveContext", () => {
  const context = { main: { agent: "main", usedTokens: 120, contextWindow: 131072 } }
  const state = {
    activeId: "a",
    sessions: { a: { convo: { context } } },
  } as unknown as StoreState

  test("返回 store 自己持有的那个 map", () => {
    expect(selectActiveContext(state)).toBe(context)
  })

  test("两次取到的是同一个引用", () => {
    expect(selectActiveContext(state)).toBe(selectActiveContext(state))
  })

  test("没有会话时是 undefined，而不是空数组", () => {
    const empty = { activeId: undefined, sessions: {} } as unknown as StoreState
    expect(selectActiveContext(empty)).toBeUndefined()
  })
})

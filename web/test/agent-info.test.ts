import { afterEach, describe, expect, test } from "bun:test"

import {
  badgeFromAgents,
  checkConfigChanged,
  loadAgentBadge,
  resetAgentBadgeCache,
  selectableAgents,
} from "../src/lib/agent-info"
import type { AgentModelView, AgentView, AgentsView } from "../src/lib/api"

function makeAgent(id: string, model: Partial<AgentModelView> = {}): AgentView {
  return {
    id,
    description: `${id} description`,
    model: {
      provider: `${id}-provider`,
      model: `${id}-model`,
      reasoning: "high",
      reasoningAvailable: ["high", "low"],
      reasoningDefault: "high",
      ...model,
    },
    maxIterations: 10,
    subagents: [],
    disabledTools: [],
  }
}

function makeView(
  agents: AgentView[],
  defaultAgent = "main",
  version = "v1",
): AgentsView {
  return {
    defaultAgent,
    maxDepth: 1,
    agents,
    version,
  }
}

const realFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = realFetch
  resetAgentBadgeCache()
})

describe("selectableAgents", () => {
  test("列出全部 agent —— 没有「候选名单」这回事", () => {
    const view = makeView([makeAgent("pm"), makeAgent("coder"), makeAgent("lead")], "pm")
    const out = selectableAgents(view)
    expect(out.map((a) => a.id)).toEqual(["pm", "coder", "lead"])
  })

  test("一个 agent 都没有时是空列表", () => {
    expect(selectableAgents(makeView([]))).toEqual([])
  })
})

describe("badgeFromAgents", () => {
  test("按 defaultAgent 选，不依赖数组顺序", () => {
    const badge = badgeFromAgents(
      makeView([makeAgent("worker"), makeAgent("main", { reasoning: "high" })]),
    )
    expect(badge?.id).toBe("main")
    expect(badge?.provider).toBe("main-provider")
    expect(badge?.model).toBe("main-model")
    expect(badge?.reasoning).toBe("high")
  })

  test("defaultAgent 找不到时回退第一条", () => {
    const badge = badgeFromAgents(makeView([makeAgent("worker")], "missing"))
    expect(badge?.id).toBe("worker")
  })

  test("空 reasoning 归一成 off", () => {
    const badge = badgeFromAgents(makeView([makeAgent("main", { reasoning: "" })]))
    expect(badge?.reasoning).toBe("off")
  })

  test("没有 agent 时返回 null", () => {
    expect(badgeFromAgents(makeView([]))).toBeNull()
  })
})

describe("loadAgentBadge", () => {
  test("并发调用共享一次请求，force 才重新拉取", async () => {
    let calls = 0
    globalThis.fetch = (async () => {
      calls++
      return new Response(JSON.stringify(makeView([makeAgent("main")])), {
        status: 200,
        headers: { "content-type": "application/json" },
      })
    }) as unknown as typeof fetch

    resetAgentBadgeCache()
    const [a, b] = await Promise.all([loadAgentBadge(true), loadAgentBadge(true)])
    expect(calls).toBe(1)
    expect(a?.id).toBe("main")
    expect(b).toEqual(a)

    await loadAgentBadge()
    expect(calls).toBe(1)

    await loadAgentBadge(true)
    expect(calls).toBe(2)
  })
})

describe("checkConfigChanged", () => {
  function stubFetch(responder: () => Promise<Response>): void {
    globalThis.fetch = (async () => responder()) as unknown as typeof fetch
  }

  test("本来没有缓存时，填上并报「该刷新」", async () => {
    stubFetch(async () =>
      new Response(JSON.stringify(makeView([makeAgent("main")], "main", "v1")), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    )

    expect(await checkConfigChanged()).toBe(true)
    expect((await loadAgentBadge())?.id).toBe("main")
  })

  test("指纹变了才算变了，并把缓存换成新的", async () => {
    let version = "v1"
    stubFetch(async () =>
      new Response(JSON.stringify(makeView([makeAgent("main")], "main", version)), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    )

    await loadAgentBadge(true)
    expect(await checkConfigChanged()).toBe(false)

    version = "v2"
    expect(await checkConfigChanged()).toBe(true)
  })

  test("拉取失败时安静地当没变，旧缓存留着", async () => {
    let fail = false
    stubFetch(async () => {
      if (fail) throw new Error("boom")
      return new Response(JSON.stringify(makeView([makeAgent("main")], "main", "v1")), {
        status: 200,
        headers: { "content-type": "application/json" },
      })
    })

    await loadAgentBadge(true)
    fail = true
    expect(await checkConfigChanged()).toBe(false)
    expect((await loadAgentBadge())?.id).toBe("main")
  })
})

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
      contextWindow: 0,
      ...model,
    },
    maxIterations: 10,
    subagents: [],
    tools: [],
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
  // stubFetch answers by URL: the cheap signal and the fat view are two
  // different endpoints, and the test has to be able to tell them apart.
  function stubFetch(responder: (url: string) => Promise<Response>): string[] {
    const seen: string[] = []
    globalThis.fetch = (async (input: any) => {
      const url = typeof input === "string" ? input : String(input?.url ?? "")
      seen.push(url)
      return responder(url)
    }) as unknown as typeof fetch
    return seen
  }

  function jsonRes(body: unknown): Response {
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "content-type": "application/json" },
    })
  }

  // ★ The whole point of the cheap check: it reads the fingerprint and
  //   nothing else. The agent table is fetched by the caller, and only
  //   when the fingerprint moved.
  test("只读指纹，不拉整张 agent 表", async () => {
    const seen = stubFetch(async (url) =>
      url === "/v2/health"
        ? jsonRes({ configVersion: "v1" })
        : jsonRes(makeView([makeAgent("main")], "main", "v1")),
    )

    await loadAgentBadge(true)
    seen.length = 0
    expect(await checkConfigChanged()).toBe(false)
    expect(seen).toEqual(["/v2/health"])
  })

  test("指纹没变就是没变", async () => {
    stubFetch(async (url) =>
      url === "/v2/health"
        ? jsonRes({ configVersion: "v1" })
        : jsonRes(makeView([makeAgent("main")], "main", "v1")),
    )
    await loadAgentBadge(true)
    expect(await checkConfigChanged()).toBe(false)
  })

  test("指纹变了就报变了 —— 视图由调用方重新拉", async () => {
    let version = "v1"
    stubFetch(async (url) =>
      url === "/v2/health"
        ? jsonRes({ configVersion: version })
        : jsonRes(makeView([makeAgent("main")], "main", version)),
    )
    await loadAgentBadge(true)
    expect(await checkConfigChanged()).toBe(false)

    version = "v2"
    expect(await checkConfigChanged()).toBe(true)
    // ★ The check does not refresh the view — the caller does, and only
    //   now that the fingerprint has moved.
    await loadAgentBadge(true)
    expect((await loadAgentBadge())?.id).toBe("main")
  })

  test("服务器给不出便宜信号时退回整张表", async () => {
    const seen = stubFetch(async (url) =>
      url === "/v2/health"
        ? jsonRes({ ok: true })
        : jsonRes(makeView([makeAgent("main")], "main", "v1")),
    )
    await loadAgentBadge(true)
    seen.length = 0
    expect(await checkConfigChanged()).toBe(false)
    expect(seen).toContain("/v2/agents")
  })

  test("拉取失败时安静地当没变，旧缓存留着", async () => {
    let fail = false
    stubFetch(async (url) => {
      if (fail) throw new Error("boom")
      return url === "/v2/health"
        ? jsonRes({ configVersion: "v1" })
        : jsonRes(makeView([makeAgent("main")], "main", "v1"))
    })

    await loadAgentBadge(true)
    fail = true
    expect(await checkConfigChanged()).toBe(false)
    expect((await loadAgentBadge())?.id).toBe("main")
  })
})
import { describe, expect, test } from "bun:test"

import {
  check,
  isEditable,
  suggestPath,
  validateContent,
  validatePath,
  type Issue,
  type ReasoningCatalog,
  type Section,
} from "../src/lib/settings-validate"

/**
 * 分区内的规则是模块私有函数 —— 刻意不导出。
 *
 * ★ 导出它们就等于给「绕过 check」开一扇门：调用方可以只跑一半规则，
 *   而那种用法不会有任何测试变红。测试一律走 [check] 或
 *   [validateContent] 这两个公开入口 —— 那是界面真正会调的东西。
 */
// ★ 入口是**路径**而不是分区 —— policy/ 下有两种文件形态，规则不同。
const provider = (content: string): Issue[] => validateContent("llm/x.yaml", content)
const agent = (content: string, catalog: ReasoningCatalog = {}): Issue[] =>
  validateContent("agents/x.md", content, catalog)
const policy = (content: string): Issue[] => validateContent("policy/permissions.yaml", content)
const limits = (content: string): Issue[] => validateContent("policy/limits.yaml", content)

/**
 * 配置编辑器的客户端预校验。
 *
 * ★ 这些用例的存在理由不是「覆盖率」，而是：这一层**会误报**。
 *   误报的校验会被用户关掉，而关掉之后连真正有用的那部分也没了 ——
 *   所以每条规则都要能说出它凭什么拦，以及服务端是否也拦。
 *
 * 所有断言都只钉「服务端也会拒」或「这里会误报」两件事。
 */

const lines = (issues: { line: number; message: string }[]) => issues.map((i) => i.line)

// ── 路径 ──────────────────────────────────────────────────────────────────

describe("validatePath", () => {
  test("accepts the editable sections", () => {
    for (const p of [
      "policy/permissions.yaml",
      "llm/deepseek.yaml",
      "agents/main.md",
      "tools/http.yaml",
    ]) {
      expect(validatePath(p)).toEqual([])
    }
  })

  test("rejects server/ with the reason attached", () => {
    // ★ server 不热更新，界面必须说清楚原因而不是只说「非法」。
    const issues = validatePath("server/config.yaml")
    expect(issues).toHaveLength(1)
    expect(issues[0]!.message).toContain("restart")
  })

  test("rejects escapes and absolute paths", () => {
    for (const p of ["", "  ", "/etc/passwd", "../x.yaml", "policy/../x.yaml", "a/b/c"]) {
      expect(validatePath(p).length).toBeGreaterThan(0)
    }
  })

  test("enforces the policy/ file whitelist", () => {
    // ★ policy/ 下只有 permissions.yaml 与 limits.yaml；写别的名字会让加载器
    //   **报错**，所以这一条与服务端 ValidatePath 同形。
    expect(validatePath("policy/permissions.yaml")).toHaveLength(0)
    expect(validatePath("policy/limits.yaml")).toHaveLength(0)
    expect(validatePath("policy/whatever.yaml")[0]!.message).toContain("permissions.yaml")
  })

  test("enforces the tools/ file whitelist", () => {
    // ★ tools/ 下每个文件是一种工具；未知文件名在**加载期**就是硬错误，
    //   所以界面必须提前拦（与服务端 config.validateFileName 同形）。
    for (const f of ["http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml"]) {
      expect(validatePath(`tools/${f}`)).toHaveLength(0)
    }
    expect(validatePath("tools/whatever.yaml")[0]!.message).toContain("http.yaml")
  })

  test("requires the right extension per section", () => {
    expect(validatePath("agents/main.txt")).toHaveLength(1)
    expect(validatePath("llm/deepseek.json")).toHaveLength(1)
  })

  test("isEditable matches the documented set", () => {
    expect(isEditable("policy")).toBe(true)
    expect(isEditable("agents")).toBe(true)
    expect(isEditable("llm")).toBe(true)
    // ★ tools/ 进热更新 —— 改一个工具的参数下一轮就生效，所以可编辑。
    expect(isEditable("tools")).toBe(true)
    expect(isEditable("server")).toBe(false)
    expect(isEditable("skills")).toBe(false)
  })
})

describe("suggestPath", () => {
  test("adds the section and the extension", () => {
    expect(suggestPath("deepseek", "llm")).toBe("llm/deepseek.yaml")
    expect(suggestPath("researcher.md", "agents")).toBe("agents/researcher.md")
  })

  test("leaves a correct name alone", () => {
    expect(suggestPath("permissions.yaml", "policy")).toBe("policy/permissions.yaml")
  })

  test("completes the policy file names", () => {
    // ★ policy/ 的文件名是白名单，输个裸名要被补成 .yaml 才合法。
    expect(suggestPath("limits", "policy")).toBe("policy/limits.yaml")
    expect(suggestPath("permissions", "policy")).toBe("policy/permissions.yaml")
  })

  test("returns empty for a blank name", () => {
    expect(suggestPath("   ", "llm")).toBe("")
  })
})

// ── llm/<provider>.yaml ───────────────────────────────────────────────────

describe("validateProvider", () => {
  const good = "type: openai\nbase_url: https://api.x.com\nmodels:\n  - id: fast\n"

  test("accepts a complete provider", () => {
    expect(provider(good)).toEqual([])
  })

  test("requires type, base_url and models", () => {
    // 缺 models 不是「可以不写」：缺省 = 该 provider 不可被引用，
    // 服务端会在 agent 解析引用时报错。
    const issues = provider("type: openai\nbase_url: https://x\n")
    expect(issues.map((i) => i.message)).toContain("missing models — a provider without models cannot be referenced")
  })

  test("rejects a literal api_key", () => {
    // ★ 这一条必须与服务端 config_secret_literal 一致 ——
    //   放行一个明文密钥进仓库是这一层最严重的漏判。
    const issues = provider("type: openai\nbase_url: https://x\napi_key: sk-real\nmodels: []\n")
    expect(issues.some((i) => i.message.includes("must be a reference"))).toBe(true)
  })

  test("accepts both reference forms", () => {
    // ★ `@file:` 的**带引号**写法才是可用形态：`@` 是 YAML 保留指示符，
    //   不加引号服务端解析就过不去。未加引号的那条由下一条用例覆盖。
    for (const key of ["${MY_KEY}", '"@file:/run/secrets/k"', ""]) {
      const issues = provider(`type: openai\nbase_url: https://x\napi_key: ${key}\nmodels: []\n`)
      expect(issues.filter((i) => i.message.includes("api_key"))).toEqual([])
    }
  })

  test("flags an unquoted @file value", () => {
    // `@` 是 YAML 保留指示符，不加引号服务端解析就过不去。
    // 这条是本层发现的、服务端给的报错在**另一个**位置的那类问题。
    const issues = provider("type: openai\nbase_url: https://x\napi_key: @file:/k\nmodels: []\n")
    expect(issues.some((i) => i.message.includes("must be quoted"))).toBe(true)
  })

  test("flags a base_url that needs quotes", () => {
    // ★ config-design.md §6.4 明确要求带引号，值以 : 开头会解析歧义。
    const issues = provider("type: openai\nbase_url: :8080\nmodels: []\n")
    expect(issues.some((i) => i.line === 2 && i.message.includes("quoted"))).toBe(true)
  })

  test("flags a unit-less timeout", () => {
    const issues = provider("type: openai\nbase_url: https://x\ntimeout: 300\nmodels: []\n")
    expect(issues.some((i) => i.line === 3 && i.message.includes("unit"))).toBe(true)
  })

  test("accepts a timeout with a unit", () => {
    for (const t of ["30s", "5m", "1h", "500ms"]) {
      const issues = provider(`type: openai\nbase_url: https://x\ntimeout: ${t}\nmodels: []\n`)
      expect(issues.filter((i) => i.message.includes("unit"))).toEqual([])
    }
  })

  test("ignores comments and indented keys", () => {
    // ★ 缩进的 key 是嵌套字段，不是顶层声明。误认会让 `extra:` 下面
    //   的一堆键全部报「unknown」—— 那正是「校验被关掉」的起因。
    const content = "# top comment\ntype: openai\nbase_url: https://x\nmodels:\n  - id: fast\n    extra:\n      timeout: 5m\n"
    expect(provider(content)).toEqual([])
  })
})

// ── agents/<id>.md ────────────────────────────────────────────────────────

describe("validateAgent", () => {
  test("accepts a complete agent", () => {
    const md = "---\nmodel: openai/gpt-4o\ndescription: root\n---\n\nYou are helpful.\n"
    expect(agent(md)).toEqual([])
  })

  test("requires a model and never offers inheritance", () => {
    // ★ 服务端是硬错误：不继承、不回退。子 agent 静默继承主模型是成本事故。
    const issues = agent("---\ndescription: root\n---\nbody\n")
    expect(issues.some((i) => i.message.includes("never inherited"))).toBe(true)
  })

  test("flags an empty model as well as a missing one", () => {
    // ★ 「键整个不存在」与「键存在但为空」是两种写法，同一条规则要覆盖两者 ——
    //   只在遍历里查空值的话，缺键的写法会一条都不报，而服务端会拒。
    const missing = agent("---\ndescription: root\n---\nbody\n")
    const empty = agent("---\nmodel:\ndescription: root\n---\nbody\n")
    expect(missing.filter((i) => i.message.includes("never inherited"))).toHaveLength(1)
    expect(empty.filter((i) => i.message.includes("never inherited"))).toHaveLength(1)
    expect(empty[0]!.line).toBe(2)
  })

  test("requires the provider prefix", () => {
    const issues = agent("---\nmodel: gpt-4o\n---\nbody\n")
    expect(issues.some((i) => i.line === 2 && i.message.includes("<provider>/<model>"))).toBe(true)
  })

  test("flags frontmatter that does not start on line 1", () => {
    const issues = agent("intro\n---\nmodel: openai/gpt-4o\n---\nbody\n")
    expect(issues.some((i) => i.message.includes("first line"))).toBe(true)
  })

  test("flags unterminated frontmatter", () => {
    // 分隔符没有闭合是**另一种**错，要说清楚 —— 服务端会直接报
    // 「无法判断正文从哪开始」。
    const issues = agent("---\nmodel: openai/gpt-4o\nbody with no closing ---\n")
    expect(issues.some((i) => i.message.includes("never closed"))).toBe(true)
  })

  test("flags a missing frontmatter on an otherwise valid body", () => {
    const issues = agent("just prose, no frontmatter at all\n")
    expect(issues.some((i) => i.message.includes("missing frontmatter"))).toBe(true)
  })

  test("a legal reasoning level is not an issue at all", () => {
    // ★ 这是那个 bug 的正面钉子。
    //
    //   原实现对**任何** `reasoning:` 行都推一条 issue，Issue 当时又没有
    //   严重级别，而 save() 见 issue 就 return —— 于是
    //   「agents/main.md 里写了 reasoning: high」这份**完全合法**的配置
    //   从编辑器存不进去，用户唯一的出路是删掉那行。
    //
    //   症状不是「保存失败」而是「根本存不了」，所以它躲过了只测
    //   「保存成功」的 E2E。
    const issues = agent("---\nmodel: openai/gpt-4o\nreasoning: high\n---\nbody\n")
    expect(issues).toEqual([])
  })

  test("a trailing comment does not become part of the level", () => {
    // 出厂配置就是这么写的：`reasoning: high     #← 档位名`。
    // 不剥注释的话 `high     #←…` 会被当成档位名，合法值被判成非法。
    const withCatalog = agent("---\nmodel: openai/gpt-4o\nreasoning: high  # note\n---\nbody\n", {
      "openai/gpt-4o": ["off", "low", "high"],
    })
    expect(withCatalog).toEqual([])
  })

  test("flags a level the model does not declare", () => {
    // 查得到词汇表时才判 —— 而且要说清**有哪些**可用档位。
    const issues = agent("---\nmodel: openai/gpt-4o\nreasoning: ultra\n---\nbody\n", {
      "openai/gpt-4o": ["off", "low", "high"],
    })
    expect(issues).toHaveLength(1)
    expect(issues[0]!.line).toBe(3)
    expect(issues[0]!.message).toContain('"ultra"')
    expect(issues[0]!.message).toContain("openai/gpt-4o")
    expect(issues[0]!.message).toContain("off, low, high")
  })

  test("says nothing when the vocabulary is unknown", () => {
    // ★ 查不到就**不报**。服务端本来就会把没声明的档位记进 deferred
    //   并打印（config-design.md §10），而本地猜一条「大概有问题」
    //   只是假警报 —— 假警报比没有更贵。
    const issues = agent("---\nmodel: brand-new/nope\nreasoning: ultra\n---\nbody\n")
    expect(issues).toEqual([])
  })
})

// ── policy/permissions.yaml ───────────────────────────────────────────────

describe("validatePolicy", () => {
  test("accepts the factory default", () => {
    expect(policy("allow: [ls, read_file]\nask: [write_file]\ndeny: []\n")).toEqual([])
  })

  test("rejects an unknown key", () => {
    // ★ 服务端是严格模式：拼错的键带行号报错，静默用默认值是最贵的错。
    const issues = policy("allow: [ls]\naskk: [write_file]\n")
    expect(issues).toHaveLength(1)
    expect(issues[0]!.line).toBe(2)
    expect(issues[0]!.message).toContain("unknown key")
  })

  test("flags a tool listed in two tiers", () => {
    // deny 赢，所以这么写不会改变运行时行为 —— 但它几乎总是笔误。
    const issues = policy("allow: [execute]\nask: [execute]\n")
    expect(issues).toHaveLength(1)
    expect(issues[0]!.message).toContain("already listed in allow")
  })

  test("flags a tool listed twice inside one tier", () => {
    // union() 会去重，所以这不改变结果 —— 同样是指出来更省事。
    const issues = policy("ask: [execute, execute]\n")
    expect(issues.some((i) => i.message.includes("already listed"))).toBe(true)
  })

  test("accepts an empty document", () => {
    expect(policy("")).toEqual([])
  })
})

// ── check 汇总 ────────────────────────────────────────────────────────────

describe("validateLimits", () => {
  test("accepts the two known sections with their keys", () => {
    expect(limits("agent:\n  max_depth: 2\n")).toHaveLength(0)
    expect(limits("retry:\n  max_retries: 3\n")).toHaveLength(0)
    expect(limits("agent:\n  max_depth: 2\nretry:\n  max_retries: 0\n")).toHaveLength(0)
  })

  test("treats 0 as a meaningful value, not an error", () => {
    // ★ 0 = 不设限，沿用 server/config.yaml 的全局值。它是合法配置，
    //   判成错会让「我想显式关掉这一项」写不进去。
    expect(limits("agent:\n  max_depth: 0\n")).toHaveLength(0)
  })

  test("ignores comments and blank lines", () => {
    expect(limits("# 上限\n\nagent:\n  # 根是第 0 层\n  max_depth: 2\n")).toHaveLength(0)
  })

  test("rejects an unknown section", () => {
    const issues = limits("budget:\n  max_depth: 2\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("unknown section")
  })

  test("rejects an unknown key inside a known section", () => {
    const issues = limits("agent:\n  max_tokens: 100\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("unknown key")
  })

  test("a section name used as a key says it looks like a new section", () => {
    // ★ 这是编辑器自动缩进造成的**最常见**的一种写错：
    //   「agent:」下面缩进两格后敲一个分区名，retry 就落进了 agent 里面。
    //   只说「未知键」的话，用户会以为 retry 这个分区不存在。
    const issues = limits("agent:\n  max_depth: 1\n  retry:\n  max_retries: 2\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("looks like a new section")
  })

  test("rejects a non-integer or negative value", () => {
    expect(limits("agent:\n  max_depth: abc\n").length).toBeGreaterThan(0)
    expect(limits("agent:\n  max_depth: -1\n").length).toBeGreaterThan(0)
    expect(limits("retry:\n  max_retries: 1.5\n").length).toBeGreaterThan(0)
  })

  test("rejects an indented key with no section above it", () => {
    const issues = limits("  max_depth: 2\n")
    expect(issues.length).toBeGreaterThan(0)
    expect(issues[0]!.message).toContain("without a section")
  })

  test("does not apply the permissions rules to it", () => {
    // ★ 分派必须按**文件名**：permissions 的规则会把 agent/retry 判成
    //   「未知键 allow/ask/deny」。
    expect(limits("allow: [ls]\n").length).toBeGreaterThan(0)
    expect(limits("agent:\n  max_depth: 1\n").length).toBe(0)
  })
})

describe("check", () => {
  test("a bad path short-circuits before content is examined", () => {
    const r = check("server/config.yaml", "anything at all")
    expect(r.ok).toBe(false)
    expect(r.needsServer).toBe(false)
  })

  test("a clean local pass still asks the server", () => {
    // ★ 本地只能证明「这个文件自己没问题」，证明不了「它和别的放一起没问题」。
    const r = check("policy/permissions.yaml", "allow: [ls]\n")
    expect(r.ok).toBe(true)
    expect(r.needsServer).toBe(true)
  })

  test("reports both path and content issues from one entry point", () => {
    const r = check("llm/x.yaml", "type: openai\n")
    expect(r.ok).toBe(false)
    expect(r.issues.map((i) => i.message).join(" ")).toContain("missing base_url")
  })

  test("is total over every editable section", () => {
    // 任何一个分区走到 check 里抛异常，界面就整个不能用了。
    const sections: Section[] = ["policy", "agents", "llm"]
    for (const s of sections) {
      expect(() => check(`${s}/probe`, "")).not.toThrow()
    }
  })

  test("reports an issue's line as 1-based and in source order", () => {
    const r = check("llm/x.yaml", "type: openai\napi_key: sk-leak\nbase_url: https://x\nmodels: []\n")
    expect(lines(r.issues)).toEqual([...lines(r.issues)].sort((a, b) => a - b))
    expect(r.issues.some((i) => i.line === 2)).toBe(true)
  })
})

// ── 与服务端的形状一致性 ──────────────────────────────────────────────────

describe("validateContent dispatch", () => {
  test("each section routes to its own rules", () => {
    // ★ 这里验的是**分派**，不是条数 —— 三个分区各给一段必然有问题
    //   的内容，断言它们各自报了至少一条，而 server 不参与校验。
    expect(validateContent("policy/permissions.yaml", "nope: [x]\n").length).toBeGreaterThan(0)
    expect(validateContent("agents/x.md", "no frontmatter here").length).toBeGreaterThan(0)
    expect(validateContent("llm/x.yaml", "api_key: sk-x\n").length).toBeGreaterThan(0)
    expect(validateContent("server/config.yaml", "whatever")).toEqual([])
  })

  test("the llm section is the only one guarding secrets", () => {
    // 密钥规则只属于 llm/ —— 别处出现 api_key 字样不受影响，
    // 与服务端 checkSecret 同一条纪律。
    //
    // ★ 断言要挑**规则**而不是只匹配 "api_key" 这个词：policy 里的
    //   `api_key: sk-x` 会命中「未知键」这条规则，消息里也带这个��，
    //   那是完全正确的另一件事。
    const isSecretRule = (i: Issue) => i.message.includes("must be a reference")
    expect(validateContent("llm/x.yaml", "type: o\nbase_url: https://x\napi_key: sk-x\nmodels: []\n")
      .some(isSecretRule)).toBe(true)
    expect(validateContent("policy/permissions.yaml", "api_key: sk-x\n").some(isSecretRule)).toBe(false)
    expect(validateContent("policy/limits.yaml", "api_key: sk-x\n").some(isSecretRule)).toBe(false)
    expect(validateContent("agents/x.md", "---\nmodel: a/b\napi_key: sk-x\n---\n").some(isSecretRule)).toBe(false)
  })
})

// ── tools/ ────────────────────────────────────────────────────────────────

describe("validateContent for tools/", () => {
  test("flags a timeout without a unit", () => {
    // ★ 服务端是硬报错（yaml.v3：duration must be a string with a unit），
    //   裸数字会被当成纳秒。
    const issues = validateContent("tools/http.yaml", "methods: [get]\ntimeout: 30\n")
    expect(issues.some((i) => i.message.includes("unit"))).toBe(true)
  })

  test("accepts a timeout with a unit", () => {
    expect(validateContent("tools/http.yaml", "methods: [get]\ntimeout: 30s\n")).toEqual([])
    expect(validateContent("tools/web_search.yaml", "timeout: 60s\n")).toEqual([])
  })

  test("rejects an absolute corpus path", () => {
    // ★ 服务端拒：索引与语料必须同生共死（换目录就得重建索引）。
    const issues = validateContent("tools/kb.yaml", "corpus: /usr/share/doc\n")
    expect(issues.some((i) => i.message.includes("relative"))).toBe(true)
  })

  test("requires a model when the corpus is set", () => {
    // ★ 服务端硬报错：kb.model is required when kb.corpus is set。
    const issues = validateContent("tools/kb.yaml", "corpus: docs\n")
    expect(issues.some((i) => i.message.includes("model is required"))).toBe(true)
    expect(validateContent("tools/kb.yaml", "corpus: docs\nmodel: local/embed\n")).toEqual([])
  })

  test("an empty corpus is off, not an error", () => {
    // 没配语料 = 知识库关闭 —— 那不是错误。
    expect(validateContent("tools/kb.yaml", "corpus: \"\"\n")).toEqual([])
  })
})
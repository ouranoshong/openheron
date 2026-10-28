/**
 * 配置编辑器的**客户端预校验**。
 *
 * ★ 为什么要有一层客户端校验，而服务端已经有 `POST /v2/settings/validate`：
 *
 *   这层只为**瞬时反馈**，不承担权威。权威永远是服务端那次试合并 ——
 *   它看得见跨文件的东西（agent 引用的模型没声明、子 agent 图成环），
 *   客户端看不见。
 *
 * ★ 所以这里**只查单文件内可判定的规则**，而且刻意窄：
 *
 *   禁得太宽会误伤正当内容，而误报会让人把整个校验关掉 —— 那比没有校验
 *   更糟。与 agent 侧的 checkSecret 同一条纪律（见 config/write.go）。
 *
 * 任何「这个值在别处能不能解析」的问题都不要在这里答 —— 答错了界面就会
 * 说谎，而说谎的界面比一个缺功能的界面糟糕得多。
 */

/**
 * 一条校验结果。`line` 从 1 开始，与编辑器显示的行号一致。
 *
 * ★ `severity` 决定**能不能存**，不只是显示上的差别 ——
 *   `settingsStore.save` 见 error 就直接 return，不发请求。
 *   分不清这两级的话，一条提示就会变成「合法配置存不进去」。
 */
export interface Issue {
  line: number
  message: string
  /**
   * `error` 阻断保存；`hint` 只显示。
   *
   * ★ 缺省按 **error** 处理：漏写严重级别时宁可吵一点，也不要悄悄放过
   *   一个真错误。反过来（默认 hint）会让未来的新检查静默失效。
   */
  severity?: Severity
}

export type Severity = "error" | "hint"

/** 只有 error 阻断保存。 */
export function blocking(issues: readonly Issue[]): Issue[] {
  return issues.filter((i) => (i.severity ?? "error") === "error")
}

/** 与后端 config.Category 对齐。 */
export type Section = "policy" | "agents" | "llm" | "server" | "tools"

/** 每个分区允许的文件名；policy/ 与 tools/ 有白名单（与 config-design.md §6.3 同）。 */
const ALLOWED_FILES: Record<Section, readonly string[]> = {
  // ★ policy/ 下两种文件形态，规则不同 —— 校验必须按**文件名**分派，
  //   不能只看分区。see validateContent。
  policy: ["permissions.yaml", "limits.yaml"],
  agents: [], // 每个 agent 一个 .md，文件名即 id
  llm: [], // 每个 provider 一个 .yaml
  server: ["config.yaml"],
  // ★ tools/ 下四种文件形态，每个文件是一种工具 —— 白名单与服务端
  //   config.validateFileName 同源。
  tools: ["http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml"],
}

/**
 * 可编辑的分区。
 *
 * ★ server 不在其中：它不进热更新（config/reload.go 的 HotDirs），
 *   做成「保存成功但不生效」比拒绝更糟。
 *
 * ★ tools/ 在其中：它**进**热更新 —— 改一个工具的超时，下一轮就生效。
 */
export const EDITABLE_SECTIONS: readonly Section[] = ["policy", "agents", "llm", "tools"]

/** isEditable 报告某分区能否写入。 */
export function isEditable(section: string): boolean {
  return (EDITABLE_SECTIONS as readonly string[]).includes(section)
}

/**
 * validatePath 校验一个「分区/文件名」形态的路径。
 *
 * 刻意与服务端 ValidatePath **同形**：界面提前拦下服务端会 400 的东西，
 * 省掉一次往返。它不是权威 —— 服务端仍会再判一次。
 */
export function validatePath(path: string): Issue[] {
  const issues: Issue[] = []
  const at = (line: number, message: string) => issues.push({ line, message })

  const trimmed = path.trim()
  if (!trimmed) {
    at(1, "path is empty")
    return issues
  }
  if (trimmed.startsWith("/")) {
    at(1, "path must be relative to the config directory")
    return issues
  }
  const segments = trimmed.split("/")
  if (segments.some((s) => s === ".." || s === ".")) {
    at(1, 'path must not contain "." or ".."')
    return issues
  }
  if (segments.length !== 2) {
    at(1, "path must be <section>/<file>")
    return issues
  }

  const [section, file] = segments as [string, string]
  if (!isEditable(section)) {
    at(1, `${section}/ is not editable${section === "server" ? " — changes need a restart" : ""}`)
    return issues
  }
  if (file.startsWith(".")) {
    at(1, "file name must not start with a dot")
    return issues
  }

  const allowed = ALLOWED_FILES[section as Section] ?? []
  if (allowed.length > 0 && !allowed.includes(file)) {
    at(1, `${section}/ only accepts ${allowed.join(", ")}`)
  }
  if (section === "agents" && !file.endsWith(".md")) {
    at(1, "agents/ files must end with .md")
  }
  if (section === "llm" && !file.endsWith(".yaml")) {
    at(1, "llm/ files must end with .yaml")
  }
  return issues
}

/** suggestPath 从裸文件名推出一个合法的相对路径。 */
export function suggestPath(fileName: string, section: Section): string {
  let name = fileName.trim()
  if (!name) return ""
  if (section === "agents" && !name.endsWith(".md")) name = `${name}.md`
  if (section === "llm" && !name.endsWith(".yaml")) name = `${name}.yaml`
  // ★ policy/ 的文件名是**白名单**（permissions.yaml / limits.yaml），
  //   补上 .yaml 才能让「输个 limits」变成合法的 policy/limits.yaml。
  if (section === "policy" && !name.endsWith(".yaml")) name = `${name}.yaml`
  // ★ tools/ 的文件名是**白名单**（http.yaml / web_search.yaml /
  //   wikipedia.yaml / kb.yaml），补上 .yaml 才能把「输个 http」变成
  //   合法的 tools/http.yaml。
  if (section === "tools" && !name.endsWith(".yaml")) name = `${name}.yaml`
  return `${section}/${name}`
}

/**
 * validateContent 是对单文件内容的预校验。
 *
 * ★ 每一类都注明了「服务端是否也会拒」，免得后来人以为这里才是权威。
 */
export function validateContent(
  path: string,
  content: string,
  catalog: ReasoningCatalog = {},
): Issue[] {
  const section = path.split("/")[0] as Section
  switch (section) {
    case "llm":
      return validateProvider(content)
    case "agents":
      return validateAgent(content, catalog)
    case "policy":
      // ★ policy/ 下有两种文件形态，规则不同 —— 必须按**文件名**分派。
      //   只按分区分派的话，limits.yaml 的键会被 permissions 的规则判成
      //   「未知键 allow/ask/deny」。
      return path.endsWith("limits.yaml") ? validateLimits(content) : validatePolicy(content)
    case "tools":
      return validateTool(path, content)
    default:
      return []
  }
}

// ── tools/<tool>.yaml ─────────────────────────────────────────────────────

/**
 * validateTool 校验一个工具配置文件。
 *
 * ★ 只查服务端**会拒**的两种 —— 界面的职责是「别让用户白跑一趟」，
 *   不是替服务端做完整校验。
 *
 *   1. timeout 必须带单位（服务端是硬报错：裸数字会被当成纳秒）。
 *   2. kb.yaml 的 corpus 不能是绝对路径（服务端拒：索引与语料必须同生共死）。
 */
function validateTool(path: string, content: string): Issue[] {
  const issues: Issue[] = []
  const lines = content.split("\n")

  lines.forEach((line, i) => {
    const n = i + 1
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const m = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
    if (!m) return
    const [, key, rawValue] = m as unknown as [string, string, string]
    const value = rawValue.trim()

    if (key === "timeout" && value && !/^\d+(\.\d+)?(ns|us|ms|s|m|h)/.test(value) && !/^["']/.test(value)) {
      // ★ 服务端 yaml.v3 对裸数字会报 "duration must be a string with a unit"。
      issues.push({ line: n, message: "timeout needs a unit, e.g. 30s or 5m" })
    }

    if (key === "corpus" && value && value.startsWith("/")) {
      issues.push({
        line: n,
        message: "corpus must be relative to the project root — the index and its corpus share one lifetime",
      })
    }
  })

  // ★ kb.yaml 还要求：corpus 非空时 model 必填（服务端硬报错）。
  //
  //   「非空」要**排除引号里的空串** —— `corpus: ""` 是「知识库关闭」，
  //   与服务端 `if c.KB.Corpus == "" { return nil }` 同义。
  if (path.endsWith("kb.yaml")) {
    const value = (key: string) => {
      const raw = content.match(new RegExp(`^\\s*${key}:\\s*(.*)$`, "m"))?.[1]?.trim() ?? ""
      return raw === '""' || raw === "''" ? "" : raw
    }
    if (value("corpus") !== "" && value("model") === "") {
      issues.push({
        line: 1,
        message: "model is required when corpus is set — it must reference an embedding model under llm/",
      })
    }
  }

  return issues
}

// ── llm/<provider>.yaml ───────────────────────────────────────────────────

function validateProvider(content: string): Issue[] {
  const issues: Issue[] = []
  const lines = content.split("\n")

  let hasBaseURL = false
  let hasType = false
  let modelsDeclared = false

  lines.forEach((line, i) => {
    const n = i + 1
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const m = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
    if (!m) return
    const [, key, rawValue] = m as unknown as [string, string, string]
    const value = rawValue.trim()

    if (key === "base_url") {
      hasBaseURL = true
      // ★ config-design.md §6.4：值以 : 开头不加引号会解析歧义。
      //   服务端 yaml.v3 会报错，这里提前拦。
      if (value.startsWith(":") && !/^["']/.test(value)) {
        issues.push({ line: n, message: 'base_url starting with ":" must be quoted' })
      }
    }
    if (key === "type") hasType = true
    if (key === "models") modelsDeclared = true

    if (key === "timeout" && value && !/^\d+(\.\d+)?(ns|us|ms|s|m|h)/.test(value) && !/^["']/.test(value)) {
      // Duration 写错会让超时行为不可预测，服务端是**硬报错**。
      issues.push({ line: n, message: "timeout needs a unit, e.g. 30s or 5m" })
    }

    if (key === "api_key" && value) {
      // ★ 明文密钥服务端一律拒（config_secret_literal）。
      const unquoted = value.replace(/^["']|["']$/g, "")
      const isRef = /^\$\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(unquoted)
      const isFile = unquoted.startsWith("@file:")
      if (!isRef && !isFile) {
        issues.push({
          line: n,
          message: "api_key must be a reference: ${ENV_VAR} or @file:/path — a literal would be committed",
        })
      }
      // `@` 是 YAML 保留指示符，不加引号服务端解析就过不去。
      if (isFile && unquoted.startsWith("@file:") && !/^["']/.test(value)) {
        issues.push({ line: n, message: 'an "@file:" value must be quoted: api_key: "@file:/path"' })
      }
    }
  })

  if (!hasType) issues.push({ line: 1, message: "missing type" })
  if (!hasBaseURL) issues.push({ line: 1, message: "missing base_url" })
  // 缺省 = 该 provider 不可用（引用会解析失败），服务端会拒。
  if (!modelsDeclared) issues.push({ line: 1, message: "missing models — a provider without models cannot be referenced" })
  return issues
}

// ── agents/<id>.md ────────────────────────────────────────────────────────

/**
 * `reasoning` 档位在**这个模型**上可用哪些档。
 *
 * ★ key 是 `<provider>/<model>`，value 是该模型 `models[].reasoning` 的键。
 *
 *   取不到时（模型不存在、离线、或清单还没加载）整个表可以是空的 ——
 *   见 [validateAgent] 里「查不到就不报」那条。
 */
export type ReasoningCatalog = Readonly<Record<string, readonly string[]>>

/**
 * 去掉 YAML 行尾注释。
 *
 * ★ `#` 只有在**行首或空白之后**才算注释起点 —— 这条规则要照做，
 *
 *   因为 `api_key: "sk-abc#def"` 里中间的 `#` 是值的一部分。
 *   无脑 `split("#")[0]` 会把它截成 `sk-abc`，而那正是密钥。
 */
function stripComment(value: string): string {
  for (let i = 0; i < value.length; i++) {
    if (value[i] !== "#") continue
    if (i === 0 || /\s/.test(value[i - 1]!)) return value.slice(0, i)
  }
  return value
}

function validateAgent(content: string, catalog: ReasoningCatalog = {}): Issue[] {
  const issues: Issue[] = []
  const lines = content.split("\n")

  const open = lines.findIndex((l) => l.trim() === "---")
  if (open < 0) {
    // 服务端视「无 frontmatter」为合法（纯提示词草稿），随后因缺 model 报错。
    issues.push({ line: 1, message: "missing frontmatter; an agent file needs --- … --- with a model" })
    return issues
  }
  // ★ 分隔符不在第一行是**另一种错**，值得单独说：
  //   服务端 splitFrontmatter 只认开头的 ---，于是前面那几行会被当成
  //   正文（= 系统提示词的一部分）吞掉，而用户以为自己写了前言。
  if (open !== 0) {
    issues.push({ line: 1, message: "the frontmatter must start on the first line" })
  }

  const end = lines.findIndex((l, i) => i > open && l.trim() === "---")
  if (end < 0) {
    issues.push({ line: open + 1, message: "frontmatter is never closed — add a closing ---" })
    return issues
  }

  let sawModel = false
  let modelRef = ""
  for (let i = open + 1; i < end; i++) {
    const raw = lines[i]!
    const trimmed = raw.trim()
    if (!trimmed || trimmed.startsWith("#")) continue
    const idx = raw.indexOf(":")
    if (idx <= 0) continue
    const key = raw.slice(0, idx).trim()
    const value = raw.slice(idx + 1)
    const n = i + 1

    if (key === "model") {
      sawModel = true
      const v = value.trim()
      modelRef = v
      if (!v) {
        // ★ 服务端硬错误：不继承、不回退。子 agent 静默继承主模型是成本事故。
        issues.push({ line: n, message: "model is required — pick one from the available list; it is never inherited" })
      } else if (!v.includes("/")) {
        issues.push({ line: n, message: 'model must be "<provider>/<model>"' })
      }
    }
    if (key === "reasoning" && value.trim()) {
      // ★ 这里必须**用真实词汇表判**，不能无条件提示一句「服务端会校验」。
      //
      //   原实现对任何 `reasoning:` 行都报一条 issue，而 Issue 当时
      //   没有严重级别、save() 见 issue 就 return —— 于是**合法的
      //   reasoning 档位也让文件存不进去**，唯一的「修法」是删掉
      //   一行正确配置。而那不是提示，是阻断。
      //
      //   查不到词汇表就**什么都不说**：服务端本来就会把没声明的档位
      //   记进 deferred 并打印（config-design.md §10）。本地猜一条
      //   「大概有问题」只会是假警报，而假警报比没有更贵。
      const level = stripComment(value).trim()
      const known = catalog[modelRef]
      if (known && !known.includes(level)) {
        issues.push({
          line: n,
          message:
            `reasoning level "${level}" is not declared by ${modelRef}; ` +
            `it supports: ${known.join(", ")}`,
        })
      }
    }
  }

  // ★ 键**整个不存在**与「键存在但为空」是两回事，两条都要说 ——
  //   只在遍历里查空值的话，`---\ndescription: x\n---` 会一条都不报，
  //   而服务端会拒。
  if (!sawModel) {
    issues.push({ line: 1, message: "model is required — pick one from the available list; it is never inherited" })
  }
  return issues
}

// ── policy/permissions.yaml ───────────────────────────────────────────────

const TIERS = new Set(["allow", "ask", "deny"])

function validatePolicy(content: string): Issue[] {
  const issues: Issue[] = []
  // 值是**先前所在的档位名**，报错时要指名是哪一档 —— 只说「重复了」
  // 不足以让人判断自己是不是想让它同时属于两档。
  const seen = new Map<string, string>()

  content.split("\n").forEach((raw, i) => {
    const trimmed = raw.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const m = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
    if (!m) return
    const key = m[1]!
    if (!TIERS.has(key)) {
      issues.push({ line: i + 1, message: `unknown key "${key}" — expected allow, ask or deny` })
      return
    }

    for (const name of listItems(m[2] ?? "")) {
      const prev = seen.get(name)
      if (prev !== undefined) {
        // 同名出现在两档里：匹配顺序是 deny → ask → allow，deny 赢。
        // 但这么写几乎总是笔误，说出来比默默生效有用。
        issues.push({
          line: i + 1,
          message: `"${name}" is already listed in ${prev} — deny wins, but this is probably a typo`,
        })
      } else {
        seen.set(name, key)
      }
    }
  })

  return issues
}

/**
 * listItems 拆出 `[a, b]` 或 `[]` 形式列表里的每一项。
 *
 * ★ 只认行内列表：openheron 的 policy 文件就是这一种形态。跨行的块序列
 *   交给服务端的严格解析去报 —— 在这里猜反而可能猜错。
 */
function listItems(raw: string): string[] {
  const t = raw.trim()
  if (!t.startsWith("[")) return []
  const inner = t.replace(/^\[/, "").replace(/\]$/, "")
  return inner
    .split(",")
    .map((s) => s.trim().replace(/^["']|["']$/g, ""))
    .filter(Boolean)
}

// ── policy/limits.yaml ────────────────────────────────────────────────────

/** 限制规则里允许的顶层分区。 */
const LIMIT_SECTIONS = new Set(["agent", "retry"])

/** 每个分区下允许的键。 */
const LIMIT_KEYS: Record<string, ReadonlySet<string>> = {
  agent: new Set(["max_depth"]),
  retry: new Set(["max_retries"]),
}

/**
 * validateLimits 校验 policy/limits.yaml。
 *
 * ★ 限制规则是**数量上的上限**，与 permissions.yaml 的「要不要审批」正交。
 *
 *   agent.max_depth    —— 嵌套深度上限（根 agent 是第 0 层）
 *   retry.max_retries  —— 模型调用重试次数上限
 *
 * ★ 0 是合法值，含义是「不设限，沿用 server/config.yaml 的全局值」。
 *   所以不能把 0 判成错 —— 它是一个**有意义的**配置。
 */
function validateLimits(content: string): Issue[] {
  const issues: Issue[] = []
  let current: string | null = null

  content.split("\n").forEach((raw, i) => {
    const trimmed = raw.trim()
    if (!trimmed || trimmed.startsWith("#")) return

    const line = i + 1
    // ★ 缩进看**原始行** —— trimmed 已经去掉了空格，拿它判断缩进
    //   会把每个缩进键都当成顶层分区。
    const indented = /^\s/.test(raw)

    if (!indented) {
      // 顶层分区：`agent:` / `retry:`
      const sec = /^([a-z_]+):\s*(.*)$/.exec(trimmed)
      if (!sec) {
        issues.push({ line, message: "cannot parse this line" })
        return
      }
      const key = sec[1]!
      if (!LIMIT_SECTIONS.has(key)) {
        issues.push({
          line,
          message: `unknown section "${key}" — expected agent or retry`,
        })
        return
      }
      current = key
      // `agent: 2` 是不合法的 —— 那是嵌套键，该缩进。
      if ((sec[2] ?? "").trim() !== "") {
        issues.push({
          line,
          message: `"${key}" is a section, not a value — put its keys on the next lines`,
        })
      }
      return
    }

    // 缩进过的键：`  max_depth: 2`
    const kv = /^\s+([a-z_]+):\s*(.*)$/.exec(raw)
    if (!kv) {
      issues.push({ line, message: "cannot parse this line" })
      return
    }
    if (current === null) {
      issues.push({ line, message: "indented key without a section — add `agent:` or `retry:` first" })
      return
    }
    const key = kv[1]!
    const allowed = LIMIT_KEYS[current]!
    if (!allowed.has(key)) {
      // ★ 「分区名被当成了键」是**最常见的一种写错** —— 而且成因具体：
      //
      //	编辑器的 YAML 自动缩进会让「agent:」下面那行缩进两格，
      //	用户接着敲一个分区名（retry:）就落在了 agent 里面。
      //	只说「未知键」的话，用户会以为 retry 这个分区不存在。
      if (LIMIT_SECTIONS.has(key)) {
        issues.push({
          line,
          message: `"${key}" looks like a new section — it must start at the left margin (no indentation)`,
        })
        return
      }
      issues.push({
        line,
        message: `unknown key "${key}" in ${current} — expected ${[...allowed].join(" or ")}`,
      })
      return
    }
    const rawValue = (kv[2] ?? "").trim()
    if (!/^\d+$/.test(rawValue)) {
      issues.push({
        line,
        message: `"${key}" must be a non-negative integer (0 = no limit), got "${rawValue}"`,
      })
    }
  })

  return issues
}

// ── 汇总 ──────────────────────────────────────────────────────────────────

export interface ValidationResult {
  /** 没有 **error** 级问题时为 true —— hint 不算。 */
  ok: boolean
  issues: Issue[]
  /** needsServer 是需要问服务端的那些（跨文件），本地判不了。 */
  needsServer: boolean
}

/**
 * check 跑一遍完整的本地预校验。
 *
 * ★ 返回 `needsServer` 而不是因为本地过了就放行：本地只能证明
 *   「这个文件自己没问题」，证明不了「它和别的文件放在一起没问题」。
 *
 * ★ `ok` 只看 **error**：hint 的存在不该让一份合法文件存不进去。
 *   这里曾经把 hint 当 error，结果是**任何设了 `reasoning:` 的
 *   agents/*.md 都存不了** —— 而那行配置完全合法。
 */
export function check(
  path: string,
  content: string,
  catalog: ReasoningCatalog = {},
): ValidationResult {
  const pathIssues = validatePath(path)
  if (pathIssues.length > 0) {
    return { ok: false, issues: pathIssues, needsServer: false }
  }
  const issues = validateContent(path, content, catalog)
  return { ok: blocking(issues).length === 0, issues, needsServer: true }
}
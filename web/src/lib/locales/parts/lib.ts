// ★ The copy that the **pure functions** in `lib/` produce: the tool titles,
//   the three authorization modes, the thinking label, the skill modes.
//
// ★ These live in `lib/` because they are pure — and a pure function cannot
//   use a hook. ⇒ They take the translator as an argument (see `Translate`
//   in `lib/i18n.ts`), and the caller passes the one it has.
//
// ★ The English here is the text the interface shows **today**, byte for
//   byte: the E2E suite asserts those sentences, and a wording that drifts
//   is a wording the tests would not catch.
export const lib = {
  // the tool titles (lib/tool-titles.ts)
  "tool.title.ls": { en: "List directory", zh: "列出目录" },
  "tool.title.glob": { en: "Find files", zh: "查找文件" },
  "tool.title.grep": { en: "Search content", zh: "搜索内容" },
  "tool.title.readFile": { en: "Read file", zh: "读取文件" },
  "tool.title.writeFile": { en: "Write file", zh: "写入文件" },
  "tool.title.editFile": { en: "Edit file", zh: "编辑文件" },
  "tool.title.execute": { en: "Run command", zh: "运行命令" },
  "tool.title.task": { en: "Run a sub-agent", zh: "运行子专家" },
  "tool.title.writeTodos": { en: "Update todos", zh: "更新待办" },
  "tool.title.skill": { en: "Load a skill", zh: "加载技能" },
  "tool.title.configGet": { en: "Read configuration", zh: "读取配置" },
  "tool.title.configValidate": { en: "Validate configuration", zh: "校验配置" },
  "tool.title.configTemplate": { en: "Show a template", zh: "显示模板" },
  "tool.title.configNotices": { en: "Show notices", zh: "显示通知" },
  "tool.title.configTier": { en: "Explain approval tiers", zh: "解释审批级别" },
  "tool.title.configWrite": { en: "Write configuration", zh: "写入配置" },
  "tool.title.requestGet": { en: "Send a GET request", zh: "发送 GET 请求" },
  "tool.title.requestPost": { en: "Send a POST request", zh: "发送 POST 请求" },
  "tool.title.requestPut": { en: "Send a PUT request", zh: "发送 PUT 请求" },
  "tool.title.requestDelete": { en: "Send a DELETE request", zh: "发送 DELETE 请求" },
  "tool.title.duckduckgoSearch": { en: "Search the web", zh: "搜索网页" },
  "tool.title.wikipediaSearch": { en: "Search Wikipedia", zh: "搜索维基百科" },
  "tool.title.kbSearch": { en: "Search the knowledge base", zh: "搜索知识库" },
  "tool.title.kbIndex": { en: "Index the knowledge base", zh: "索引知识库" },
  "tool.title.fetch": { en: "Fetch URL", zh: "抓取 URL" },

  // the three authorization modes (lib/authorization.ts)
  "authorization.label.default": { en: "Default approval", zh: "默认审批" },
  "authorization.title.default": {
    en: "Tools ask before they run, as the configuration says",
    zh: "工具按配置要求在运行前请求审批",
  },
  "authorization.label.trusted": { en: "Trusted paths", zh: "可信路径" },
  "authorization.title.trusted": {
    en: "Writes inside the project, the session workspace and the configuration do not ask",
    zh: "在项目、会话工作区和配置内的写入无需请求审批",
  },
  "authorization.label.full": { en: "Full authorization", zh: "完全授权" },
  "authorization.title.full": { en: "Every tool runs without asking", zh: "所有工具都无需请求审批即可运行" },

  // the thinking label (lib/thinking.ts)
  "thinking.running": { en: "Thinking…", zh: "思考中…" },
  "thinking.interrupted": { en: "Interrupted", zh: "已中断" },
  "thinking.interruptedFor": { en: "Interrupted after {duration}", zh: "在 {duration} 后中断" },
  "thinking.done": { en: "Thought", zh: "思考完成" },
  "thinking.doneFor": { en: "Thought for {duration}", zh: "思考了 {duration}" },

  // the skill modes (lib/skill-info.ts)
  "skill.mode.fork": { en: "Run in a separate agent", zh: "在独立专家中运行" },
  "skill.mode.forkWithContext": { en: "Run in a separate agent (with context)", zh: "在独立专家中运行（带上下文）" },
  "skill.mode.inline": { en: "Run in the current agent", zh: "在当前专家中运行" },

  // the approval card's own words (lib/approval.ts)
  "approval.title.one": { en: "Approval required", zh: "需要审批" },
  "approval.title.many": { en: "{count} tool calls need your approval", zh: "{count} 次工具调用需要你审批" },
  "approval.toolCall": { en: "Tool call", zh: "工具调用" },

  // a call's state (lib/tool-groups.ts)
  "tool.phase.readingArguments": { en: "reading arguments", zh: "读取参数" },
  "tool.phase.running": { en: "running", zh: "运行中" },
  "tool.phase.done": { en: "done", zh: "已完成" },
  "tool.phase.failed": { en: "failed", zh: "失败" },

  // the name of a file's kind (lib/file-kind.ts)
  "file.kind.image": { en: "Image", zh: "图片" },
  "file.kind.pdf": { en: "PDF", zh: "PDF" },
  "file.kind.markdown": { en: "Markdown", zh: "Markdown" },
  "file.kind.code": { en: "Code", zh: "代码" },
  "file.kind.table": { en: "Table", zh: "表格" },
  "file.kind.text": { en: "Text", zh: "文本" },
  "file.kind.archive": { en: "Archive", zh: "归档" },
  "file.kind.file": { en: "File", zh: "文件" },

  // the secrets panel's own sentences (lib/secrets.ts)
  // ★ The four **scope** words are keys of their own: the sentence needs the
  //   singular and the plural, and a language that has no plural still has
  //   to be able to say which namespace is being emptied.
  // ★ The **group names**: the server sends its own English labels, and the
  //   interface says the ones the reader is reading. The server's label is
  //   the English fallback, not the Chinese one.
  "secrets.group.llm": { en: "Providers", zh: "服务商" },
  "secrets.group.mcp": { en: "MCP servers", zh: "连接器" },

  // ★ The **error sentences**: the server's `code` is stable and machine
  //   readable, so the interface can say the failure in the reader's words.
  //   A code that is not mapped here shows the server's English sentence as
  //   it is — untranslated, unprefixed, unswallowed.
  "secrets.error.secretUnavailable": {
    en: "The keyring is not available on this machine.",
    zh: "本机密钥环不可用。",
  },
  "secrets.error.invalidBody": { en: "The request was not valid.", zh: "请求不合法。" },
  "secrets.error.unknown": { en: "Something went wrong.", zh: "出了点问题。" },
  "secrets.error.network": { en: "The server could not be reached.", zh: "无法连接到服务端。" },

  "secrets.scope.providerOne": { en: "provider secret", zh: "服务商密钥" },
  "secrets.scope.providerMany": { en: "provider secrets", zh: "服务商密钥" },
  "secrets.scope.mcpOne": { en: "MCP server secret", zh: "连接器密钥" },
  "secrets.scope.mcpMany": { en: "MCP server secrets", zh: "连接器密钥" },
  "secrets.remove.message": {
    en: "Remove {count} {scope}? Only the {group} group is affected; {other} are untouched.",
    zh: "移除 {count} 个 {scope}？仅 {group} 分组受影响，{other} 保持不变。",
  },
  "secrets.source.none": { en: "no source — no key is in effect", zh: "无来源 —— 没有生效的密钥" },
  "secrets.source.env": { en: "the environment variable {name}", zh: "环境变量 {name}" },
  "secrets.source.keyring": { en: "the keyring entry {name}", zh: "密钥环条目 {name}" },
  "secrets.source.file": { en: "the file {name}", zh: "文件 {name}" },
  "secrets.status.stored": { en: "stored", zh: "已存储" },
  "secrets.status.notSet": { en: "not set", zh: "未设置" },
  "secrets.status.shadowed": {
    en: "The environment variable {envVar} wins — the stored key is not in effect.",
    zh: "环境变量 {envVar} 优先 —— 已存储的密钥未生效。",
  },
  "secrets.env.set": { en: "set", zh: "已设置" },
  "secrets.env.notSet": { en: "not set", zh: "未设置" },
} as const

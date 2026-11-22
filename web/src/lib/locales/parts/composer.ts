// ★ The input area's copy: the composer, the line of facts under it, the
//   pickers and the project dialog.
//
// ★ The English here is the text the interface shows **today**, byte for
//   byte: the E2E suite asserts these sentences, and a wording that drifts
//   is a wording the tests would not catch.
//
// ★ A label that ends in a space ("Agent: ") keeps it: the space belongs to
//   the label, not to the value that follows it.
export const composer = {
  // the composer itself (components/composer.tsx)
  "composer.input.label": { en: "Message input", zh: "消息输入框" },
  "composer.input.placeholder": { en: "Send a message...", zh: "发送消息…" },
  "composer.input.placeholderBlocked": {
    en: "Answer the approval above to continue",
    zh: "请先处理上方的审批以继续",
  },
  "composer.input.send": { en: "Send message", zh: "发送消息" },
  "composer.input.attach": { en: "Add attachment", zh: "添加附件" },
  "composer.input.attachFiles": { en: "Attach files", zh: "附加文件" },
  "composer.files.loadFailed": { en: "Failed to load files", zh: "加载文件失败" },

  // the completion popup (components/composer-popup.tsx)
  "composer.popup.ariaLabel": { en: "Completions", zh: "补全建议" },
  "composer.popup.searching": { en: "Searching…", zh: "搜索中…" },
  "composer.popup.noFiles": { en: "No matching files", zh: "没有匹配的文件" },
  "composer.popup.noCommands": { en: "No matching commands", zh: "没有匹配的命令" },
  "composer.popup.noCommandsAvailable": { en: "No commands available", zh: "没有可用命令" },

  // the line of facts under the input box (components/composer-meta.tsx)
  "composer.meta.busyElsewhere": {
    en: "This session is running in another window — stop it to take over",
    zh: "该会话正在另一个窗口中运行 —— 请停止后接管",
  },
  "composer.meta.agentUnavailable": { en: "Agent info unavailable", zh: "无法获取专家信息" },
  "composer.meta.agentLabel": { en: "Agent: ", zh: "专家: " },
  "composer.meta.switchAgent": { en: "Switch agent", zh: "切换专家" },
  "composer.meta.ariaLabel": { en: "Current agent configuration", zh: "当前专家配置" },
  "composer.meta.reasoningAvailable": { en: "Available: {available}", zh: "可用：{available}" },
  "composer.meta.context.label": {
    en: "Context: ≈{used} / {window} ({percent})",
    zh: "上下文：≈{used} / {window} ({percent})",
  },
  "composer.meta.context.tokens": {
    en: "Tokens in this agent's context — the most recent request, or the summary after a compaction.",
    zh: "当前专家上下文中的 token —— 最近一次请求，或压缩后的摘要。",
  },
  "composer.meta.context.window": { en: "Window: {tokens} tokens", zh: "窗口：{tokens} token" },
  "composer.meta.context.noWindow": {
    en: "The model declares no context window.",
    zh: "该模型未声明上下文窗口。",
  },
  "composer.meta.context.compactedAt": {
    en: "Compacted at: {tokens} tokens",
    zh: "压缩于：{tokens} token",
  },
  "composer.meta.context.compactedTimes": {
    en: "Compacted {count} time(s)",
    zh: "已压缩 {count} 次",
  },

  // the reasoning level, in the two places that name it
  "composer.reasoning": { en: "Reasoning: {reasoning}", zh: "推理：{reasoning}" },

  // the agent picker (components/agent-picker.tsx)
  "composer.agentPicker.title": { en: "Switch agent", zh: "切换专家" },
  "composer.agentPicker.busy": {
    en: "A run is in progress — finish or stop it before switching.",
    zh: "正在运行 —— 请先完成或停止后再切换。",
  },
  "composer.agentPicker.description": {
    en: "The selected agent runs the next turn as the root.",
    zh: "选中的专家将作为根专家运行下一轮。",
  },
  "composer.agentPicker.filter": { en: "Filter agents", zh: "筛选专家" },
  "composer.agentPicker.current": { en: "Current agent", zh: "当前专家" },
  "composer.agentPicker.subAgent": { en: "{count} sub-agent", zh: "{count} 个子专家" },
  "composer.agentPicker.subAgents": { en: "{count} sub-agents", zh: "{count} 个子专家" },
  "composer.agentPicker.empty": { en: "No matching agents", zh: "没有匹配的专家" },

  // the skill picker (components/skill-picker.tsx)
  "composer.skillPicker.title": { en: "Use a skill", zh: "使用技能" },
  "composer.skillPicker.description": {
    en: "The skill is read by the model before it works — pick one, then say what to do.",
    zh: "模型在开始前会读取技能 —— 选一个，然后说明要做什么。",
  },
  "composer.skillPicker.filter": { en: "Filter skills", zh: "筛选技能" },
  "composer.skillPicker.agent": { en: "agent: ", zh: "专家: " },
  "composer.skillPicker.empty": {
    en: "No skill matches. Skills come from .openheron/skills/<name>/SKILL.md.",
    zh: "没有匹配的技能。技能来自 .openheron/skills/<name>/SKILL.md。",
  },

  // the authorization picker (components/authorization-picker.tsx)
  "composer.authorization.ariaLabel": { en: "Authorization: {mode}", zh: "授权：{mode}" },
  "composer.authorization.stagedTitle": {
    en: "the mode belongs to the conversation that is about to start.",
    zh: "该模式属于即将开始的对话。",
  },
  "composer.authorization.title": { en: "Authorization mode", zh: "授权模式" },
  "composer.authorization.descriptionActive": {
    en: "The mode belongs to this conversation. A running turn keeps the mode it started with, so a change you make now takes effect on the next turn.",
    zh: "该模式属于当前对话。正在运行的轮次会保持其开始时的模式，因此你现在所做的更改将在下一轮生效。",
  },
  "composer.authorization.descriptionStaged": {
    en: "The mode belongs to the conversation that is about to start. It is sent when that conversation is created, so the first turn already runs in it.",
    zh: "该模式属于即将开始的对话。它会在该对话创建时一并发送，因此第一轮就已经在该模式下运行。",
  },
  "composer.authorization.modesAriaLabel": { en: "Authorization modes", zh: "授权模式" },
  "composer.authorization.currentMode": { en: "Current mode", zh: "当前模式" },
  "composer.authorization.confirmed": {
    en: "Already confirmed for this conversation — it will not ask again.",
    zh: "本对话已确认 —— 不会再询问。",
  },
  "composer.authorization.confirmDescription": {
    en: "Full authorization — every tool runs without asking, including shell commands and configuration changes.",
    zh: "完全授权 —— 所有工具都无需询问即可运行，包括 shell 命令和配置更改。",
  },
  "composer.authorization.enableFull": { en: "Enable full authorization", zh: "启用完全授权" },

  // the project table (components/projects-dialog.tsx)
  "composer.projects.ariaLabel": { en: "Project: {name}", zh: "项目：{name}" },
  "composer.projects.labelTitle": {
    en: "{name} — the project this conversation works in. A conversation's project cannot change — start a new conversation to work elsewhere.",
    zh: "{name} —— 当前对话所在的项目。对话的项目无法更改 —— 如需在其他位置工作，请开始新对话。",
  },
  "composer.projects.buttonTitle": {
    en: "{name} — the project the next conversation works in. Click to choose another one, or to make a new one.",
    zh: "{name} —— 下一个对话将所在的项目。点击以选择其他项目或新建项目。",
  },
  "composer.projects.title": { en: "Projects", zh: "项目" },
  "composer.projects.description": {
    en: "A project is a directory. The conversations that belong to one work in its directory, and they are listed under it in the sidebar.",
    zh: "项目是一个目录。属于某个项目的对话会在其目录中工作，并在侧边栏中列在该项目下。",
  },
  "composer.projects.empty": {
    en: "No projects yet. The conversations you start now work in the default project root.",
    zh: "还没有项目。你现在开始的对话将在默认项目根目录中工作。",
  },
  "composer.projects.defaultRoot": { en: "the default project root", zh: "默认项目根目录" },
  "composer.projects.currentTitle": {
    en: "The project this conversation works in",
    zh: "当前对话所在的项目",
  },
  "composer.projects.current": { en: "Current", zh: "当前" },
  "composer.projects.sessionOne": { en: "{count} session", zh: "{count} 个会话" },
  "composer.projects.sessionMany": { en: "{count} sessions", zh: "{count} 个会话" },
  "composer.projects.new": { en: "New project", zh: "新建项目" },
  "composer.projects.namePlaceholder": {
    en: "Name (optional — the directory's own name is used)",
    zh: "名称（可选 —— 留空则使用目录名）",
  },
  "composer.projects.nameLabel": { en: "Project name", zh: "项目名称" },
  "composer.projects.rootPlaceholder": { en: "Root directory", zh: "根目录" },
  "composer.projects.rootLabel": { en: "Project root", zh: "项目根目录" },
  "composer.projects.chooseDirectory": { en: "Choose a directory", zh: "选择目录" },
  "composer.projects.browseTitle": {
    en: "Open the file browser and choose a directory",
    zh: "打开文件浏览器并选择目录",
  },
  "composer.projects.browse": { en: "Browse", zh: "浏览" },
  "composer.projects.create": { en: "Create project", zh: "创建项目" },
  "composer.projects.changeFolderDescription": {
    en: "The directory {name} points at. The conversations in it work in that directory.",
    zh: "{name} 指向的目录。其中的对话会在该目录中工作。",
  },
} as const

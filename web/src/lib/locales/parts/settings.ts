// ★ The settings dialog's copy: the tabs, the file list, the secrets panel
//   and the MCP panel.
export const settings = {
  "settings.title": { en: "Settings", zh: "设置" },
  "settings.close": { en: "Close settings", zh: "关闭设置" },
  "settings.sections": { en: "Settings sections", zh: "设置分区" },
  "settings.tab.files": { en: "Files", zh: "文件" },
  "settings.tab.secrets": { en: "Secrets", zh: "密钥" },
  "settings.description.secrets": { en: "the keys, and where each one comes from", zh: "密钥，以及每个密钥的来源" },
  "settings.description.none": { en: "no file selected", zh: "未选择文件" },
  "settings.notHotReloaded": {
    en: "This section is not hot-reloaded; changes apply after a restart.",
    zh: "此分区不支持热重载；更改将在重启后生效。",
  },
  "settings.selectFile": { en: "Select a file", zh: "选择文件" },
  "settings.section.llm": { en: "Providers", zh: "服务商" },
  "settings.section.server": { en: "Server", zh: "服务端" },
  "settings.section.tools": { en: "Tools", zh: "工具" },
  "settings.section.mcps": { en: "MCP servers", zh: "连接器" },
  "settings.sectionPicker": { en: "Config section", zh: "配置分区" },
  "settings.fileList.empty": { en: "No files yet", zh: "暂无文件" },
  "settings.fileList.restartBadge": { en: "restart", zh: "重启" },
  "settings.fileList.restartTitle": { en: "requires a restart", zh: "需要重启" },

  "settings.footer.saving": { en: "Saving…", zh: "保存中…" },
  "settings.footer.validating": { en: "Validating…", zh: "校验中…" },
  "settings.footer.valid": { en: "Valid", zh: "有效" },
  "settings.footer.savedApplied": { en: "Saved · applied", zh: "已保存 · 已生效" },
  "settings.footer.savedRestart": { en: "Saved · restart openheron to apply", zh: "已保存 · 重启 openheron 后生效" },
  "settings.footer.unsaved": { en: "Unsaved changes", zh: "有未保存的更改" },
  "settings.footer.conflict": {
    en: "This file changed on disk — reload or overwrite.",
    zh: "此文件在磁盘上已更改 —— 请重新加载或覆盖。",
  },
  "settings.footer.openFile": { en: "Open {path}", zh: "打开 {path}" },

  "settings.newFile.button": { en: "+ New file", zh: "+ 新建文件" },
  "settings.newFile.title": { en: "New file", zh: "新建文件" },
  "settings.newFile.description": {
    en: "The skeleton is intentionally incomplete — the server rejects it until you fill in what it requires.",
    zh: "骨架特意留得不完整 —— 在填齐必填项之前，服务端会拒绝它。",
  },
  "settings.newFile.section": { en: "Section", zh: "分区" },
  "settings.newFile.fileName": { en: "File name", zh: "文件名" },
  "settings.newFile.skillName": { en: "Skill name (its folder)", zh: "技能名称（其所在文件夹）" },
  "settings.newFile.content": { en: "Content", zh: "内容" },
  "settings.newFile.reset": { en: "Reset to template", zh: "重置为模板" },
  "settings.newFile.models": { en: "available models: {models}", zh: "可用模型：{models}" },

  "settings.conflict.title": { en: "This file changed on disk", zh: "此文件在磁盘上已更改" },
  "settings.conflict.description": {
    en: "You edited an older version. Overwriting discards the on-disk version permanently.",
    zh: "你编辑的是较旧的版本。覆盖将永久丢弃磁盘上的版本。",
  },
  "settings.conflict.yourVersion": { en: "Your version", zh: "你的版本" },
  "settings.conflict.onDisk": { en: "On disk", zh: "磁盘上的版本" },
  "settings.conflict.changedByOther": { en: "changed by someone else", zh: "被其他人更改" },
  "settings.conflict.reload": { en: "Reload from disk", zh: "从磁盘重新加载" },
  "settings.conflict.keepEditing": { en: "Keep editing", zh: "继续编辑" },

  "settings.secrets.empty": { en: "Nothing here yet", zh: "这里还没有内容" },
  "settings.secrets.removeAll": { en: "Remove all {group}", zh: "移除全部 {group}" },
  "settings.secrets.noGroups": { en: "No providers or MCP servers yet", zh: "暂无服务商或连接器" },
  "settings.secrets.select": { en: "Select a secret", zh: "选择密钥" },
  "settings.secrets.storedIn": { en: "Stored in", zh: "存储于" },
  "settings.secrets.inEffect": { en: "In effect", zh: "生效中" },
  "settings.secrets.environment": { en: "Environment", zh: "环境变量" },
  "settings.secrets.pasteKey": { en: "Paste the key", zh: "粘贴密钥" },
  "settings.secrets.setValue": { en: "Set value", zh: "设置值" },
  "settings.secrets.note": {
    en: "The value goes to the keyring only — it is never written into the configuration, and it is never shown here again.",
    zh: "该值仅存入密钥环 —— 绝不写入配置，也不会再次在这里显示。",
  },
  "settings.secrets.removeTitle": { en: "Remove the stored secrets?", zh: "移除已存储的密钥？" },

  "settings.mcp.descriptionBefore": { en: "The servers of agent", zh: "专家的连接器" },
  "settings.mcp.descriptionAfter": { en: "— what it would get from them.", zh: "—— 它能从中获得什么。" },
  "settings.mcp.asking": { en: "Asking the servers…", zh: "正在询问连接器…" },
  "settings.mcp.liveState": { en: "Live state, read on open.", zh: "实时状态，打开时读取。" },
  "settings.mcp.refresh": { en: "Refresh", zh: "刷新" },
  "settings.mcp.empty": { en: "No MCP servers are configured", zh: "未配置连接器" },
  "settings.mcp.tool": { en: "Tool", zh: "工具" },
  "settings.mcp.exposedName": { en: "Exposed name", zh: "对外名称" },
  "settings.mcp.tier": { en: "Tier", zh: "级别" },
  "settings.mcp.state": { en: "State", zh: "状态" },

  // the line one issue reads as (lib/settings-validate.ts)
  // ★ The message inside it is the **server's** sentence — the runtime speaks
  //   English — and it comes in as a parameter, not as part of this key.
  "settings.issue.line": { en: "line {line}: {message}", zh: "第 {line} 行：{message}" },
} as const

// ★ The header's copy: the frame's own labels (the sidebar button, the
//   language switch, the status dot).
export const header = {
  "header.showSidebar": { en: "Show sidebar", zh: "显示侧边栏" },
  "header.hideSidebar": { en: "Hide sidebar", zh: "隐藏侧边栏" },
  "header.openConversations": { en: "Open conversations", zh: "打开对话列表" },
  "header.closeConversations": { en: "Close conversations", zh: "关闭对话列表" },
  "header.locale.toEn": { en: "Switch to English", zh: "切换到英文" },
  "header.locale.toZh": { en: "Switch to Chinese", zh: "切换到中文" },
  "header.status.idle": { en: "Idle", zh: "空闲" },
  "header.status.running": { en: "Running", zh: "运行中" },
  "header.status.awaitingApproval": { en: "Waiting for approval", zh: "等待审批" },
  "header.status.error": { en: "Failed", zh: "失败" },
  // ★ The theme toggle names the theme it switches to, like the locale
  //   toggle names the language. Its aria-label/title used to be written
  //   straight into the component — now it is copy like everything else.
  "header.theme.toLight": { en: "Switch to light theme", zh: "切换到浅色主题" },
  "header.theme.toDark": { en: "Switch to dark theme", zh: "切换到深色主题" },
} as const

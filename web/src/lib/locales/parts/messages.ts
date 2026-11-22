// ★ The conversation's copy: the message list, the tool cards, the markdown,
//   the approval card and the activity timeline.
//
// ★ The English here is the text the interface shows **today**, byte for
//   byte: the E2E suite asserts those sentences, and a wording that drifts
//   is a wording the tests would not catch.
//
// ★ The words that already have a key of their own are **not** repeated
//   here: the cancellation is `common.cancel`, the spinner is
//   `common.loading`, the failed call is `tool.phase.failed`, and the
//   authorization levels are `authorization.*`. ★ A word that means the
//   same thing in two places is written once — a second copy of it is a
//   second thing to translate.
export const messages = {
  // the message list (components/message-list.tsx)
  "messages.jumpToLatest": { en: "Jump to latest", zh: "跳到最新" },
  "messages.runFailed": { en: "Run failed", zh: "运行失败" },
  "messages.askAnything": { en: "Ask anything about this workspace.", zh: "关于这个工作区，尽管提问。" },
  "messages.working": { en: "Working…", zh: "处理中…" },
  "messages.stopGenerating": { en: "Stop generating", zh: "停止生成" },

  // the reasoning block (components/message-parts.tsx)
  "messages.collapse": { en: "collapse", zh: "收起" },

  // the tool card and the tool group's bar (tool-card.tsx, tool-group.tsx)
  "messages.toolLines": { en: "{count} lines", zh: "{count} 行" },
  "messages.toolSteps": { en: "{count} steps", zh: "{count} 步" },
  "messages.toolFailedCount": { en: "{count} failed", zh: "{count} 个失败" },

  // the file preview (components/file-preview.tsx)
  "messages.closePreview": { en: "Close preview", zh: "关闭预览" },
  "messages.fileGone": { en: "The file is no longer in the session.", zh: "该文件已不在当前会话中。" },
  "messages.previewTruncated": {
    en: "Showing the first {size} KiB. Download the file to read the rest.",
    zh: "仅显示前 {size} KiB。下载文件以查看其余内容。",
  },
  "messages.download": { en: "Download", zh: "下载" },

  // the approval card (components/approval-card.tsx)
  "messages.approvalPaused": { en: "The agent is paused until you respond.", zh: "在你响应之前，专家已暂停。" },
  "messages.requestedBy": { en: "Requested by {agent}", zh: "由 {agent} 请求" },
  "messages.rejectionReason": { en: "Rejection reason", zh: "拒绝原因" },
  "messages.rejectionPlaceholder": { en: "Reason (sent back to the model)", zh: "原因（将回传给模型）" },
  "messages.confirmDenial": { en: "Confirm denial", zh: "确认拒绝" },
  "messages.approve": { en: "Approve", zh: "批准" },
  "messages.approveAll": { en: "Approve all", zh: "全部批准" },
  "messages.deny": { en: "Deny", zh: "拒绝" },
  "messages.denyAll": { en: "Deny all", zh: "全部拒绝" },

  // the activity timeline (components/operations-panel.tsx)
  "messages.sessionActivity": { en: "Session activity", zh: "会话活动" },
  "messages.activity": { en: "Activity", zh: "活动" },
  "messages.operationsTooltip": {
    en: "What happened in this conversation — the session's operation log.",
    zh: "本对话中发生的事 —— 会话的操作日志。",
  },
  "messages.operationsDescription": {
    en: "What happened in this conversation, oldest first. The log is the server's record.",
    zh: "本对话中发生的事，按时间从早到晚。日志是服务端记录。",
  },
  "messages.noActivity": { en: "No activity yet.", zh: "暂无活动。" },

  // one line of the log: the sentence the client writes for the server's
  // kind (components/operations-panel.tsx)
  "messages.op.sessionCreated": { en: "Session created.", zh: "会话已创建。" },
  "messages.op.sessionRenamed": { en: "Session renamed to \"{title}\".", zh: "会话已重命名为“{title}”。" },
  "messages.op.agentChanged": { en: "Agent changed to {agent}.", zh: "专家已切换为 {agent}。" },
  "messages.op.fullAuthorization": { en: "Full authorization confirmed.", zh: "已确认完全授权。" },
  "messages.op.authorizationChanged": {
    en: "Authorization mode changed to {mode}.",
    zh: "授权模式已切换为 {mode}。",
  },
  "messages.op.fileUploaded": { en: "File uploaded: {file}.", zh: "已上传文件：{file}。" },
  "messages.op.fileDeleted": { en: "File deleted: {file}.", zh: "已删除文件：{file}。" },
  "messages.op.turnResumed": {
    en: "Turn resumed — the round picked up where it stopped.",
    zh: "轮次已恢复 —— 从中断处继续。",
  },
  "messages.op.turnStarted": { en: "Turn started — a new question.", zh: "轮次已开始 —— 新的提问。" },
  "messages.op.turnFinished": { en: "Turn finished: {state}.", zh: "轮次已结束：{state}。" },
  "messages.op.approvalRequested": { en: "Approval requested for {tool}.", zh: "已请求对 {tool} 的审批。" },
  "messages.op.approvalResolved": { en: "Approval {decision} for {tool}.", zh: "{tool} 的审批已{decision}。" },

  // ★ The words a line falls back to when the server named no one: they
  //   are the client's own sentences, so they are written here too.
  "messages.op.defaultTitle": { en: "a new title", zh: "新标题" },
  "messages.op.defaultAgent": { en: "another agent", zh: "另一个专家" },
  "messages.op.defaultFile": { en: "a file", zh: "一个文件" },
  "messages.op.defaultTool": { en: "a tool", zh: "一个工具" },

  // the identifiers a line is about (components/operations-panel.tsx)
  "messages.op.detailRun": { en: "run {id}", zh: "运行 {id}" },
  "messages.op.detailInterrupt": { en: "interrupt {id}", zh: "中断 {id}" },
  "messages.op.detailAgent": { en: "agent {id}", zh: "专家 {id}" },
  "messages.op.detailProject": { en: "project {id}", zh: "项目 {id}" },

  // the round's ending (components/operations-panel.tsx)
  "messages.op.state.completed": { en: "completed", zh: "已完成" },
  "messages.op.state.failed": { en: "failed", zh: "失败" },
  "messages.op.state.aborted": { en: "aborted", zh: "已中止" },
  "messages.op.state.interrupted": { en: "interrupted", zh: "已中断" },
  "messages.op.state.waitingApproval": { en: "waiting for approval", zh: "等待审批" },
  "messages.op.state.finished": { en: "finished", zh: "已结束" },

  // the answer to an approval (components/operations-panel.tsx)
  "messages.op.decisionResolved": { en: "resolved", zh: "已处理" },
  "messages.op.decisionApproved": { en: "approved", zh: "已批准" },
  "messages.op.decisionRefused": { en: "refused", zh: "已拒绝" },

  // the compaction boundary (components/compaction-divider.tsx)
  "messages.tokensSaved": { en: "≈{tokens} tokens saved", zh: "节省约 {tokens} token" },
  "messages.earlierMessages": { en: "{count} earlier messages", zh: "更早的 {count} 条消息" },
  "messages.earlierMessagesPlain": { en: "Earlier messages", zh: "更早的消息" },
  "messages.compactedIntoSummary": { en: "compacted into a summary", zh: "压缩为摘要" },
  "messages.compactionReadsSummary": {
    en: "The model reads a summary of the messages above this line, not the messages themselves.",
    zh: "模型读取的是这条线上方消息的摘要，而不是消息本身。",
  },
  "messages.compactionStillThere": {
    en: "The messages are still in the transcript.",
    zh: "这些消息仍在记录中。",
  },
  "messages.compactionSuperseded": { en: "Superseded messages: {count}", zh: "已被取代的消息：{count}" },
  "messages.compactionEstimatedTokens": {
    en: "Estimated tokens saved: {count}",
    zh: "预计节省的 token：{count}",
  },
} as const

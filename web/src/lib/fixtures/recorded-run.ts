/**
 * 真后端录下的一轮事件序列（`POST /v2/run` 的 SSE 流，timestamp 已剥掉）。
 *
 * ★ 为什么用录的而不是手编的：手编的事件序列只覆盖「作者想到的分支」，
 *   而这个 fixture 里的 14 种事件类型都是真后端实际发过的 —— 包括
 *   reasoning 与正文共用 messageId、工具调用带 parentMessageId 这些细节。
 *   Phase 1 之所以漏掉 role 字面量，正是因为手编 fixture 不会告诉你
 *   前端 schema 到底认什么。
 *
 * 为了体积，连续的同类型 delta 已合并；长文本截断到 200 字符。
 * 形状（START / CONTENT×N / END）与顺序原样保留。
 *
 * ★ 后端后来加了两样东西，这份 fixture 是**回填**的（不是重录）：
 *
 *   ① TEXT_MESSAGE_START 带 `name` —— 产出这条消息的 agent。后端从
 *      2026-10-22 起发这个字段（多 agent 模式要靠它归属）。
 *   ② 只有推理/工具调用、没有正文的消息，末尾多一对
 *      TEXT_MESSAGE_START / TEXT_MESSAGE_END —— 那是「声明」，用来说明
 *      这条消息属于哪个 agent（REASONING_* / TOOL_CALL_* 不带归属）。
 *      见 agtransport/transport.go 的 emitter.announce。
 *
 *   回填而不是重录，是因为重录要一台真后端 + 真模型，而这两样都不在手边；
 *   而 fixture 的价值是「后端变了什么，这里一眼看得见」——
 *   留着旧形状，它就慢慢不再代表后端了。
 */
/**
 * ★ 类型标成 AgEvent[] 而不是 Record<string, unknown>[]。
 *   标成后者时测试里必须 `as AgEvent` 强转，于是「这份 fixture 到底还对不对」
 *   永远没人验 —— 后端改了字段名，测试照样跑。
 *   标成前者，录下来的真实事件一旦与本地类型不符就是编译错误。
 */
import type { AgEvent } from "../events"

export const recordedRun: AgEvent[] = [
 {
  "type": "RUN_STARTED",
  "threadId": "64000d91-92be-481a-8858-42c50c8456f5",
  "runId": "205b23c1-5ba8-480f-814d-15265994f1a3"
 },
 {
  "type": "REASONING_START",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858"
 },
 {
  "type": "REASONING_MESSAGE_START",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858",
  "role": "reasoning"
 },
 {
  "type": "REASONING_MESSAGE_CONTENT",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858",
  "delta": "The user wants me to read the first line of go.mod. Let me find it."
 },
 {
  "type": "REASONING_MESSAGE_END",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858"
 },
 {
  "type": "REASONING_END",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858"
 },
 {
  "type": "TOOL_CALL_START",
  "toolCallId": "call_00_X3XBhfYdIxQfxGk3mRgN2489",
  "toolCallName": "glob",
  "parentMessageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858"
 },
 {
  "type": "TOOL_CALL_ARGS",
  "toolCallId": "call_00_X3XBhfYdIxQfxGk3mRgN2489",
  "delta": "{\"pattern\": \"/home/user/code/openheron/**/go.mod\", \"path\": \"/home/user/code/openheron\"}"
 },
 {
  "type": "TOOL_CALL_END",
  "toolCallId": "call_00_X3XBhfYdIxQfxGk3mRgN2489"
 },
 {
  "type": "TOOL_CALL_RESULT",
  "messageId": "cd269a90-ebbe-4e49-8961-ecb862143a56",
  "toolCallId": "call_00_X3XBhfYdIxQfxGk3mRgN2489",
  "content": "No files found",
  "role": "tool"
 },
 {
  "type": "TEXT_MESSAGE_START",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858",
  "role": "assistant",
  "name": "main"
 },
 {
  "type": "TEXT_MESSAGE_END",
  "messageId": "f62f5e33-b873-4d4a-9f48-486c3a5c6858"
 },
 {
  "type": "REASONING_START",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae"
 },
 {
  "type": "REASONING_MESSAGE_START",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae",
  "role": "reasoning"
 },
 {
  "type": "REASONING_MESSAGE_CONTENT",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae",
  "delta": "Hmm, no go.mod found by glob? Maybe glob path semantics. Let me try ls the root."
 },
 {
  "type": "REASONING_MESSAGE_END",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae"
 },
 {
  "type": "REASONING_END",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae"
 },
 {
  "type": "TOOL_CALL_START",
  "toolCallId": "call_00_JCMAjRvfuPOet3cgfukY6897",
  "toolCallName": "ls",
  "parentMessageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae"
 },
 {
  "type": "TOOL_CALL_ARGS",
  "toolCallId": "call_00_JCMAjRvfuPOet3cgfukY6897",
  "delta": "{\"path\": \"/home/user/code/openheron\"}"
 },
 {
  "type": "TOOL_CALL_START",
  "toolCallId": "call_01_zFFwcsTkjiXEuhIiEJN36739",
  "toolCallName": "glob",
  "parentMessageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae"
 },
 {
  "type": "TOOL_CALL_ARGS",
  "toolCallId": "call_01_zFFwcsTkjiXEuhIiEJN36739",
  "delta": "{\"pattern\": \"**/go.mod\", \"path\": \"/home/user/code/openheron\"}"
 },
 {
  "type": "TOOL_CALL_END",
  "toolCallId": "call_00_JCMAjRvfuPOet3cgfukY6897"
 },
 {
  "type": "TOOL_CALL_END",
  "toolCallId": "call_01_zFFwcsTkjiXEuhIiEJN36739"
 },
 {
  "type": "TOOL_CALL_RESULT",
  "messageId": "2044605f-c509-4339-9c31-20febbef1bdb",
  "toolCallId": "call_00_JCMAjRvfuPOet3cgfukY6897",
  "content": ".air.toml\n.git\n.gitignore\n.openheron\n.openheron-example\n.opencode\nAGENTS.md\nMakefile\na2ui\nagent\nagtransport\napi\nconfig\ndocs\ngo.mod\ngo.sum\nhelpers\nheronrt\ninputctx\ninternal\nllm\nmain.go\nmem\nmsgops\nscripts\nsessi…(truncated)",
  "role": "tool"
 },
 {
  "type": "TOOL_CALL_RESULT",
  "messageId": "955f0993-5111-4cd9-aba3-08923946f056",
  "toolCallId": "call_01_zFFwcsTkjiXEuhIiEJN36739",
  "content": "go.mod",
  "role": "tool"
 },
 {
  "type": "TOOL_CALL_START",
  "toolCallId": "call_00_ET_cmkvV89lPHMl5iWTaK3v4829",
  "toolCallName": "read_file",
  "parentMessageId": "4103abd6-6861-4948-816e-c1bbfa12c3c8"
 },
 {
  "type": "TOOL_CALL_ARGS",
  "toolCallId": "call_00_ET_cmkvV89lPHMl5iWTaK3v4829",
  "delta": "{\"file_path\": \"/home/user/code/openheron/go.mod\", \"limit\": 5, \"offset\": 1}"
 },
 {
  "type": "TOOL_CALL_END",
  "toolCallId": "call_00_ET_cmkvV89lPHMl5iWTaK3v4829"
 },
 {
  "type": "TOOL_CALL_RESULT",
  "messageId": "61b699b2-d042-4cc2-b8f2-dc2b5404fa8e",
  "toolCallId": "call_00_ET_cmkvV89lPHMl5iWTaK3v4829",
  "content": "     1\tmodule openheron\n     2\t\n     3\tgo 1.27.1\n     4\t\n     5\trequire (",
  "role": "tool"
 },
 {
  "type": "TEXT_MESSAGE_START",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae",
  "role": "assistant",
  "name": "main"
 },
 {
  "type": "TEXT_MESSAGE_END",
  "messageId": "4ff2e61b-2dbc-4add-9ae9-a3c26167fcae"
 },
 {
  "type": "REASONING_START",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c"
 },
 {
  "type": "REASONING_MESSAGE_START",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c",
  "role": "reasoning"
 },
 {
  "type": "REASONING_MESSAGE_CONTENT",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c",
  "delta": "Wait, go 1.27.1? That's odd but fine. Actually maybe the file content is different. Let me just report. Actually let me double check — line 3 says \"go 1.27.1\". Hmm, that could be a hallucination risk,…(truncated)"
 },
 {
  "type": "REASONING_MESSAGE_END",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c"
 },
 {
  "type": "REASONING_END",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c"
 },
 {
  "type": "TEXT_MESSAGE_START",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c",
  "role": "assistant",
  "name": "main"
 },
 {
  "type": "TEXT_MESSAGE_CONTENT",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c",
  "delta": "`/home/user/code/openheron/go.mod` 的第一行是：\n\n```\nmodule openheron\n```\n\n文件开头几行：\n```\n1  module openheron\n2  (空行)\n3  go 1.27.1\n4  (空行)\n5  require (\n```\n\n补充一点观察：在整个仓库里搜 `**/go.mod` 只匹配到根目录这一个 `go…(truncated)"
 },
 {
  "type": "TEXT_MESSAGE_END",
  "messageId": "7cea748c-fb52-48b8-aa97-47db6cc8579c"
 },
 {
  "type": "RUN_FINISHED",
  "threadId": "64000d91-92be-481a-8858-42c50c8456f5",
  "runId": "205b23c1-5ba8-480f-814d-15265994f1a3",
  "outcome": {
   "type": "success"
  }
 }
]

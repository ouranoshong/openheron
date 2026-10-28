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

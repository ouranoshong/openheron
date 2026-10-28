/**
 * 真后端 SSE 流的原始帧（前 10 块，原样保留 `data:` 无空格的形式）。
 *
 * ★ 分帧器必须用**真帧**测。手写 `data: {...}` 会顺手带上空格，
 *   而真后端是不带的 —— 那正是当初前端收不到任何内容的原因。
 */
export const recordedFrames: string[] = [
 "data:{\"type\":\"RUN_STARTED\",\"timestamp\":1791868198808,\"threadId\":\"64000d91-92be-481a-8858-42c50c8456f5\",\"runId\":\"205b23c1-5ba8-480f-814d-15265994f1a3\"}",
 "data:{\"type\":\"REASONING_START\",\"timestamp\":1791868199737,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\"}",
 "data:{\"type\":\"REASONING_MESSAGE_START\",\"timestamp\":1791868199737,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"role\":\"reasoning\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199737,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\"The\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199796,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\" user\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199820,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\" wants\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199820,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\" me\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199820,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\" to\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199820,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\" read\"}",
 "data:{\"type\":\"REASONING_MESSAGE_CONTENT\",\"timestamp\":1791868199820,\"messageId\":\"f62f5e33-b873-4d4a-9f48-486c3a5c6858\",\"delta\":\" the\"}"
]

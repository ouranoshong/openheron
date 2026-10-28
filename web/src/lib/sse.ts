/**
 * SSE 分帧。
 *
 * ★ 为什么自己写而不用 @ag-ui/client：
 *
 *   1. 它发的请求体是 `{threadId, runId, messages, tools, context,
 *      forwardedProps}` —— 没有 `query` 字段，而那正是后端要的
 *      （见 api/v2_run.go）。要用就得在它上面再糊一层。
 *   2. 它自己还维护一份 messages/state，我们不读 —— 就是 docs 里反复
 *      警告的「两个事实源」，上一版一半的 bug 出在这里。
 *   3. 它锁版本且内置 4 层向后兼容（BackwardCompatibility_0_0_39…57），
 *      换后端版本时是额外的排查面。
 *
 *   分帧本身只有几十行，而且可以对着真后端录的帧测。
 *
 * ★ 帧格式：冒号后**没有空格**。
 *   sseio.Stream 每写一个换行就切出一个 SSE 事件，hertz 输出成 `data:{...}`。
 *   按 `data: ` 严格匹配的解析器会一条都收不到 —— 而且不报错，只是静默为空。
 *   这里两种都认，但断言的是无空格那种。
 */

export interface SseEvent {
  /** `data:` 之后的原始负载。 */
  data: string
  /** 事件名（`event:` 字段）。openheron 不发，恒为 undefined。 */
  event?: string
}

/**
 * takeEvents 从累积的缓冲里取出**完整**的事件块。
 *
 * 返回 rest 留给下一次调用 —— 网络分片会把一个 JSON 拦腰截断，
 * 任何「一次读到就解析」的做法都会在流式下随机炸掉。
 *
 * @param buffer 上一次剩下的 + 这次新到的
 */
export function takeEvents(buffer: string): { events: SseEvent[]; rest: string } {
  const events: SseEvent[] = []
  let rest = buffer

  // 事件之间以空行分隔。兼容 \n\n 与 \r\n\r\n。
  for (;;) {
    const idx = findBoundary(rest)
    if (idx === null) break // ← 返回的是对象或 null，跟数字比大小永远不成立
    const block = rest.slice(0, idx.start)
    rest = rest.slice(idx.end)

    const ev = parseBlock(block)
    if (ev) events.push(ev)
  }

  return { events, rest }
}

function findBoundary(s: string): { start: number; end: number } | null {
  const lf = s.indexOf("\n\n")
  const crlf = s.indexOf("\r\n\r\n")
  if (lf < 0 && crlf < 0) return null
  if (crlf >= 0 && (lf < 0 || crlf < lf)) return { start: crlf, end: crlf + 4 }
  return { start: lf, end: lf + 2 }
}

/**
 * parseBlock 把一个事件块解成 SseEvent。
 *
 * 按 SSE 规范：多行 data 用 \n 连接；`:` 开头是注释，忽略；
 * 空块（只有心跳注释）返回 null。
 */
function parseBlock(block: string): SseEvent | null {
  const dataLines: string[] = []
  let event: string | undefined

  for (const raw of block.split(/\r?\n/)) {
    const line = raw.trimEnd()
    if (line === "" || line.startsWith(":")) continue

    const colon = line.indexOf(":")
    const field = colon < 0 ? line : line.slice(0, colon)
    // 冒号后恰好一个可选空格 —— `data:{}` 与 `data: {}` 都要收成 `{}`。
    let value = colon < 0 ? "" : line.slice(colon + 1)
    if (value.startsWith(" ")) value = value.slice(1)

    if (field === "data") dataLines.push(value)
    else if (field === "event") event = value
  }

  if (dataLines.length === 0) return null
  const out: SseEvent = { data: dataLines.join("\n") }
  if (event) out.event = event
  return out
}

/**
 * ReadableStreamDefaultReader → SseEvent[] 的异步迭代器。
 *
 * 用法：
 *
 * ```ts
 * for await (const ev of readSse(res.body!)) {
 *   handle(JSON.parse(ev.data))
 * }
 * ```
 *
 * 结束时正常收尾；中途出错抛出（由调用方决定转成 RUN_ERROR 还是别的）。
 */
export async function* readSse(body: ReadableStream<Uint8Array>): AsyncGenerator<SseEvent> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })

      const { events, rest } = takeEvents(buffer)
      buffer = rest
      for (const ev of events) yield ev
    }
    // 服务端在最后一个事件后直接断开（openheron 不发终止标记）。
    buffer += decoder.decode()
    const { events } = takeEvents(buffer + "\n\n")
    for (const ev of events) yield ev
  } finally {
    reader.releaseLock()
  }
}


export interface SseEvent {
  data: string
  event?: string
}

export function takeEvents(buffer: string): { events: SseEvent[]; rest: string } {
  const events: SseEvent[] = []
  let rest = buffer

  for (;;) {
    const idx = findBoundary(rest)
    if (idx === null) break
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

function parseBlock(block: string): SseEvent | null {
  const dataLines: string[] = []
  let event: string | undefined

  for (const raw of block.split(/\r?\n/)) {
    const line = raw.trimEnd()
    if (line === "" || line.startsWith(":")) continue

    const colon = line.indexOf(":")
    const field = colon < 0 ? line : line.slice(0, colon)
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
    buffer += decoder.decode()
    const { events } = takeEvents(buffer + "\n\n")
    for (const ev of events) yield ev
  } finally {
    reader.releaseLock()
  }
}

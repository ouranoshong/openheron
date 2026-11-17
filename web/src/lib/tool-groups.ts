import type { AttachmentPart, Part, ReasoningPart, TextPart, ToolPart } from "@/lib/types"
import { toolTitle } from "@/lib/tool-titles"

// ToolRun is a stretch of tool calls that follow each other.
export interface ToolRun {
  parts: ToolPart[]
  // key is the first call's id.
  //
  // ★ A run grows at its end while the turn runs, and the key must not
  //   change when it does: the open state lives in the node, and a node
  //   that is unmounted and mounted again loses it.
  key: string
}

// OtherPart is every part of a message that is not a tool call.
export type OtherPart = TextPart | ReasoningPart | AttachmentPart

// Segment is one piece of a message: either a part of its own, or a run of
// tool calls.
export type Segment =
  | { kind: "part"; part: OtherPart }
  | { kind: "run"; run: ToolRun }

/**
 * segments groups the tool calls that follow each other.
 *
 * ★ The boundary is **one message**: the model's own thinking sits between
 *   two stretches of calls, and the thinking is what the reader came for —
 *   folding a call into the group on the other side of it would hide it.
 *
 * ★ It is a **pure** function: no framework, no component. `bun test` proves
 *   it on its own.
 */
export function segments(parts: Part[]): Segment[] {
  const out: Segment[] = []
  let run: ToolPart[] = []

  const flush = () => {
    if (run.length === 0) return
    out.push({ kind: "run", run: { parts: run, key: run[0]!.id } })
    run = []
  }

  for (const part of parts) {
    if (part.kind === "tool") {
      run.push(part)
      continue
    }
    flush()
    out.push({ kind: "part", part })
  }
  flush()

  return out
}

// RunPhase is the group's own state: the liveliest call in it decides.
export type RunPhase = "streaming" | "running" | "complete" | "failed"

// RunSummary is what the group's bar says about the run.
export interface RunSummary {
  count: number
  // titles are the tools the run used, deduped, in the order they ran.
  //
  // ★ They are the **kinds** of work, not the outcome: a result's shape
  //   belongs to the tool that produced it, and those tools live upstream —
  //   so a summary this side would be a guess. ⇒ The bar says what ran, and
  //   the failure says what went wrong.
  titles: string[]
  // failed counts the calls that came back with an error.
  failed: number
  phase: RunPhase
}

/**
 * summarize reads a run: how many calls it holds, which tools they used,
 * and how the run ended.
 *
 * ★ The phase is the **liveliest** call in it: a run with one call still
 *   going is a run that is still going, whatever else in it already
 *   finished.
 */
export function summarize(run: ToolRun): RunSummary {
  const titles: string[] = []
  let failed = 0
  let streaming = false
  let running = false

  for (const p of run.parts) {
    const title = toolTitle(p.name)
    if (!titles.includes(title)) titles.push(title)
    if (p.isError) failed++
    if (p.phase === "streaming") streaming = true
    if (p.phase === "running") running = true
  }

  return {
    count: run.parts.length,
    titles,
    failed,
    phase: streaming ? "streaming" : running ? "running" : failed > 0 ? "failed" : "complete",
  }
}

// PHASE_LABEL is the word the interface gives a call's state.
//
// ★ It is **English** (AGENTS.md: interface copy → English), and it lives
//   here because the card and the group's bar are the two places that say it.
const PHASE_LABEL: Record<RunPhase, string> = {
  streaming: "reading arguments",
  running: "running",
  complete: "done",
  failed: "failed",
}

// phaseLabel is the word the interface gives a call's state.
export function phaseLabel(phase: RunPhase): string {
  return PHASE_LABEL[phase]
}

import type { ConversationState, Interrupt, TurnStatus } from "@/lib/types"

// The session's channel carries three facts, and all of them outlive a run.
//
// ★ An approval belongs to the **session**: the run that raised it is over
//   by the time anybody can answer it. ★ A round belongs to the session too:
//   the surface that did not ask for it has no stream of its own, so the
//   session is what tells it that a round began.
//
// ★ The wire uses snake_case — the server owns the shape, and this module
//   translates it once, here. ★ The scan answers the same card in the REST
//   spelling (pendingApprovals), so the two surfaces say one thing in two
//   words — and this module is where the channel's words become the card.

// ApprovalPending says an approval is waiting for an answer.
//
// ★ It carries the **card's data**, not just the fact that there is one: a
//   surface that was not watching the run that raised the approval has no
//   stream of its own, so the tool name and the arguments exist nowhere else
//   for it — and a card without them is a card nobody can judge.
export interface ApprovalPending {
  kind: "approval-pending"
  // interrupt is the card itself.
  interrupt: Interrupt
  // runId names the run that raised the approval.
  runId: string
}

// ApprovalResolved says the approval was answered.
export interface ApprovalResolved {
  kind: "approval-resolved"
  // interruptId names the approval that was answered.
  interruptId: string
  // approved is the decision.
  approved: boolean
  // reason is what the user said, if anything.
  reason: string
  // runId names the run that consumed the approval.
  runId: string
}

// RunStarted says a new run owns the session.
export interface RunStarted {
  kind: "run-started"
  runId: string
  // turnKind is the kind's name, not a number: the frame outlives the
  // process that wrote it.
  turnKind: string
}

export type SessionFact = ApprovalPending | ApprovalResolved | RunStarted

// readInterrupt turns the channel's words into the card.
//
// ★ The card is the same card the scan answers: the channel spells it in
//   snake_case, the scan in camelCase, and the fields are the same fields.
// ★ The sub-agent's run id wins over the agent that asked — the same rule
//   the scan's mapping uses, so a card looks the same whichever surface
//   built it.
function readInterrupt(value: Record<string, unknown>): Interrupt {
  const subagentRunId = typeof value.subagent_run_id === "string" ? value.subagent_run_id : ""
  const requestedBy = typeof value.requested_by_agent === "string" ? value.requested_by_agent : ""
  return {
    id: typeof value.interrupt_id === "string" ? value.interrupt_id : "",
    reason: typeof value.reason === "string" ? value.reason : "tool_call",
    message: typeof value.message === "string" ? value.message : undefined,
    toolCallId: typeof value.tool_call_id === "string" ? value.tool_call_id : undefined,
    toolName: typeof value.tool_name === "string" ? value.tool_name : undefined,
    arguments: value.arguments,
    requestedByAgent: subagentRunId || requestedBy || undefined,
  }
}

// parseSessionFact reads one frame off the session's channel.
//
// ★ A frame it does not know is not an error: the channel is a live
//   notification path, and the server may add facts to it. ★ What must
//   not happen is a frame that is **half** understood — an approval
//   without an id names no card, so it is dropped.
export function parseSessionFact(raw: unknown): SessionFact | null {
  if (!raw || typeof raw !== "object") return null
  const ev = raw as { type?: unknown; name?: unknown; value?: unknown }
  if (ev.type !== "CUSTOM") return null
  const value = (ev.value ?? {}) as Record<string, unknown>

  if (ev.name === "approval_pending") {
    const interruptId = typeof value.interrupt_id === "string" ? value.interrupt_id : ""
    if (!interruptId) return null
    return {
      kind: "approval-pending",
      interrupt: readInterrupt(value),
      runId: typeof value.run_id === "string" ? value.run_id : "",
    }
  }

  if (ev.name === "approval_resolved") {
    const interruptId = typeof value.interrupt_id === "string" ? value.interrupt_id : ""
    if (!interruptId) return null
    return {
      kind: "approval-resolved",
      interruptId,
      approved: value.approved === true,
      reason: typeof value.reason === "string" ? value.reason : "",
      runId: typeof value.run_id === "string" ? value.run_id : "",
    }
  }

  if (ev.name === "run_started") {
    const runId = typeof value.run_id === "string" ? value.run_id : ""
    if (!runId) return null
    return {
      kind: "run-started",
      runId,
      turnKind: typeof value.kind === "string" ? value.kind : "query",
    }
  }

  return null
}

// applySessionFact folds one fact into the conversation.
//
// ★ The card is the point. A surface that waits on an approval and is not
//   told that the approval was answered keeps a card the server will
//   refuse every answer on — so the fact drops it.
export function applySessionFact(convo: ConversationState, fact: SessionFact): ConversationState {
  switch (fact.kind) {
    case "approval-pending": {
      // ★ The card may already be here: the surface that asked for the
      //   round saw the interrupt on the run's own stream. ★ The fact is
      //   the same card, so it merges instead of doubling it.
      const known = convo.interrupts.some((i) => i.id === fact.interrupt.id)
      const interrupts = known ? convo.interrupts : [...convo.interrupts, fact.interrupt]
      // ★ The status follows the card: the session is waiting for an
      //   answer, and that is what the card says.
      return { ...convo, interrupts, status: "awaiting-approval" }
    }

    case "approval-resolved": {
      const interrupts = convo.interrupts.filter((i) => i.id !== fact.interruptId)
      // ★ An approval this surface never held is not this surface's news.
      if (interrupts.length === convo.interrupts.length) return convo
      // ★ The status follows the card: a session that still waits for
      //   another approval keeps waiting, and one that does not is free.
      const status: TurnStatus = interrupts.length > 0 ? "awaiting-approval" : "idle"
      return { ...convo, interrupts, status }
    }

    case "run-started":
      // ★ A new round owns the session. ★ The old approval is void — the
      //   round that asked it is over — and the surface follows the new
      //   one from here.
      //
      // ★ The run's id is **not** written down. The id says "this surface
      //   is watching that run", and the surface is not watching it yet:
      //   it is about to attach. ★ Writing it down would make the attach
      //   believe it had nothing to do.
      return { ...convo, status: "running", interrupts: [], busy: false }
  }
}

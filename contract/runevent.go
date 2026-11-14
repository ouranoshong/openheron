package contract

import (
	"time"
)

// RunEventKind is what happened in a run.
//
// ★ It is openheron's own vocabulary, not AG-UI's. The run must not depend on
//
//	the wire format: the transport maps a RunEvent onto AG-UI frames, and a
//	second wire format would be a second renderer, not a second run.
type RunEventKind uint8

const (
	// RunEventTextStart opens a text message.
	RunEventTextStart RunEventKind = iota
	// RunEventTextDelta carries a piece of the text.
	RunEventTextDelta
	// RunEventTextEnd closes a text message.
	RunEventTextEnd
	// RunEventReasoningStart opens the reasoning trace.
	RunEventReasoningStart
	// RunEventReasoningDelta carries a piece of the reasoning.
	RunEventReasoningDelta
	// RunEventReasoningEnd closes the reasoning trace.
	RunEventReasoningEnd
	// RunEventToolStart opens a tool call.
	RunEventToolStart
	// RunEventToolArgs carries a piece of the tool's arguments.
	RunEventToolArgs
	// RunEventToolEnd closes a tool call.
	RunEventToolEnd
	// RunEventToolResult carries the tool's output.
	RunEventToolResult
	// RunEventAnnounce publishes a message that produced no visible delta.
	RunEventAnnounce
	// RunEventCompaction reports that the context was compacted.
	RunEventCompaction
	// RunEventContextUsage reports one agent's context occupancy.
	RunEventContextUsage
	// RunEventInterrupt reports the approvals the run is waiting for.
	RunEventInterrupt
	// RunEventApprovalResolved says the approval was answered.
	//
	// ★ It is a **session** fact, not a run's: the approval belongs to the
	//   session, and the run that answered it is a *different* run. A
	//   surface that is not watching that run still has to learn that the
	//   card it holds is no longer answerable — otherwise it sits on a
	//   question that was already answered, and every answer it offers is
	//   refused.
	RunEventApprovalResolved
	// RunEventRunStarted says a new run owns the session.
	//
	// ★ It is what lets a surface that missed the start follow the round:
	//   the run's own stream belongs to the surface that asked, and a
	//   surface that was not there when the round began has no stream at
	//   all.
	RunEventRunStarted
	// RunEventRetry reports that the provider is retrying.
	RunEventRetry
	// RunEventError reports that the run failed.
	RunEventError
)

// RunEvent is one thing that happened in a run.
//
// ★ It is a fact, not a frame. The renderer turns it into whatever the wire
//
//	format wants; two wire formats would be two renderers over the same
//	facts.
type RunEvent struct {
	// Kind is what happened.
	Kind RunEventKind
	// RunID is the run the event belongs to.
	RunID string
	// MsgID identifies the message the event is about.
	MsgID string
	// Text carries the delta, the error text or the tool's output.
	Text string
	// Role is the message's role.
	Role string
	// Owner is the agent the message belongs to.
	Owner string
	// ToolID identifies the tool call.
	ToolID string
	// ToolName is the tool's name.
	ToolName string
	// Sub is the sub-agent's run id, when the event is a sub-agent's.
	Sub string
	// Agent is the agent the event is about.
	Agent string
	// At is when the event happened.
	At time.Time
	// Usage is set on RunEventContextUsage.
	Usage ContextUsage
	// Interrupts is set on RunEventInterrupt.
	Interrupts []Interrupt
	// Pending is set on RunEventInterrupt: the same interrupts in the shape
	// the session persists.
	Pending []PendingInterrupt
	// Code is the failure's code, on RunEventError.
	Code string
	// InterruptID names the approval the event is about, on
	// RunEventApprovalResolved.
	InterruptID string
	// Approved carries the decision, on RunEventApprovalResolved.
	Approved bool
	// Reason carries the user's reason, on RunEventApprovalResolved.
	Reason string
	// TurnKind says what kind of turn a RunEventRunStarted announces.
	TurnKind TurnKind
}

// Interrupt is one approval request, in openheron's own shape.
//
// ★ The AG-UI shape lives in the transport. The run must not depend on the
//
//	wire format, and an approval is a fact long before it is a frame.
type Interrupt struct {
	// ID is the framework's interrupt context id.
	ID string
	// Reason says what kind of interruption this is.
	Reason string
	// Message is the human-readable line.
	Message string
	// Metadata carries the tool's name and arguments.
	Metadata map[string]any
	// SubagentRunID is the sub-agent that asked, if any.
	SubagentRunID string
}

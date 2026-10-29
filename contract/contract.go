package contract

import (
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// TurnKind distinguishes a fresh user query from an approval resumption.
type TurnKind uint8

const (
	// TurnQuery is a fresh user query.
	TurnQuery TurnKind = iota
	// TurnResume continues an interrupted turn after the user answered.
	TurnResume
)

func (k TurnKind) String() string {
	switch k {
	case TurnQuery:
		return "query"
	case TurnResume:
		return "resume"
	default:
		return "unknown"
	}
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// IsZero reports whether nothing was reported.
func (u Usage) IsZero() bool { return u.TotalTokens == 0 && u.PromptTokens == 0 }

// ContextUsage is one agent's context occupancy.
//
// "Used" is the size of the agent's most recent model call — the same
// baseline eino's summarization middleware counts from, so the number the UI
// shows and the number that decides "compact now" never disagree.
type ContextUsage struct {
	Agent         string `json:"agent"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	UsedTokens    int    `json:"used_tokens"`
	ContextWindow int    `json:"context_window"`
	// SummarizeAt is the token count at which this agent's history would be
	// compacted. Zero when no threshold is configured.
	SummarizeAt int `json:"summarize_at,omitempty"`
	// Summarized counts the compactions this agent has been through.
	Summarized int       `json:"summarized,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AgentContext is what one agent's context allows.
type AgentContext struct {
	// Window is the model's context window. Zero means "not configured".
	Window int
	// SummarizeAt is the token count that triggers compaction. Zero means
	// "never".
	SummarizeAt int
	// Provider and Model name the model the agent runs on.
	Provider string
	Model    string
}

// PendingInterrupt is one outstanding decision.
type PendingInterrupt struct {
	// InterruptID is the framework interrupt context ID. It is an address-like
	// string, not a UUID.
	InterruptID string `json:"interrupt_id"`
	// Detail is everything the UI needs to render the approval. It is
	// captured from the interrupt event before the stream closes — after
	// that the tool name and arguments exist nowhere else, so
	// GET /v2/sessions/{id} could not answer without it.
	Detail *InterruptDetail `json:"detail,omitempty"`
}

type InterruptDetail struct {
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Message          string          `json:"message,omitempty"`
	RequestedByAgent string          `json:"requested_by_agent,omitempty"`
	SubagentRunID    string          `json:"subagent_run_id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
}

type TurnView interface {
	SessionID() string
	Events() *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]
}

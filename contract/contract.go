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

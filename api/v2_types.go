package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// rfc3339Nano is the timestamp shape every v2 field uses.
const rfc3339Nano = time.RFC3339Nano

// ── error codes ──────────────────────────────────────────────────────────
// v1 replies with a bare {"error": string}. v2 keeps that key and adds a
// machine-readable code so a client can branch without parsing prose — the
// difference matters most for the model errors, where "rate limited, lower
// concurrency" and "quota exhausted, lowering concurrency does NOT help" are
// opposite advice and must not share a branch.
const (
	codeInvalidBody        = "invalid_body"
	codeEmptyMessage       = "empty_message"
	codeCommandNotRunnable = "command_not_runnable"
	codeFileRequired       = "file_required"
	codeFileTooLarge       = "file_too_large"
	codeInvalidFileName    = "invalid_file_name"
	codeThreadNotFound     = "thread_not_found"
	codeInterruptMissing   = "no_pending_interrupt"
	codeNoCheckpoint       = "no_checkpoint"
	codeInterruptMismatch  = "interrupt_mismatch"
	codeAgentNotFound      = "agent_not_found"
	codeAgentBusy          = "agent_busy"
	codeTurnSuperseded     = "turn_superseded"
	codeTurnStartTimeout   = "turn_start_timeout"
	codeInternal           = "internal"
)

// errBody is the v2 error envelope.
type errBody struct {
	// Error stays a plain message so a v1-era client still shows something.
	Error string `json:"error"`
	// Code is stable and machine-readable; switch on this, not on Error.
	Code string `json:"code"`
	// Kind mirrors llm/common.Kind for model errors.
	Kind string `json:"kind,omitempty"`
	// Hint is the actionable half. For model errors llm/common already folds
	// it into Error() as "what to do: …", so this carries it separately for
	// clients that want to style it apart.
	Hint string `json:"hint,omitempty"`
	// Retryable is false unless the endpoint's own classification says a retry
	// can help — a quota wall is not retryable, a 429 is.
	Retryable bool `json:"retryable"`

	cause error
}

// v2Err writes a structured error and logs it.
func v2Err(c *app.RequestContext, status int, body errBody) {
	if status >= 500 {
		detail := body.Error
		if body.cause != nil {
			detail = fmt.Sprintf("%s: %v", body.Error, body.cause)
		}
		obs.Logger().Error("[api] request failed",
			"method", c.Request.Method(), "uri", c.Request.URI().String(),
			"status", status, "code", body.Code, "detail", detail)
	}
	c.JSON(status, body)
}

func v2Fail(c *app.RequestContext, status int, code, msg string, retryable bool) {
	v2Err(c, status, errBody{Error: msg, Code: code, Retryable: retryable})
}

func v2FailInternal(c *app.RequestContext, err error) {
	v2Err(c, consts.StatusInternalServerError, errBody{
		Error: "internal error",
		Code:  codeInternal,
		cause: err,
	})
}

// v2FailErr maps a agentkit/run sentinel onto the v2 envelope.
func v2FailErr(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, errEmptyMessage):
		v2Fail(c, consts.StatusBadRequest, codeEmptyMessage, err.Error(), false)
	case errors.Is(err, errCommandNotRunnable):
		v2Fail(c, consts.StatusBadRequest, codeCommandNotRunnable, err.Error(), false)
	case errors.Is(err, errInterruptMismatch):
		v2Fail(c, consts.StatusConflict, codeInterruptMismatch, err.Error(), false)
	case errors.Is(err, errAgentNotFound):
		v2Fail(c, consts.StatusBadRequest, codeAgentNotFound, err.Error(), false)
	case errors.Is(err, errAgentBusy):
		v2Fail(c, consts.StatusConflict, codeAgentBusy, err.Error(), true)
	case errors.Is(err, storage.ErrSessionNotFound):
		v2Fail(c, consts.StatusNotFound, codeThreadNotFound, "session not found", false)
	case errors.Is(err, run.ErrNoPendingInterrupt):
		v2Fail(c, consts.StatusConflict, codeInterruptMissing, "no pending approval", false)
	case errors.Is(err, run.ErrNoCheckpoint):
		v2Fail(c, consts.StatusConflict, codeNoCheckpoint,
			"this approval can no longer be resumed: its checkpoint is gone. Send a new message to continue.",
			false)
	case errors.Is(err, run.ErrStartTimeout):
		v2Fail(c, consts.StatusConflict, codeTurnStartTimeout, err.Error(), true)
	case errors.Is(err, run.ErrSuperseded):
		v2Fail(c, consts.StatusConflict, codeTurnSuperseded, err.Error(), true)
	default:
		v2FailInternal(c, err)
	}
}

// ── DTOs ─────────────────────────────────────────────────────────────────

// threadState is what the sidebar dot renders.
type threadState string

const (
	stateIdle    threadState = "idle"
	stateRunning threadState = "running"
	stateWaiting threadState = "waiting_approval"
)

type threadMeta struct {
	ThreadID     string      `json:"threadId"`
	Title        string      `json:"title"`
	CreatedAt    string      `json:"createdAt"`
	UpdatedAt    string      `json:"updatedAt"`
	MessageCount int         `json:"messageCount"`
	State        threadState `json:"state"`
	// AgentID is the session's agent. Always emitted: the client's
	// "not switched yet" case is the string "main", not a missing field.
	AgentID string `json:"agentId"`
	// HasPendingApproval lets the sidebar draw the waiting dot without a
	// second request per row.
	HasPendingApproval bool `json:"hasPendingApproval"`
}

type threadList struct {
	Items []threadMeta `json:"items"`
}

// pendingView is the approval card's data.
// v1 synthesised the approval chip from `cursor > len(history)`, a heuristic
// that only worked because A2UI slots are positional. Here the server states
// the pending approval outright and the client renders it.
type pendingView struct {
	InterruptID      string          `json:"interruptId"`
	Reason           string          `json:"reason"`
	Message          string          `json:"message,omitempty"`
	ToolCallID       string          `json:"toolCallId,omitempty"`
	ToolName         string          `json:"toolName,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	RequestedByAgent string          `json:"requestedByAgent,omitempty"`
	SubagentRunID    string          `json:"subagentRunId,omitempty"`
	CreatedAt        string          `json:"createdAt,omitempty"`
}

// lastErrorView mirrors lastError in the session projection.
type lastErrorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	At      string `json:"at"`
}

type threadDetail struct {
	threadMeta
	RunID            string         `json:"runId,omitempty"`
	StartedAt        string         `json:"startedAt,omitempty"`
	LastError        *lastErrorView `json:"lastError,omitempty"`
	PendingApprovals []pendingView  `json:"pendingApprovals,omitempty"`
	Decisions        []decisionView `json:"decisions,omitempty"`
}

type decisionView struct {
	InterruptID      string `json:"interruptId"`
	ToolName         string `json:"toolName,omitempty"`
	Approved         bool   `json:"approved"`
	Reason           string `json:"reason,omitempty"`
	DecidedAt        string `json:"decidedAt"`
	RequestedByAgent string `json:"requestedByAgent,omitempty"`
}

type usageView struct {
	PromptTokens     int                   `json:"promptTokens"`
	CompletionTokens int                   `json:"completionTokens"`
	TotalTokens      int                   `json:"totalTokens"`
	ReasoningTokens  int                   `json:"reasoningTokens"`
	CachedTokens     int                   `json:"cachedTokens"`
	ByAgent          map[string]*usageView `json:"byAgent,omitempty"`
}

type messagesView struct {
	ThreadID         string          `json:"threadId"`
	State            threadState     `json:"state"`
	Messages         []types.Message `json:"messages"`
	PendingApprovals []pendingView   `json:"pendingApprovals,omitempty"`
	Usage            *usageView      `json:"usage,omitempty"`
	Agent            string          `json:"agent,omitempty"`
}

// toPendingViews converts the persisted approval set, or nil when there is none.
func toPendingViews(p *storage.PendingApproval) []pendingView {
	if p == nil || len(p.Interrupts) == 0 {
		return nil
	}
	out := make([]pendingView, 0, len(p.Interrupts))
	for _, it := range p.Interrupts {
		out = append(out, toPendingView(it))
	}
	return out
}

// toPendingView converts one outstanding decision.
func toPendingView(it contract.PendingInterrupt) pendingView {
	out := pendingView{InterruptID: it.InterruptID, Reason: "tool_call"}
	if it.Detail == nil {
		return out
	}
	d := it.Detail
	out.Message = d.Message
	out.ToolCallID = d.ToolCallID
	out.ToolName = d.ToolName
	out.Arguments = d.Arguments
	out.RequestedByAgent = d.RequestedByAgent
	out.SubagentRunID = d.SubagentRunID
	if !d.CreatedAt.IsZero() {
		out.CreatedAt = d.CreatedAt.UTC().Format(rfc3339Nano)
	}
	return out
}

// toUsageView returns nil when nothing was reported, so the client can tell
// "no data" from "zero tokens".
func toUsageView(u contract.Usage) *usageView {
	if u.IsZero() {
		return nil
	}
	return &usageView{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		ReasoningTokens:  u.ReasoningTokens,
		CachedTokens:     u.CachedTokens,
	}
}

// toUsageViewWithAgents adds the per-agent breakdown to the total.
func toUsageViewWithAgents(u contract.Usage, byAgent map[string]contract.Usage) *usageView {
	out := toUsageView(u)
	if out == nil {
		return nil
	}
	if len(byAgent) == 0 {
		return out
	}
	out.ByAgent = make(map[string]*usageView, len(byAgent))
	for agent, au := range byAgent {
		if v := toUsageView(au); v != nil {
			out.ByAgent[agent] = v
		}
	}
	if len(out.ByAgent) == 0 {
		out.ByAgent = nil
	}
	return out
}

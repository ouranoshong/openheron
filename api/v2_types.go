package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/obs"
	"openheron/agentkit/run"
	"openheron/contract"
	"openheron/storage"
)

// rfc3339Nano is the timestamp shape every v2 field uses.
const rfc3339Nano = time.RFC3339Nano

// ── error codes ──────────────────────────────────────────────────────────
//
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

	// cause 是 5xx 的真实原因，**只进日志、不进响应体**。
	//
	// ★ 它必须是非导出字段：encoding/json 只序列化导出字段，
	//
	//	于是「日志里有、客户端看不到」由类型系统保证，而不是靠记得别赋值。
	//	靠约定的话，某天有人写 `Cause: err` 就把内部路径泄给了客户端。
	cause error
}

// v2Err writes a structured error and logs it.
func v2Err(c *app.RequestContext, status int, body errBody) {
	if status >= 500 {
		// ★ 5xx 的日志必须带上 cause，否则记下来的是一句同义反复：
		//	「500 internal: internal error」。排查时无从下手 ——
		//	不知道是会话文件坏了、目录没权限，还是配置读不了。
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

// v2FailInternal 是 v2 所有 5xx 的出口。
//
// ★ 客户端只拿到 `"internal error"`，**真实原因只进日志**。
//
//	原先 11 个 5xx 出口各写一句描述（"create session failed"、"rename failed"…）
//	并把 error 丢掉。那句描述在日志里也是多余的：路由已经在 URI 里，
//	而 5xx 唯一有用的信息是「为什么」—— 那个恰恰被扔了。
//
// ★ 客户端文案为什么不给细节：500 的处置是「看服务端日志 + 报 issue」，
//
//	而细节里常带绝对路径与文件名（openheron 的会话就在用户目录下）。
//	回给客户端对用户没有帮助，只会在转贴 issue 时泄露本地目录结构。
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
		// ★ 409 而不是 400：状态没了，**重发同样的请求也不会好**。
		//
		//	消息是**给用户看的**（前端把它显示在对话里）：那张卡还挂在
		//	界面上（待批记录留着，巡检不再清它），用户点下去只会看到这句 ——
		//	所以要告诉他出路是「重新提问」，而不是抛一句内部术语。
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
//
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
	RunID     string         `json:"runId,omitempty"`
	StartedAt string         `json:"startedAt,omitempty"`
	LastError *lastErrorView `json:"lastError,omitempty"`
	// PendingApprovals is every outstanding decision. ★ 它是**列表**：
	//   一轮里可以有多个独立待批（模型一次请求四个都要审批的工具）。
	PendingApprovals []pendingView `json:"pendingApprovals,omitempty"`
	// Decisions 是**已答复**的审批记录（审计），最旧的在前。
	//
	// ★ 待批与已批是两份东西：待批会被取走，答复后就没了；
	//	审计留着，所以「上周批过什么」答得上来。
	Decisions []decisionView `json:"decisions,omitempty"`
}

// decisionView 是一条审批决定的对外形状。
//
// ★ 字段名用驼峰（与本包其它对外字段一致），而磁盘上用下划线 ——
//
//	那是对内（state.json）的形状，对外要换。
type decisionView struct {
	InterruptID string `json:"interruptId"`
	ToolName    string `json:"toolName,omitempty"`
	Approved    bool   `json:"approved"`
	Reason      string `json:"reason,omitempty"`
	DecidedAt   string `json:"decidedAt"`
	// RequestedByAgent 是发起这次调用的 agent。
	RequestedByAgent string `json:"requestedByAgent,omitempty"`
}

type usageView struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
	ReasoningTokens  int `json:"reasoningTokens"`
	CachedTokens     int `json:"cachedTokens"`
	// ByAgent 是**整个会话**的用量分账：哪个 agent 花的。
	//
	// ★ 它**不随消息过滤而变** —— 用量是会话级的事实，而过滤只影响
	//   「看哪些消息」。若过滤时把 byAgent 也收窄，两个数就对不上了：
	//   usage 是被过滤后的子集，byAgent 是全体。
	//
	// ★ 分母相同：byAgent 各桶之和 == usage 的总量。少一个桶都不对 ——
	//   消息上的归属标记是**写**的时候打的（transport），读的时候
	//   只做分组，不再推断，所以两个数必然同源。
	ByAgent map[string]*usageView `json:"byAgent,omitempty"`
}

type messagesView struct {
	ThreadID string          `json:"threadId"`
	State    threadState     `json:"state"`
	Messages []types.Message `json:"messages"`
	// PendingApprovals is every outstanding decision — 见 threadDetail 的同名字段。
	PendingApprovals []pendingView `json:"pendingApprovals,omitempty"`
	Usage            *usageView    `json:"usage,omitempty"`
	// Agent 是本次视图的过滤条件（?agent=xxx）。空 = 全部消息。
	//
	// ★ 回显它，是为了让客户端知道**自己拿到的不是全部** —— 否则一个
	//   只看到 3 条消息的客户端会以为会话里就只有 3 条。
	Agent string `json:"agent,omitempty"`
}

// toPendingViews converts the persisted approval set, or nil when there is none.
//
// ★ 返回**列表**：一轮里可以有多个独立待批，而每个都要有自己的 id ——
//
//	只有其中一个的话，客户端拿到几张卡就答不了几张（详见
//	storage.PendingApproval 的注释）。
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
//
// ★ 分账挂在**总量**下面，而不是取代它：总量是「这个会话花了多少」，
//
//	分账是「谁花的」。两个数必须同时在场 —— 只给分账，客户端要自己求和；
//	只给总量，「哪个 agent 贵」这个问题就没答案。
//
// ★ 分账为空时退化成纯总量视图：老客户端不认识 byAgent，但多一个
//
//	空字段不会让它崩，而「没有分账」与「分账为零」必须能区分开。
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

// Package contract 放**跨层共享的类型**。
//
// ★ 它存在的理由只有一个：**两个层都要用同一个类型，而它们不能互相 import**。
//
//	编排层（agentkit/run）与装配层（agent）必须互相不认识 —— 依赖铁律
//	（docs/architecture.md §3.1）就是靠这条成立的：编排层不 import 装配层，
//	装配层也不 import 编排层。
//
//	但有些类型**必须**是同一个：各写一份，编译期看不出来，只有运行期才炸，
//	而炸的方式往往「放行」而不是「报错」（见 approval 包的教训）。
//
// ★ 所以这些类型住在**叶子包**里：零依赖，谁都能引。
//
//   - [TurnKind]：这一轮是新提问还是审批恢复。编排层要分，装配层也要分
//     （恢复轮不能重新建 agent，要从 checkpoint 恢复）。
//
// ★ 它**不含行为**：这里放的是数据与它的自述，不是规则。
package contract

import (
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// TurnKind distinguishes a fresh user query from an approval resumption.
//
// ★ 它住在叶子包里而不是编排层：装配层（agent.Builder）也必须能说这个词，
//
//	而它不能 import 编排层（依赖铁律）。各写一份的后果不是编译错误，
//	是运行期才炸 —— 且炸的方式是「放行」。
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

// Usage 是一轮的 token 记账。
//
// ★ 归这个叶子包：渲染层（transport）聚合它、编排层（agentkit/run）带着它、
//
//	接口层（api）报它 —— 三处都要用，而渲染层不该 import 编排层。
//
// ★ 端点没报数就全是零：openheron 不编数字。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// ReasoningTokens 是 completion 里按思考计费的那部分。
	ReasoningTokens int `json:"reasoning_tokens"`
	// CachedTokens 是 prompt 里命中缓存的那部分。
	CachedTokens int `json:"cached_tokens"`
}

// IsZero reports whether nothing was reported.
func (u Usage) IsZero() bool { return u.TotalTokens == 0 && u.PromptTokens == 0 }

// PendingInterrupt is one outstanding decision.
//
// ★ 为什么住在这个叶子包：渲染层（transport）产出它、编排层（agentkit/run）
// 存它、接口层（api）报它 —— 而渲染层不该 import 编排层。
type PendingInterrupt struct {
	// InterruptID is the framework interrupt context ID. It is an address-like
	// string, not a UUID.
	//
	// ★ json tag 是**磁盘契约**：state.json 里的待批项就叫这两个键。
	//
	//	它以前是 storage/filestore 自己的一个结构体（同样两个字段），
	//	后来那个结构体被删了 —— 同一份形状不该有两个类型。
	InterruptID string `json:"interrupt_id"`
	// Detail is everything the UI needs to render the approval. It is
	// captured from the interrupt event before the stream closes — after
	// that the tool name and arguments exist nowhere else, so
	// GET /v2/sessions/{id} could not answer without it.
	Detail *InterruptDetail `json:"detail,omitempty"`
}

// InterruptDetail 是审批中断的**细节**。
//
// ★ 同上：它跨三层共用，所以住在叶子包里。
//
// ★ 归这个叶子包的理由与 [Usage] 一样：渲染层产出它、编排层存它、
//
//	接口层报它 —— 而渲染层不该 import 编排层。
type InterruptDetail struct {
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Message          string          `json:"message,omitempty"`
	RequestedByAgent string          `json:"requested_by_agent,omitempty"`
	SubagentRunID    string          `json:"subagent_run_id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
}

// TurnView 是编排层交给渲染层的**只读视图**。
//
// ★ 渲染层不该认识编排层的活对象（*agentkit/run.Turn）：那会让「渲染」依赖
//
//	「编排」，而渲染其实只需要两样东西 —— 这一轮是哪个会话，
//	以及事件从哪来。给一个接口，编排层就可以自由改自己的 Turn。
type TurnView interface {
	// SessionID 是这一轮所属的会话。
	SessionID() string
	// Events 是这一轮的事件迭代器。
	Events() *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]
}

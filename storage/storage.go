// Package storage 是**会话存储的契约**（叶子包）。
//
// ★ 为什么契约住在自己的包里，而不是跟着消费者（编排层）或实现（文件层）：
//
//	契约里出现任何一层的类型，那一层就得被另一层 import ——
//	于是「编排层」与「文件层」之间就有了依赖，而它们的职责本该正交。
//	契约归自己，谁都能引，谁都不欠谁。
//
// ★ 它只依赖标准库与 eino：它是**叶子**。
//
//   - 编排层（agentkit/run）用它声明「我要一个会话仓库」
//   - 实现层（storage/filestore）用它实现「我就是一个文件仓库」
//   - 接口层（api）用它读出待批与审计
package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/contract"
)

// ErrSessionNotFound signals an unknown session id.
var ErrSessionNotFound = errors.New("session not found")

// SessionManager is the session store contract.
//
// ★ Create and Get are deliberately separate: an unknown id always yields
//
//	ErrSessionNotFound —— 没有隐式创建。
type SessionManager interface {
	// Create mints a new session with a fresh UUID.
	//
	// ★ agentID 是这个会话的入口 agent，在**创建时**就定下来并落盘。
	//
	//   它是会话的属性，不是运行时的参数：之后所有决策（跑哪棵 agent
	//   树、输出算谁的、审批卡挂在谁名下）都读 state 里这一个值，不再
	//   问配置。
	//
	//   ★ 为什么写进创建而不是事后补：会话一旦存在，它就可能被列进侧栏、
	//     被别的请求读到 —— 那时 state 里没有根，读的人就得自己回退，
	//     于是「谁的根」有两份答案。创建时一起写，没有这个窗口。
	//
	//   空串表示「不记录」—— 只有在确实没有配置可依时才会这样。
	Create(ctx context.Context, agentID string) (Session, error)
	// Get returns an existing session, or ErrSessionNotFound.
	Get(ctx context.Context, id string) (Session, error)
	// List returns metadata for every known session.
	List(ctx context.Context) ([]Meta, error)
	// Delete removes the session and its derived artefacts.
	Delete(ctx context.Context, id string) error
	// Inspect reconciles session state against checkpoint state at startup.
	Inspect(ctx context.Context) (Report, error)
}

// Session is the aggregate root of one conversation.
//
// Messages, pending approval, cursor and workspace are all persisted state;
// memory is only a cache. Every read returns a snapshot the caller must not
// mutate in place.
type Session interface {
	ID() string
	CreatedAt() time.Time

	// Message log. Append-only.
	Append(msg *schema.Message) error
	Messages() []*schema.Message
	Len() int

	// SetPendingApproval is fail-closed: if the state cannot be persisted the
	// approval must not be admitted, so an error is returned and nothing is
	// held in memory either.
	SetPendingApproval(p *PendingApproval) error
	// PendingApproval peeks without consuming. The render path needs this:
	// calling TakePendingApproval there would swallow the approval.
	PendingApproval() (*PendingApproval, error)
	// TakePendingApproval atomically takes and clears the pending approval.
	TakePendingApproval() (*PendingApproval, error)
	ClearPendingApproval() error

	// RecordDecision appends approval decisions to the session's audit log.
	//
	// ★ **只追加**：审计的价值在于「那一次批了什么」，改写过的审计不算审计。
	//
	//	实现可以截断**旧的**记录（审计不是日志），但不能改写已有的那几条。
	RecordDecision(recs ...ApprovalRecord) error
	// Decisions returns the recorded approval decisions, oldest first.
	Decisions() []ApprovalRecord

	// Title is the cached display title. Empty means the cache was never
	// populated; callers fall back to deriving it from the transcript.
	Title() string
	// SetTitle overrides the cached title. Rejects an empty title so the
	// cache is never silently cleared.
	SetTitle(title string) error

	// AgentID is the session's agent. Empty means main — that is what
	// sessions written before the field existed read as.
	AgentID() string
	// SetAgentID persists a new root agent. It only affects later turns.
	//
	// ★ Rejecting "busy" sessions is the caller's job: the runtime phase is
	//   not visible here, and a session that is mid-turn must not be mutated
	//   underneath the loop.
	SetAgentID(id string) error

	Workspace() string
	PutFile(name string, r io.Reader) (string, error)

	// Init 把这个会话落地自己：建目录、建 workspace、写第一份 state.json
	// 与 transcript 头。
	//
	// ★ 它是 [SessionManager.Create] 用的：直接调它，**仓库不会缓存这个会话**
	//   —— 仓库的生命周期只有仓库管。
	Init(agentID string) error
	// Delete 删掉这个会话在盘上的全部东西：transcript、state、workspace、
	// checkpoint。
	//
	// ★ 直接调它**不做运行期清理** —— 那是 [SessionManager.Delete] 的事：
	//   它先 teardown，再删文件。
	Delete() error
	// Meta 是这个会话的元数据（侧栏要的那几项：标题、条数、时间）。
	//
	// ★ 标题的推导只由会话做（缓存里没有就从正文取一次）—— 别的层再写
	//   一遍就会有两套「这个会话叫什么」的答案。
	Meta() Meta
	// HasPending 报告这个会话还欠着人审批吗。
	HasPending() bool
	// HasCheckpoint 问这个会话有没有可恢复的 checkpoint。
	//
	// ★ 「有待批但没有 checkpoint」= 孤儿审批：那张卡还在界面上，但答复会被
	//   拒绝。回答「有没有可恢复的」只有这一处知道。
	HasCheckpoint(ctx context.Context) (bool, error)
	// GetCheckPointStore 返回这个会话的 checkpoint 存取器。
	//
	// ★ 它是**按 id 索引**的 —— 所有会话共用一个 store，所以它属于仓库，
	//
	//	不属于某一个会话；挂在 Session 上是因为「这个会话的 checkpoint 从哪
	//	取」是会话自己的事。
	//
	//	两个返回值都是 nil 表示「这个部署不做 checkpoint」—— 那时审批中断
	//	无法恢复（见 agentkit/run.hasCheckpoint）。
	GetCheckPointStore() (adk.CheckPointStore, adk.CheckPointDeleter)
}

// PendingApproval is the set of outstanding human decisions.
//
// ★ 它是**一组**而不是一个。
//
//	eino 可以在同一轮里抛出多个彼此独立的中断（模型一次请求四个都要
//	审批的工具，就是四个）。而 core.Interrupt 每次都生成**新的 uuid**
//	（eino internal/core/interrupt.go:105）—— 所以没被答复的那几个在
//	resume 之后会带着**新 id** 重新中断。
//
//	这直接否掉了「一次只答一个」：客户端手里的旧 id 在下一次 resume
//	之后全部作废，再答就必然对不上（对不上时服务端报
//	「interruptId does not match the pending approval」）。
//
//	★ 所以答复必须是**一次答完整组**。框架的 ResumeParams.Targets 本来
//
//	就是 map（eino adk/runner.go:133），AG-UI 的 resume 也本来就是数组 ——
//	两边都是为「一次答多个」设计的。
type PendingApproval struct {
	// Interrupts are every outstanding decision, in the order the framework
	// reported them. Never empty for a live pending approval.
	//
	// ★ 契约只说「一组待批」—— 磁盘上的**旧单数形态**（2026-10-21 之前
	// 写的 interrupt_id + detail）是磁盘格式的遗留，归实现层：实现层读
	// 出来时把它归一成一项的集合，写出去时只写集合。
	Interrupts []contract.PendingInterrupt
}

// Has reports whether id is one of the outstanding decisions.
func (p *PendingApproval) Has(id string) bool {
	if p == nil {
		return false
	}
	for _, it := range p.Interrupts {
		if it.InterruptID == id {
			return true
		}
	}
	return false
}

// ApprovalRecord 是**一条审批决定的审计记录**。
//
// ★ 为什么落盘而不只进日志：日志会滚掉，state.json 不会。
//
//	「上周那次 write_file 到底批没批」只有持久化的审计能回答 ——
//	而审批卡一旦被答完，待批记录就**取走了**（TakePendingApproval），
//	不留痕迹的话，那次决定就再也找不回来了。
//
// ★ **没有「谁」这一栏**：openheron 是单机、单操作者、无鉴权的运行时，
//
//	记一个 DecidedBy 只会给人虚假的安全感 —— 看上去有审计，其实那栏
//	恒为同一个值。真正该记的是「批了什么、什么时候、为什么」。
type ApprovalRecord struct {
	// InterruptID 是被答复的那个待批的 id（答复后会作废，但审计要留住它）。
	InterruptID string `json:"interrupt_id"`
	// ToolName 是被批准/拒绝的工具名。
	ToolName string `json:"tool_name,omitempty"`
	// Approved 报告人是否同意这次调用。
	Approved bool `json:"approved"`
	// Reason 是人给的理由（拒绝时常用）。
	Reason string `json:"reason,omitempty"`
	// DecidedAt 是决定做出的时刻（服务端盖的，客户端说了不算）。
	DecidedAt time.Time `json:"decided_at"`
	// RequestedByAgent 是发起这次调用的 agent（根或子）。
	RequestedByAgent string `json:"requested_by_agent,omitempty"`
}

// Meta summarises a session for listing.
//
// ★ JSON tag 必须是小写：`GET /sessions` 直接序列化本类型，而前端读的是
// `sess.id` / `sess.title`。没有 tag 时 Go 会输出 `ID` / `Title`，前端拿到
// undefined，`sess.id.slice(...)` 直接抛异常，侧栏整块渲染不出来。
type Meta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt advances on every append, so listings can sort by activity
	// instead of falling back to directory order.
	UpdatedAt time.Time `json:"updated_at"`
	// MessageCount is the number of persisted messages, so the sidebar can
	// show it without loading the transcript.
	MessageCount int `json:"message_count"`
}

// Report counts the outcomes of the startup consistency pass.
type Report struct {
	Clean          int // no pending, no checkpoint
	PendingOK      int // pending and checkpoint agree
	Orphan         int // pending without checkpoint — kept, so the card stays visible
	Ownerless      int // checkpoint without pending, unrecoverable
	SkippedBadJSON int // unreadable state, skipped
}

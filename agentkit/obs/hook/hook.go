// Package hook 把 eino 的回调事件变成日志。
//
// ★ 它补的是「收的那半」。eino 的回调通道此前只有发、没有收：
//
//	kb 的检索与索引、llm/common 的模型调用，都在主动调
//	callbacks.OnStart / OnEnd / OnError —— 而全仓没有任何一处注册接收方。
//	于是那 9 处调用纯属开销：构造了 RunInfo 与回调载荷，却没人消费。
//
//	装上本包之后，**那 9 处一行都不用改** —— 这正是旁路范式的前提：
//	发事件的业务代码不该知道谁在收。
//
// ★ 它与 [openheron/obs] 分开，是因为 obs 必须**不含 eino**：
//
//	obs 是日志入口，业务包都要用它（config 也要）—— 而 config 的依赖闭包里
//	eino 的包数必须是 0（docs/architecture.md §3.2.1）。把 eino 放在
//	obs/hook 里，那条铁律才成立。
package hook

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/obs"
)

// Handler 把 eino 的回调事件记成日志。
//
// ★ 它只做三件事：记开始时刻、算耗时、把组件身份与 runId 写进日志。
//
//	它**不**改变任何业务行为：所有方法都只读输入、只写日志，
//	返回的 ctx 最多多挂一个计时栈。
type Handler struct {
	log *slog.Logger
}

// New 建一个回调处理器。
//
// l 为 nil 时用进程级日志器（见 [obs.Logger]）。
func New(l *slog.Logger) *Handler {
	if l == nil {
		l = obs.Logger()
	}
	return &Handler{log: l}
}

// OnStart 记下这次调用的开始时刻。
//
// ★ 开始时刻挂在 ctx 上而不是 map 里：同一个组件可能并发跑多轮，
//
//	map 会互相覆盖。ctx 是**每条链自己的**，天然隔离。
func (h *Handler) OnStart(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
	s, ok := ctx.Value(spanKey{}).(*span)
	if !ok {
		s = &span{}
		ctx = context.WithValue(ctx, spanKey{}, s)
	}
	s.push(spanEntry{component: componentOf(info), name: nameOf(info), at: time.Now()})
	return ctx
}

// OnEnd 记一次成功调用。
func (h *Handler) OnEnd(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
	h.finish(ctx, info, "finished", nil)
	return ctx
}

// OnError 记一次失败调用。
func (h *Handler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	h.finish(ctx, info, "failed", err)
	return ctx
}

// OnStartWithStreamInput 不处理。
//
// ★ 关掉它不是为了省事：框架会为**每个** handler 拷贝一份输入流，
//
//	不关掉的 handler 拿不到它、却让拷贝照做 —— 白白的内存与 goroutine。
//	本包不关心流的输入。
func (h *Handler) OnStartWithStreamInput(ctx context.Context, _ *callbacks.RunInfo,
	_ *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	return ctx
}

// OnEndWithStreamOutput 记「流已就绪」。
//
// ★ 这个时刻是**首包延迟**：模型返回响应头即触发，不是整段生成结束。
//
//	整段生成的耗时有别的去处（transport 记思维链用时，
//	agentkit/run 记归档条数），这里不重复算。
//
// ★ 必须关掉自己这份拷贝 —— eino 给每个 handler 一份独立拷贝，
//
//	不关就会把底下的 goroutine 泄漏掉。**关掉而不读完是安全的**：
//	拷贝之间互不影响，下游拿的是另一份（eino 的 parentStreamReader
//	只在**所有**拷贝都关掉后才关原始流）。
func (h *Handler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	output.Close()
	h.finish(ctx, info, "stream ready", nil)
	return ctx
}

// Needed 决定框架要为哪些时刻调用本 handler。
//
// ★ 这不是优化，是**正确性要求**：框架按它决定要不要拷贝流。
//
//	关掉的时刻，框架连拷贝都不做 —— 于是本包不碰流，
//	也就不可能因为忘了关而泄漏。
func (h *Handler) Needed(_ context.Context, _ *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	switch timing {
	case callbacks.TimingOnStart, callbacks.TimingOnEnd,
		callbacks.TimingOnError, callbacks.TimingOnEndWithStreamOutput:
		return true
	default:
		return false
	}
}

// ── 计时栈 ───────────────────────────────────────────────────────────────

type spanKey struct{}

// span 是一次调用的计时栈。
//
// ★ 为什么是栈而不是 map：组件是**嵌套**的（graph → node → model），
//
//	外层组件的结束事件在里层结束**之后**才到。用 map 按组件名记，
//	外层就会读到里层的开始时刻，耗时算短了。栈的后进先出
//	与嵌套顺序一致。
type span struct {
	mu    sync.Mutex
	stack []spanEntry
}

type spanEntry struct {
	component string
	name      string
	at        time.Time
}

func (s *span) push(e spanEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// ★ 上限兜底：某个组件发了 Start 却没有 End/Error 时栈会涨，
	//   而它涨的代价是每轮多占内存。截断比泄漏便宜。
	if len(s.stack) >= 64 {
		return
	}
	s.stack = append(s.stack, e)
}

func (s *span) pop() (spanEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stack) == 0 {
		return spanEntry{}, false
	}
	e := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return e, true
}

func popSpan(ctx context.Context) (spanEntry, bool) {
	s, ok := ctx.Value(spanKey{}).(*span)
	if !ok {
		return spanEntry{}, false
	}
	return s.pop()
}

// ── 日志 ─────────────────────────────────────────────────────────────────

// finish 记一次调用的结果。
//
// ★ 弹栈必须**先于**任何过滤：栈是所有组件共用的，
//
//	少弹一次会让外层组件的耗时算成里层的。
func (h *Handler) finish(ctx context.Context, info *callbacks.RunInfo, what string, err error) {
	e, ok := popSpan(ctx)
	if !ok {
		// 没有配对的开始事件（框架在别处发的 Start，或 ctx 被换过）。
		// 照样记 —— 「知道是谁、有没有耗时」比「有没有耗时」重要。
		e = spanEntry{component: componentOf(info), name: nameOf(info)}
	}

	level := levelFor(e.component)
	if !h.log.Enabled(ctx, level) {
		return
	}

	attrs := []any{"component", e.component}
	if e.name != "" {
		attrs = append(attrs, "name", e.name)
	}
	if typ := typeOf(info); typ != "" {
		attrs = append(attrs, "type", typ)
	}
	if !e.at.IsZero() {
		attrs = append(attrs, "dur", time.Since(e.at).Round(time.Millisecond))
	}
	if err != nil {
		attrs = append(attrs, "err", err.Error())
	}
	h.log.Log(ctx, level, obs.Prefix(ctx)+"component "+what, attrs...)
}

// levelFor 决定该组件的日志级别。
//
// ★ 只把**贵**的组件抬到 info：模型调用、嵌入、索引、检索。
//
//	其余组件（图节点、工具、prompt）在 debug 级 ——
//	一轮下来它们有十几个，全放 info 会把真正要紧的几行淹掉。
func levelFor(component string) slog.Level {
	switch component {
	case "ChatModel", "Embedding", "Indexer", "Retriever":
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

func componentOf(info *callbacks.RunInfo) string {
	if info == nil || info.Component == "" {
		return "Unknown"
	}
	return string(info.Component)
}

func nameOf(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	return info.Name
}

func typeOf(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	return info.Type
}

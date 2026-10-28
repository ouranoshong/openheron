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

type Handler struct {
	log *slog.Logger
}

func New(l *slog.Logger) *Handler {
	if l == nil {
		l = obs.Logger()
	}
	return &Handler{log: l}
}

func (h *Handler) OnStart(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
	s, ok := ctx.Value(spanKey{}).(*span)
	if !ok {
		s = &span{}
		ctx = context.WithValue(ctx, spanKey{}, s)
	}
	s.push(spanEntry{component: componentOf(info), name: nameOf(info), at: time.Now()})
	return ctx
}

func (h *Handler) OnEnd(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
	h.finish(ctx, info, "finished", nil)
	return ctx
}

func (h *Handler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	h.finish(ctx, info, "failed", err)
	return ctx
}

func (h *Handler) OnStartWithStreamInput(ctx context.Context, _ *callbacks.RunInfo,
	_ *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	return ctx
}

func (h *Handler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	output.Close()
	h.finish(ctx, info, "stream ready", nil)
	return ctx
}

func (h *Handler) Needed(_ context.Context, _ *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	switch timing {
	case callbacks.TimingOnStart, callbacks.TimingOnEnd,
		callbacks.TimingOnError, callbacks.TimingOnEndWithStreamOutput:
		return true
	default:
		return false
	}
}

type spanKey struct{}

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

func (h *Handler) finish(ctx context.Context, info *callbacks.RunInfo, what string, err error) {
	e, ok := popSpan(ctx)
	if !ok {
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

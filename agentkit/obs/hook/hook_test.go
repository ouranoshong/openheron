package hook

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/obs"
)

// Handler 必须满足 eino 的回调接口 —— 少一个方法就装不上。
var _ callbacks.Handler = (*Handler)(nil)

func newTestHandler(buf *bytes.Buffer) *Handler {
	return New(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func TestOnEndLogsTheComponentAndItsDuration(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{
		Type: "OpenAICompat", Component: "ChatModel",
	}, nil)
	ctx = h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAICompat", Component: "ChatModel"}, nil)

	out := buf.String()
	if !strings.Contains(out, "component=ChatModel") {
		t.Fatalf("expected the component in the log line, got: %s", out)
	}
	if !strings.Contains(out, "dur=") {
		t.Fatalf("expected a duration in the log line, got: %s", out)
	}
	if !strings.Contains(out, "type=OpenAICompat") {
		t.Fatalf("expected the implementation type in the log line, got: %s", out)
	}
}

func TestOnErrorLogsTheFailure(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{Component: "Retriever"}, nil)
	ctx = h.OnError(ctx, &callbacks.RunInfo{Component: "Retriever"}, context.DeadlineExceeded)

	out := buf.String()
	if !strings.Contains(out, "component failed") {
		t.Fatalf("expected a failure line, got: %s", out)
	}
	if !strings.Contains(out, "err=") {
		t.Fatalf("expected the error in the log line, got: %s", out)
	}
}

// 嵌套组件：外层的结束事件在里层之后到达。
// 栈的后进先出保证外层拿到的是**自己**的开始时刻。
func TestNestedComponentsDoNotShareTheStartTime(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{Component: "Graph"}, nil)
	inner := h.OnStart(ctx, &callbacks.RunInfo{Component: "ChatModel"}, nil)
	inner = h.OnEnd(inner, &callbacks.RunInfo{Component: "ChatModel"}, nil)
	ctx = h.OnEnd(inner, &callbacks.RunInfo{Component: "Graph"}, nil)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two log lines, got %d: %s", len(lines), buf.String())
	}
	// 第一行是里层（ChatModel），第二行是外层（Graph）。
	if !strings.Contains(lines[0], "component=ChatModel") {
		t.Fatalf("expected the inner component first, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "component=Graph") {
		t.Fatalf("expected the outer component second, got: %s", lines[1])
	}
}

// Needed 决定框架要不要为某个时刻拷贝流。
// 关掉的时刻必须真的关掉 —— 否则会白拷贝一份流。
func TestNeededSkipsTheStreamInputTiming(t *testing.T) {
	h := New(nil)
	if h.Needed(context.Background(), nil, callbacks.TimingOnStartWithStreamInput) {
		t.Fatal("the stream input timing must be skipped: this handler never reads the stream")
	}
	for _, timing := range []callbacks.CallbackTiming{
		callbacks.TimingOnStart, callbacks.TimingOnEnd,
		callbacks.TimingOnError, callbacks.TimingOnEndWithStreamOutput,
	} {
		if !h.Needed(context.Background(), nil, timing) {
			t.Fatalf("timing %v must be kept", timing)
		}
	}
}

// 流的那份拷贝必须被关掉，否则底下的 goroutine 会泄漏。
func TestOnEndWithStreamOutputClosesItsCopy(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	sr, sw := schema.Pipe[callbacks.CallbackOutput](1)
	go func() {
		_ = sw.Send(nil, nil)
		sw.Close()
	}()

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{Component: "ChatModel"}, nil)
	ctx = h.OnEndWithStreamOutput(ctx, &callbacks.RunInfo{Component: "ChatModel"}, sr)
	_ = ctx

	// 关掉之后再收应当立刻拿到 EOF（不阻塞）。
	if _, err := sr.Recv(); err == nil {
		t.Fatal("expected the closed copy to stop delivering")
	}
}

// 日志行必须带上 runId 前缀 —— 这是「跨层排障有共同 id」的前提。
func TestLogLineCarriesTheRunID(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	ctx := obs.WithRunID(context.Background(), "run-7")
	ctx = h.OnStart(ctx, &callbacks.RunInfo{Component: "ChatModel"}, nil)
	ctx = h.OnEnd(ctx, &callbacks.RunInfo{Component: "ChatModel"}, nil)

	if !strings.Contains(buf.String(), "[run=run-7]") {
		t.Fatalf("expected the run id in the log line, got: %s", buf.String())
	}
}

// 不贵的组件不该在 info 级刷屏。
func TestCheapComponentsStayQuietAtInfo(t *testing.T) {
	var buf bytes.Buffer
	h := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{Component: "Tool"}, nil)
	ctx = h.OnEnd(ctx, &callbacks.RunInfo{Component: "Tool"}, nil)

	if buf.Len() != 0 {
		t.Fatalf("expected no output at info level, got: %s", buf.String())
	}
}

// 全局注册之后，eino 的回调事件真的会被收下 ——
//
// ★ 这是旁路范式的**核心断言**：业务代码只管发（kb / llm/common 已经在发），
//
//	接收方装上就一定有效果。装不上、或者装了收不到，
//	那 9 处 callbacks 调用就还是白付成本。
func TestGlobalHandlerReceivesTheEvents(t *testing.T) {
	var buf bytes.Buffer
	callbacks.AppendGlobalHandlers(newTestHandler(&buf))

	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Type: "OpenAICompat", Component: components.ComponentOfChatModel,
	})
	ctx = callbacks.OnStart[callbacks.CallbackInput](ctx, nil)
	ctx = callbacks.OnEnd[callbacks.CallbackOutput](ctx, nil)

	out := buf.String()
	if !strings.Contains(out, "component=ChatModel") {
		t.Fatalf("the global handler received nothing: %s", out)
	}
	if !strings.Contains(out, "dur=") {
		t.Fatalf("expected a duration in the log line: %s", out)
	}
}

// 关掉的时刻，框架连回调都不该调 —— 更不该为它拷贝流。
func TestSkippedTimingIsNeverDelivered(t *testing.T) {
	var buf bytes.Buffer
	callbacks.AppendGlobalHandlers(newTestHandler(&buf))

	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
	})
	// OnStartWithStreamInput 需要一个流；这里只关心「handler 没被叫到」。
	_, _ = callbacks.OnStartWithStreamInput[callbacks.CallbackInput](ctx, nil)

	// OnStartWithStreamInput 不在本包的 Needed 名单里 ⇒ 不该有日志。
	if strings.Contains(buf.String(), "component started") {
		t.Fatalf("a skipped timing produced output: %s", buf.String())
	}
}

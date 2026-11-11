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

	"github.com/ouranoshong/openheron/agentkit/obs"
)

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
	if !strings.Contains(lines[0], "component=ChatModel") {
		t.Fatalf("expected the inner component first, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "component=Graph") {
		t.Fatalf("expected the outer component second, got: %s", lines[1])
	}
}

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

func TestOnEndWithStreamOutputClosesItsCopy(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	sr, sw := schema.Pipe[callbacks.CallbackOutput](1)

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{Component: "ChatModel"}, nil)
	ctx = h.OnEndWithStreamOutput(ctx, &callbacks.RunInfo{Component: "ChatModel"}, sr)
	_ = ctx

	// ★ The write side is what reports the close, and asking it is what makes
	//   this deterministic. A writer goroutine racing the handler used to decide
	//   the outcome: when its send landed in the buffer before the handler
	//   closed the reader, the buffered value came back with a nil error and
	//   the assertion failed even though the handler had done its job. A send
	//   into a closed reader is refused, a send into an open one is buffered.
	if closed := sw.Send(nil, nil); !closed {
		t.Fatal("expected the handler to close the reader it was given")
	}
	sw.Close()

	if _, err := sr.Recv(); err == nil {
		t.Fatal("expected the closed copy to stop delivering")
	}
}

func TestOnEndWithStreamOutputLeavesTheComponentsStreamIntact(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	sr, sw := schema.Pipe[callbacks.CallbackOutput](2)
	go func() {
		_ = sw.Send("first", nil)
		_ = sw.Send("second", nil)
		sw.Close()
	}()

	// The handler is registered per invocation: the global list is process-wide
	// and grows for the rest of the run, and this test does not need it. The
	// copy machinery is the same either way — the framework hands every handler
	// a copy and keeps one for the component.
	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
	}, h)
	_, next := callbacks.OnEndWithStreamOutput[callbacks.CallbackOutput](ctx, sr)
	defer next.Close()

	// Closing the handler's copy must not cost the component its values.
	var got []string
	for {
		v, err := next.Recv()
		if err != nil {
			break
		}
		s, _ := v.(string)
		got = append(got, s)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("expected the component's copy to carry both values, got %v", got)
	}
}

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

func TestCheapComponentsStayQuietAtInfo(t *testing.T) {
	var buf bytes.Buffer
	h := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx := h.OnStart(context.Background(), &callbacks.RunInfo{Component: "Tool"}, nil)
	ctx = h.OnEnd(ctx, &callbacks.RunInfo{Component: "Tool"}, nil)

	if buf.Len() != 0 {
		t.Fatalf("expected no output at info level, got: %s", buf.String())
	}
}

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

func TestSkippedTimingIsNeverDelivered(t *testing.T) {
	var buf bytes.Buffer
	callbacks.AppendGlobalHandlers(newTestHandler(&buf))

	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
	})
	_, _ = callbacks.OnStartWithStreamInput[callbacks.CallbackInput](ctx, nil)

	if strings.Contains(buf.String(), "component started") {
		t.Fatalf("a skipped timing produced output: %s", buf.String())
	}
}

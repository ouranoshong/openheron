package helpers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// The safe middleware swallows the error so the turn survives it — and the
// failure is the one fact nobody else can see. ★ The layer above sees a
// string and the model sees the same string, so the failure has to be
// recorded where it is still an error.
func TestTheMiddlewareRecordsTheFailureItSwallowed(t *testing.T) {
	failures := NewToolFailures()
	ctx := WithToolFailures(context.Background(), failures)

	wrapped, err := NewSafeToolMiddleware().WrapInvokableToolCall(ctx,
		func(context.Context, string, ...tool.Option) (string, error) {
			return "", errors.New("file not found: /tmp/missing.txt")
		}, &adk.ToolContext{Name: "read_file", CallID: "call-1"})
	if err != nil {
		t.Fatalf("wrapping: %v", err)
	}

	out, err := wrapped(ctx, "{}")
	if err != nil {
		t.Fatalf("★ the error must still be swallowed — the turn survives it: %v", err)
	}
	if !strings.Contains(out, "[tool error]") {
		t.Fatalf("the model must read the failure: %q", out)
	}

	failure, ok := failures.Take("call-1")
	if !ok {
		t.Fatal("★ the failure was swallowed and not recorded — nobody would know the call failed")
	}
	if failure.ToolName != "read_file" {
		t.Fatalf("tool name = %q, want read_file", failure.ToolName)
	}
	if !strings.Contains(failure.Error, "file not found") {
		t.Fatalf("error = %q, want the tool's own words", failure.Error)
	}
}

// ★ A stream that fails **after** it started has no error to catch at the
// call — and the call is the thing the interface has to mark as failed.
func TestAStreamThatFailsMidwayIsRecorded(t *testing.T) {
	failures := NewToolFailures()
	ctx := WithToolFailures(context.Background(), failures)

	source, writer := schema.Pipe[string](4)
	_ = writer.Send("partial output", nil)
	_ = writer.Send("", errors.New("the shell died"))
	writer.Close()

	reader := SafeWrapReader(ctx, &adk.ToolContext{Name: "execute", CallID: "call-2"}, source)

	var text strings.Builder
	for {
		chunk, err := reader.Recv()
		if err != nil {
			break
		}
		text.WriteString(chunk)
	}
	if !strings.Contains(text.String(), "[tool error]") {
		t.Fatalf("the model must read the failure: %q", text.String())
	}

	failure, ok := failures.Take("call-2")
	if !ok {
		t.Fatal("★ a stream that failed mid-way was not recorded")
	}
	if !strings.Contains(failure.Error, "the shell died") {
		t.Fatalf("error = %q, want the stream's own words", failure.Error)
	}
}

// ★ An interrupt rerun is not a failure: the approval gate is waiting for an
// answer, and a call that is waiting is not a call that failed.
func TestAnInterruptRerunIsNotAFailure(t *testing.T) {
	failures := NewToolFailures()
	ctx := WithToolFailures(context.Background(), failures)

	wrapped, err := NewSafeToolMiddleware().WrapInvokableToolCall(ctx,
		func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
			return "", tool.StatefulInterrupt(ctx, "waiting for the approval", "{}")
		}, &adk.ToolContext{Name: "write_file", CallID: "call-3"})
	if err != nil {
		t.Fatalf("wrapping: %v", err)
	}
	if _, err := wrapped(ctx, "{}"); err == nil {
		t.Fatal("the interrupt must be passed through untouched")
	}
	if _, ok := failures.Take("call-3"); ok {
		t.Fatal("★ an interrupt rerun was recorded as a failure — the gate would read as broken")
	}
}

// A round that collects nothing must not blow up: the collector is optional.
func TestARoundWithoutACollectorIsFine(t *testing.T) {
	wrapped, err := NewSafeToolMiddleware().WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) {
			return "", errors.New("boom")
		}, &adk.ToolContext{Name: "read_file", CallID: "call-4"})
	if err != nil {
		t.Fatalf("wrapping: %v", err)
	}
	out, err := wrapped(context.Background(), "{}")
	if err != nil {
		t.Fatalf("the error must still be swallowed: %v", err)
	}
	if !strings.Contains(out, "[tool error]") {
		t.Fatalf("the model must read the failure: %q", out)
	}
}

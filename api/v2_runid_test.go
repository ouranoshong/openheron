package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/contract"
)

type runIDCapturingBuilder struct {
	seen string
}

// Contexts reports what each agent's context allows. The fake has no
// models, so there are no windows.
func (b *runIDCapturingBuilder) Contexts() map[string]contract.AgentContext { return nil }

func (b *runIDCapturingBuilder) Build(ctx context.Context, _, _ string, _ contract.TurnKind) (
	adk.TypedAgent[*schema.Message], error) {
	b.seen = obs.RunID(ctx)
	return noopAgent{}, nil
}

func TestV2RunUsesOneRunIDForTheLogAndTheStream(t *testing.T) {
	var logs bytes.Buffer
	obs.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { obs.SetLogger(obs.DefaultLogger(os.Stderr)) })

	builder := &runIDCapturingBuilder{}
	srv, captured := newV2RunServerWith(t, builder)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "hello",
		"runId":    "run-under-test",
	})
	if got.code != 200 {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}

	if !strings.Contains(captured.String(), `"runId":"run-under-test"`) {
		t.Fatalf("stream missing the run id: %s", captured.String())
	}

	if !strings.Contains(logs.String(), "[run=run-under-test]") {
		t.Fatalf("log missing the run prefix: %s", logs.String())
	}

	if builder.seen != "run-under-test" {
		t.Fatalf("the assembly layer saw run id %q, want run-under-test", builder.seen)
	}
}

func TestV2RunMintsARunIDWhenTheClientHasNone(t *testing.T) {
	var logs bytes.Buffer
	obs.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { obs.SetLogger(obs.DefaultLogger(os.Stderr)) })

	builder := &runIDCapturingBuilder{}
	srv, captured := newV2RunServerWith(t, builder)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "hello",
	})
	if got.code != 200 {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}

	if builder.seen == "" {
		t.Fatal("the assembly layer saw no run id")
	}
	if !strings.Contains(captured.String(), `"runId":"`+builder.seen+`"`) {
		t.Fatalf("the stream run id differs from the one the runtime saw: %s", captured.String())
	}
	if !strings.Contains(logs.String(), "[run="+builder.seen+"]") {
		t.Fatalf("log missing the run prefix: %s", logs.String())
	}
}

func TestV2ResumeGetsItsOwnRunID(t *testing.T) {
	var logs bytes.Buffer
	obs.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { obs.SetLogger(obs.DefaultLogger(os.Stderr)) })

	builder := &runIDCapturingBuilder{}
	srv, _ := newV2RunServerWith(t, builder)
	id := createThread(t, srv)

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id, "query": "hello", "runId": "first",
	}); got.code != 200 {
		t.Fatalf("first run = %d, want 200: %s", got.code, got.body)
	}
	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id, "query": "hello again", "runId": "second",
	}); got.code != 200 {
		t.Fatalf("second run = %d, want 200: %s", got.code, got.body)
	}

	if !strings.Contains(logs.String(), "[run=first]") || !strings.Contains(logs.String(), "[run=second]") {
		t.Fatalf("expected both run ids in the log: %s", logs.String())
	}
}

func TestRunStartedCarriesTheRunIDField(t *testing.T) {
	srv, captured := newV2RunServer(t)
	id := createThread(t, srv)

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id, "query": "hello", "runId": "rid",
	}); got.code != 200 {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}

	for _, line := range strings.Split(captured.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &ev); err != nil {
			continue
		}
		if ev["type"] == "RUN_STARTED" {
			if ev["runId"] != "rid" {
				t.Fatalf("RUN_STARTED runId = %v, want rid", ev["runId"])
			}
			return
		}
	}
	t.Fatalf("no RUN_STARTED in stream: %s", captured.String())
}

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

	"openheron/agentkit/obs"
	"openheron/contract"
)

// runIDCapturingBuilder 记下装配时拿到的 runId。
//
// ★ 它证明的是**ctx 真的流到了装配层**：api 注入的 runId 若只停在
//
//	接口层，日志里看着有前缀，模型调用那边却没有同一个 id ——
//	那种「看着有、其实不通」最费排查时间。
type runIDCapturingBuilder struct {
	seen string
}

func (b *runIDCapturingBuilder) Build(ctx context.Context, _, _ string, _ contract.TurnKind) (
	adk.TypedAgent[*schema.Message], error) {
	b.seen = obs.RunID(ctx)
	return noopAgent{}, nil
}

// 一次运行只有一个 runId：日志前缀、SSE 协议、装配层看到的是同一个。
//
// ★ 此前 runId 只存在于协议层（transport 在渲染时才造），
//
//	于是拿协议里的 id 去 grep 日志什么都找不到 ——
//	那是「跨层排障没有共同 id」的直接后果。
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

	// ① 协议里带的是它
	if !strings.Contains(captured.buf.String(), `"runId":"run-under-test"`) {
		t.Fatalf("stream missing the run id: %s", captured.buf.String())
	}

	// ② 日志前缀里带的是它
	if !strings.Contains(logs.String(), "[run=run-under-test]") {
		t.Fatalf("log missing the run prefix: %s", logs.String())
	}

	// ③ 装配层看到的也是它（ctx 真的流下去了）
	if builder.seen != "run-under-test" {
		t.Fatalf("the assembly layer saw run id %q, want run-under-test", builder.seen)
	}
}

// 客户端没带 runId 时，服务端要造一个 —— 而且要造在**进运行时之前**。
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
	if !strings.Contains(captured.buf.String(), `"runId":"`+builder.seen+`"`) {
		t.Fatalf("the stream run id differs from the one the runtime saw: %s", captured.buf.String())
	}
	if !strings.Contains(logs.String(), "[run="+builder.seen+"]") {
		t.Fatalf("log missing the run prefix: %s", logs.String())
	}
}

// 每一轮都有自己的 id：审批恢复是新的一轮，不是同一轮的延续。
func TestV2ResumeGetsItsOwnRunID(t *testing.T) {
	var logs bytes.Buffer
	obs.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { obs.SetLogger(obs.DefaultLogger(os.Stderr)) })

	builder := &runIDCapturingBuilder{}
	srv, _ := newV2RunServerWith(t, builder)
	id := createThread(t, srv)

	// 第一轮
	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id, "query": "hello", "runId": "first",
	}); got.code != 200 {
		t.Fatalf("first run = %d, want 200: %s", got.code, got.body)
	}
	// 第二轮（审批恢复走的是同一个入口）
	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id, "query": "hello again", "runId": "second",
	}); got.code != 200 {
		t.Fatalf("second run = %d, want 200: %s", got.code, got.body)
	}

	if !strings.Contains(logs.String(), "[run=first]") || !strings.Contains(logs.String(), "[run=second]") {
		t.Fatalf("expected both run ids in the log: %s", logs.String())
	}
}

// 断言 SSE 里的 runId 字段名没变（协议契约）。
func TestRunStartedCarriesTheRunIDField(t *testing.T) {
	srv, captured := newV2RunServer(t)
	id := createThread(t, srv)

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id, "query": "hello", "runId": "rid",
	}); got.code != 200 {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}

	for _, line := range strings.Split(captured.buf.String(), "\n") {
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
	t.Fatalf("no RUN_STARTED in stream: %s", captured.buf.String())
}

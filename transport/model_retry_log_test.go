package transport

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/obs"
)

// ── 重试在服务端日志里的痕迹 ─────────────────────────────────────────────

// captureLog 把统一日志入口的输出接到 buffer 上，返回恢复函数。
//
// ★ 用它是为了断言「日志里真的有这一行」—— 而「有没有」正是这条改动的
//
//	全部意义：界面上看得见「在重试」，服务端日志里查无实据，排障时
//	只能去翻前端事件流。
func captureLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := obs.Logger()
	obs.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &buf, func() { obs.SetLogger(prev) }
}

// retryEvent 造一个「模型要重试」的事件（eino 用 WillRetryError 表示）。
func retryEvent(attempt int) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Err: &adk.WillRetryError{
			ErrStr:       "429 too many requests",
			RetryAttempt: attempt,
		},
	}
}

// ★ 重试：流里给前端看（CUSTOM 事件），日志里给排障看。
func TestAModelRetryIsAlsoVisibleInTheServerLog(t *testing.T) {
	buf, restore := captureLog(t)
	defer restore()

	evs := []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("hello")),
		retryEvent(1),
		exitEvent(),
	}
	got, _, _ := runErr(t, evs, Options{})

	// 前端那一半没丢：仍然有 CUSTOM 事件。
	customs := 0
	for _, e := range got {
		if e.Type() == events.EventTypeCustom {
			customs++
		}
	}
	if customs == 0 {
		t.Fatalf("the model_retry custom event must still reach the client: %v", typesOf(got))
	}

	// ★ 新增的那一半：服务端日志里有一行。
	if !strings.Contains(buf.String(), "model retry") {
		t.Fatalf("the server log must mention the retry, got %q", buf.String())
	}
}

// ★ 不是重试的错误**不该**打出这一行 —— 否则日志里全是噪音，
// 「模型在重试」这句话就又不值钱了。
func TestANormalErrorDoesNotLogAModelRetry(t *testing.T) {
	buf, restore := captureLog(t)
	defer restore()

	evs := []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("hello")),
		{Err: errBoom{}},
	}
	runErr(t, evs, Options{})

	if strings.Contains(buf.String(), "model retry") {
		t.Fatalf("an ordinary error must not be logged as a retry: %q", buf.String())
	}
}

// errBoom 是一个普通错误（非 WillRetryError）。
type errBoom struct{}

func (errBoom) Error() string { return "boom" }

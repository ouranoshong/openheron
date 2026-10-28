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

func captureLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := obs.Logger()
	obs.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &buf, func() { obs.SetLogger(prev) }
}

func retryEvent(attempt int) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Err: &adk.WillRetryError{
			ErrStr:       "429 too many requests",
			RetryAttempt: attempt,
		},
	}
}

func TestAModelRetryIsAlsoVisibleInTheServerLog(t *testing.T) {
	buf, restore := captureLog(t)
	defer restore()

	evs := []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("hello")),
		retryEvent(1),
		exitEvent(),
	}
	got, _, _ := runErr(t, evs, Options{})

	customs := 0
	for _, e := range got {
		if e.Type() == events.EventTypeCustom {
			customs++
		}
	}
	if customs == 0 {
		t.Fatalf("the model_retry custom event must still reach the client: %v", typesOf(got))
	}

	if !strings.Contains(buf.String(), "model retry") {
		t.Fatalf("the server log must mention the retry, got %q", buf.String())
	}
}

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

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

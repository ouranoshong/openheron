package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/app"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage/filestore"
	"github.com/ouranoshong/openheron/transport"
)

// ★ **A run that cannot start must say why.** The builder is where a
//
//	configuration problem becomes a runtime one: an agent whose model is not
//	declared under llm/, a skill that cannot be read back, a provider file
//	that does not parse.
//
//	The build error used to go nowhere — the turn was never marked failed,
//	the waiter sat until the start timeout, and the client was told
//	"turn start timeout". A symptom that names neither the file nor the
//	reason is the one answer a user cannot act on.
func TestARunThatCannotStartCarriesTheReason(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")

	fm, err := filestore.NewFileManager(root, filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reason := errors.New(`agent "main" is left out of the tree: the model openai/gpt-4o-mini is not declared under llm/`)
	rt := run.New(run.Config{
		AgentBuilder:   failingBuilder{reason: reason},
		SessionManager: fm,
		Input:          run.InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(ctx) })

	engine := route.NewEngine(hertzconfig.NewOptions(nil))
	srv := New(rt, fm)
	srv.h = engine
	var captured bufExtWriter
	srv.newStream = func(c *app.RequestContext) *transport.SSEStream {
		return transport.NewWithWriter(c, &captured)
	}
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })

	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "hello",
	})
	if got.code != 200 {
		t.Fatalf("run = %d (%s)", got.code, got.body)
	}

	waitForStream(t, &captured, "RUN_ERROR")
	body := captured.String()
	if !strings.Contains(body, "agent_unavailable") {
		t.Fatalf("the client must be told the agent is unavailable, not that a turn timed out: %s", body)
	}
	if !strings.Contains(body, "is not declared under llm/") {
		t.Fatalf("★ the reason must reach the client: %s", body)
	}
	if strings.Contains(body, "turn_start_timeout") {
		t.Fatalf("★ the start timeout must not stand in for the reason: %s", body)
	}
}

// failingBuilder is a builder that cannot build.
type failingBuilder struct {
	reason error
}

func (b failingBuilder) Build(context.Context, string, string, contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {
	return nil, b.reason
}

// Contexts reports what each agent's context allows. The fake has no
// models, so there are no windows.
func (failingBuilder) Contexts() map[string]contract.AgentContext { return nil }

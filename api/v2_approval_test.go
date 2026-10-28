package api

import (
	"context"
	"encoding/json"
	"os"
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
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/storage/filestore"
	"github.com/ouranoshong/openheron/transport"
)

func TestApprovalChainEndToEnd(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")

	fm, err := filestore.NewFileManager(root, filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rt := run.New(run.Config{
		AgentBuilder:   interruptingBuilder{},
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
	sess, err := srv.sm.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "写个文件",
	})
	if got.code != 200 {
		t.Fatalf("run = %d (%s)", got.code, got.body)
	}
	if !strings.Contains(captured.buf.String(), "int-e2e") {
		t.Fatalf("the interrupt must reach the client: %s", captured.buf.String())
	}
	waitForPending(t, sess, 1)

	ckpt := filepath.Join(root, id, "checkpoints", id+".ckpt")
	info, err := os.Stat(ckpt)
	if err != nil {
		t.Fatalf("the checkpoint must live in the session's checkpoints directory: %v", err)
	}
	raw, _ := os.ReadFile(ckpt)
	t.Logf("checkpoint file = %d bytes, payload = %d bytes", info.Size(), len(raw))

	pending, err := sess.PendingApproval()
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.Interrupts) != 1 {
		t.Fatalf("the pending approval must be recorded: %+v", pending)
	}
	got = callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	if got.code != 200 {
		t.Fatalf("messages = %d (%s)", got.code, got.body)
	}
	var view struct {
		PendingApprovals []struct {
			InterruptID string `json:"interruptId"`
			ToolName    string `json:"toolName"`
		} `json:"pendingApprovals"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.PendingApprovals) != 1 || view.PendingApprovals[0].InterruptID != "int-e2e" {
		t.Fatalf("the card must reach the client: %+v", view.PendingApprovals)
	}
	if view.State != "waiting_approval" {
		t.Fatalf("state = %q, want waiting_approval", view.State)
	}

	got = callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"resume": []map[string]any{{
			"interruptId": "int-e2e",
			"status":      "resolved",
			"payload":     map[string]any{"approved": true},
		}},
	})
	if got.code != 200 {
		t.Fatalf("the approval must be accepted: %d (%s)", got.code, got.body)
	}

	waitForPending(t, sess, 0)

	if !strings.Contains(captured.buf.String(), "RUN_ERROR") {
		t.Fatalf("the dead turn must be reported, not crashed on: %s", captured.buf.String())
	}

	waitForDecisions(t, sess, 1)
	recs := sess.Decisions()
	if recs[0].InterruptID != "int-e2e" || !recs[0].Approved {
		t.Fatalf("the decision must be audited: %+v", recs[0])
	}
	st, err := readSessionState(t, root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Decisions) != 1 || st.Decisions[0].InterruptID != "int-e2e" {
		t.Fatalf("the audit must be on disk: %+v", st.Decisions)
	}
}

func waitForPending(t *testing.T, sess storage.Session, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, _ := sess.PendingApproval()
		got := 0
		if p != nil {
			got = len(p.Interrupts)
		}
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p, _ := sess.PendingApproval()
	t.Fatalf("pending approvals = %+v, want %d", p, want)
}

func waitForDecisions(t *testing.T, sess storage.Session, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sess.Decisions()) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("decisions = %+v, want %d", sess.Decisions(), want)
}

func readSessionState(t *testing.T, root, id string) (state, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, id, "state.json"))
	if err != nil {
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, err
	}
	return st, nil
}

type state struct {
	Decisions []storage.ApprovalRecord `json:"decisions"`
}

type interruptingBuilder struct{}

func (interruptingBuilder) Build(context.Context, string, string, contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {
	return &interruptingAgent{}, nil
}

type interruptingAgent struct{}

func (a *interruptingAgent) Name(context.Context) string        { return "interrupting" }
func (a *interruptingAgent) Description(context.Context) string { return "interrupting" }

func (a *interruptingAgent) Run(ctx context.Context, _ *adk.TypedAgentInput[*schema.Message], _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	go func() {
		defer gen.Close()
		gen.Send(&adk.TypedAgentEvent[*schema.Message]{
			Action: &adk.AgentAction{
				Interrupted: &adk.InterruptInfo{
					InterruptContexts: []*adk.InterruptCtx{
						{ID: "int-e2e", IsRootCause: true, Info: "write_file needs approval"},
					},
				},
			},
		})
		gen.Send(&adk.TypedAgentEvent[*schema.Message]{
			Action: &adk.AgentAction{Exit: true},
		})
	}()
	return iter
}

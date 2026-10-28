package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/spec"
	"openheron/contract"
	"openheron/storage"
)

func newAgentServer(t *testing.T) (*Server, *spec.Config) {
	t.Helper()
	srv := newV2Server(t)
	cfg := testConfig(t)
	srv.SetConfigSource(func() *spec.Config { return cfg })
	return srv, cfg
}

func TestSwitchAgentPersistsAndIsReported(t *testing.T) {
	srv, _ := newAgentServer(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/agent", map[string]any{"agentId": "worker"})
	if got.code != consts.StatusOK {
		t.Fatalf("switch = %d, want 200: %s", got.code, got.body)
	}
	var meta threadMeta
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode switch: %v (%s)", err, got.body)
	}
	if meta.AgentID != "worker" {
		t.Fatalf("switch response agentId = %q, want worker", meta.AgentID)
	}

	var detail threadDetail
	read := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	if err := json.Unmarshal([]byte(read.body), &detail); err != nil {
		t.Fatalf("decode detail: %v (%s)", err, read.body)
	}
	if detail.AgentID != "worker" {
		t.Fatalf("GET agentId = %q, want worker", detail.AgentID)
	}

	var list threadList
	listed := callJSON(t, srv, "GET", "/v2/sessions", nil)
	if err := json.Unmarshal([]byte(listed.body), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, listed.body)
	}
	if len(list.Items) != 1 || list.Items[0].AgentID != "worker" {
		t.Fatalf("the sidebar projection must carry the agent too: %+v", list.Items)
	}
}

func TestSessionWithoutSwitchReportsMain(t *testing.T) {
	srv, _ := newAgentServer(t)
	id := createThread(t, srv)

	var meta threadMeta
	got := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AgentID != "main" {
		t.Fatalf("agentId = %q, want main", meta.AgentID)
	}
}

func TestSwitchAgentRejectsUnknownAgent(t *testing.T) {
	srv, _ := newAgentServer(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/agent", map[string]any{"agentId": "ghost"})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeAgentNotFound {
		t.Fatalf("code = %q, want %q", body.Code, codeAgentNotFound)
	}
}

func TestSwitchAgentRejectsUndeclaredAgent(t *testing.T) {
	srv, _ := newAgentServer(t)
	id := createThread(t, srv)
	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/agent", map[string]any{"agentId": "ghost"})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeAgentNotFound {
		t.Fatalf("code = %q, want %q", body.Code, codeAgentNotFound)
	}
}

func TestSwitchAgentRejectsPendingApproval(t *testing.T) {
	srv, _ := newAgentServer(t)
	id := createThread(t, srv)
	seedApproval(t, srv, id, "int-1", "write_file")

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/agent", map[string]any{"agentId": "worker"})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeAgentBusy {
		t.Fatalf("code = %q, want %q", body.Code, codeAgentBusy)
	}
}

func TestCreateSessionWithAgentIDPersistsTheRoot(t *testing.T) {
	srv, captured := newV2RunServer(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	created := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"agentId": "worker"})
	if created.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", created.code, created.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(created.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, created.body)
	}
	if meta.AgentID != "worker" {
		t.Fatalf("the created session's root = %q, want worker", meta.AgentID)
	}

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": meta.ThreadID,
		"query":    "hello",
	})
	if got.code != consts.StatusOK {
		t.Fatalf("run = %d, want 200: %s", got.code, got.body)
	}
	_ = captured

	var list threadList
	listed := callJSON(t, srv, "GET", "/v2/sessions", nil)
	if err := json.Unmarshal([]byte(listed.body), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, listed.body)
	}
	if len(list.Items) != 1 {
		t.Fatalf("want exactly one session, got %d", len(list.Items))
	}
	if list.Items[0].AgentID != "worker" {
		t.Fatalf("agentId = %q, want worker", list.Items[0].AgentID)
	}
}

func TestCreateSessionWithUnknownAgentIDCreatesNothing(t *testing.T) {
	srv, _ := newV2RunServer(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"agentId": "ghost"})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	if n := sessionCount(t, srv); n != 0 {
		t.Fatalf("a rejected agentId must not create a session, got %d", n)
	}
}

type recordingSM struct {
	storage.SessionManager
	created []string
}

func (r *recordingSM) Create(ctx context.Context, agentID string) (storage.Session, error) {
	r.created = append(r.created, agentID)
	return r.SessionManager.Create(ctx, agentID)
}

func TestCreateSessionPicksTheRootAtCreationTime(t *testing.T) {
	srv, cfg := newAgentServer(t)
	spy := &recordingSM{SessionManager: srv.sm}
	srv.sm = spy

	if cfg.DefaultAgent() != "main" {
		t.Fatalf("the default root must be main, got %q", cfg.DefaultAgent())
	}

	created := callJSON(t, srv, "POST", "/v2/sessions", nil)
	if created.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", created.code, created.body)
	}
	if len(spy.created) != 1 || spy.created[0] != "main" {
		t.Fatalf("the session must be born with main as its root, got %v", spy.created)
	}

	created = callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"agentId": "worker"})
	if created.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", created.code, created.body)
	}
	if len(spy.created) != 2 || spy.created[1] != "worker" {
		t.Fatalf("the chosen root must be the one recorded, got %v", spy.created)
	}
}

func TestActiveAgentIDBackfillsALegacySession(t *testing.T) {
	srv, cfg := newAgentServer(t)
	sess := mustCreate(t, srv)
	if got := sess.AgentID(); got != "" {
		t.Fatalf("a legacy session must start without a root, got %q", got)
	}

	if got := srv.activeAgentID(sess); got != cfg.DefaultAgent() {
		t.Fatalf("active agent = %q, want %q", got, cfg.DefaultAgent())
	}
	if got := sess.AgentID(); got != cfg.DefaultAgent() {
		t.Fatalf("the backfilled root must be persisted, got %q", got)
	}
}

func TestActiveAgentIDPrefersTheState(t *testing.T) {
	srv, _ := newAgentServer(t)
	sess := mustCreate(t, srv)
	if err := sess.SetAgentID("worker"); err != nil {
		t.Fatal(err)
	}
	if got := srv.activeAgentID(sess); got != "worker" {
		t.Fatalf("active agent = %q, want worker", got)
	}
}

type blockingAgent struct {
	onStop func()
}

func (blockingAgent) Name(context.Context) string        { return "blocking" }
func (blockingAgent) Description(context.Context) string { return "blocking" }

func (a blockingAgent) Run(ctx context.Context, _ *adk.TypedAgentInput[*schema.Message],
	_ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	go func() {
		<-ctx.Done()
		if a.onStop != nil {
			a.onStop()
		}
		gen.Close()
	}()
	return iter
}

type blockingBuilder struct {
	onStop func()
}

func (b blockingBuilder) Build(context.Context, string, string, contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {
	return blockingAgent{onStop: b.onStop}, nil
}

func TestSwitchAgentRejectsRunningTurn(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	ctx := context.Background()
	sess := mustCreate(t, srv)
	if _, err := srv.rt.Submit(ctx, sess.ID(), "hold the turn open"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+sess.ID()+"/agent", map[string]any{"agentId": "worker"})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeAgentBusy {
		t.Fatalf("code = %q, want %q", body.Code, codeAgentBusy)
	}
}

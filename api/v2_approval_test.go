package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/app"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
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
	srv := New(rt, fm, obs.Discard())
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
	// ★ The interrupt is written by the run's own goroutine, and the
	//   response returns before that happens — so the buffer has to be
	//   **waited for**, not read once. Reading once is a race that fails
	//   whenever the scheduler happens to run the writer late.
	waitForStream(t, &captured, "int-e2e")
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

	if !strings.Contains(captured.String(), "RUN_ERROR") {
		t.Fatalf("the dead turn must be reported, not crashed on: %s", captured.String())
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

// waitForStream waits until the SSE buffer carries the wanted text.
//
// ★ The run writes its events from its own goroutine, so the buffer is
//
//	read while it is being written. Waiting is the only correct way to
//	look at it.
func waitForStream(t *testing.T, w *bufExtWriter, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the stream never carried %q: %s", want, w.String())
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

// ── the approval window ──────────────────────────────────────────────────

// approvalGate holds a session's approval write open.
//
// ★ The window between "the run's loop has exited" and "the approval is on
//
//	disk" is a few milliseconds wide in production, and it is the window a
//	reconnecting client lands in. ★ The gate makes it wide enough to look
//	at, and the hit channel says when the window is open.
type approvalGate struct {
	hit     chan struct{}
	release chan struct{}
	once    sync.Once
}

func newApprovalGate() *approvalGate {
	return &approvalGate{hit: make(chan struct{}, 1), release: make(chan struct{})}
}

// open lets the held write through.
func (g *approvalGate) open() { g.once.Do(func() { close(g.release) }) }

// gatedSessionManager holds every approval write of the sessions it hands out.
type gatedSessionManager struct {
	storage.SessionManager
	gate *approvalGate
}

func (m *gatedSessionManager) Get(ctx context.Context, id string) (storage.Session, error) {
	sess, err := m.SessionManager.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &gatedSession{Session: sess, gate: m.gate}, nil
}

// gatedSession holds the approval writes of one session.
type gatedSession struct {
	storage.Session
	gate *approvalGate
}

func (s *gatedSession) SetPendingApproval(p *storage.PendingApproval) error {
	select {
	case s.gate.hit <- struct{}{}:
	default:
	}
	<-s.gate.release
	return s.Session.SetPendingApproval(p)
}

// streamSink hands out one capture buffer per connection.
//
// ★ The attach is a second connection, and it has to be read on its own: the
//
//	run's own stream already carries the approval, and a test that read both
//	out of one buffer would pass even when the attach carried nothing.
type streamSink struct {
	mu      sync.Mutex
	writers []*bufExtWriter
}

func (s *streamSink) stream(c *app.RequestContext) *transport.SSEStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := &bufExtWriter{}
	s.writers = append(s.writers, w)
	return transport.NewWithWriter(c, w)
}

// waitFor waits until the i-th connection carries the wanted text.
func (s *streamSink) waitFor(t *testing.T, i int, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.text(i).contains(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stream %d never carried %q: %s", i, want, s.text(i).string())
}

// text reads one connection's capture buffer.
func (s *streamSink) text(i int) captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.writers) {
		return captured{}
	}
	return captured{body: s.writers[i].String()}
}

// captured is what one connection wrote.
type captured struct{ body string }

func (c captured) contains(want string) bool { return strings.Contains(c.body, want) }
func (c captured) string() string            { return c.body }

// An attach that lands while the approval is being written must still be
// served — and it must carry the approval with it.
//
// ★ The server refuses an attach on the runtime's turn, and the turn used to
//
//	be released before the approval reached the disk: an attach in between was
//	answered "the run is no longer live", and the client had nothing to show
//	— the transcript could not hold the approval yet either. ★ The run keeps
//	its turn until the barrier stands, so the attach is served and the
//	approval comes straight out of the run's own window.
func TestAnAttachDuringTheApprovalWriteIsServed(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")

	fm, err := filestore.NewFileManager(root, filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	gate := newApprovalGate()
	defer gate.open()
	sm := &gatedSessionManager{SessionManager: fm, gate: gate}

	rt := run.New(run.Config{
		AgentBuilder:   interruptingBuilder{},
		SessionManager: sm,
		Input:          run.InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(ctx) })

	engine := route.NewEngine(hertzconfig.NewOptions(nil))
	srv := New(rt, sm, obs.Discard())
	srv.h = engine
	sink := &streamSink{}
	srv.newStream = sink.stream
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })

	id := createThread(t, srv)
	sess, err := srv.sm.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	// ★ The run's own response ends when the run ends — and the run ends
	//	when the approval is written, which the gate holds. ★ So the request
	//	has to run beside the test, not in front of it.
	runDone := make(chan result, 1)
	go func() {
		runDone <- callJSON(t, srv, "POST", "/v2/run", map[string]any{
			"threadId": id,
			"query":    "写个文件",
		})
	}()

	// ★ The approval is being written: the run is in the window, and this is
	//	where a reconnecting client lands.
	select {
	case <-gate.hit:
	case <-time.After(3 * time.Second):
		t.Fatal("the approval was never written")
	}

	runID := activeRunID(t, srv, id)
	attachDone := make(chan result, 1)
	go func() {
		attachDone <- callJSON(t, srv, "POST", "/v2/run", map[string]any{
			"threadId": id,
			"runId":    runID,
		})
	}()

	// ★ The attach was served: its own stream opened. ★ A refused attach
	//	writes nothing — the refusal is a JSON body, not a stream.
	sink.waitFor(t, 1, "RUN_STARTED")

	// ★ The barrier stands behind the approval: once it is on disk the run
	//	is over, and the run's own response ends.
	gate.open()

	attach := <-attachDone
	if attach.code != 200 {
		t.Fatalf("the attach must be served while the approval is being written: %d (%s)",
			attach.code, attach.body)
	}
	if !sink.text(1).contains("int-e2e") {
		t.Fatalf("the attach must carry the approval: %s", sink.text(1).string())
	}
	if got := <-runDone; got.code != 200 {
		t.Fatalf("run = %d (%s)", got.code, got.body)
	}

	// ★ And now the run is over: the same attach is refused, and the client
	//	reads the transcript instead — where the approval is.
	waitForPending(t, sess, 1)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := rt.LiveRun(id, runID); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	late := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"runId":    runID,
	})
	if late.code != 404 {
		t.Fatalf("a finished run must be refused: %d (%s)", late.code, late.body)
	}
}

// ★ A query that arrives while the approval is being written must be refused
// — and the approval must still land. ★
//
// The session's slot used to be free in that window: the query was admitted,
// it took the session over, and the old round's exit took the "superseded"
// branch — which does not write the approval. ★ The user's question was
// gone, and nothing said so.
func TestAQueryDuringTheApprovalWriteIsRefused(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")

	fm, err := filestore.NewFileManager(root, filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	gate := newApprovalGate()
	defer gate.open()
	sm := &gatedSessionManager{SessionManager: fm, gate: gate}

	rt := run.New(run.Config{
		AgentBuilder:   interruptingBuilder{},
		SessionManager: sm,
		Input:          run.InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(ctx) })

	engine := route.NewEngine(hertzconfig.NewOptions(nil))
	srv := New(rt, sm, obs.Discard())
	srv.h = engine
	sink := &streamSink{}
	srv.newStream = sink.stream
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })

	id := createThread(t, srv)
	sess, err := srv.sm.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	// ★ The run's own response ends when the run ends, and the run ends when
	//	the approval is written — which the gate holds. ★ So the request runs
	//	beside the test, not in front of it.
	runDone := make(chan result, 1)
	go func() {
		runDone <- callJSON(t, srv, "POST", "/v2/run", map[string]any{
			"threadId": id,
			"query":    "写个文件",
		})
	}()

	select {
	case <-gate.hit:
	case <-time.After(3 * time.Second):
		t.Fatal("the approval was never written")
	}

	// ★ The session still owes the user a question: the query is refused.
	// ★ It has to be refused **at once** — a refusal is an answer, not a
	//	wait — and it has to be refused while the window is open, so the
	//	request runs beside the test: ★ a query that was admitted instead
	//	would stream until the round ended, and the round ends when the
	//	gate opens, which happens after this test is over.
	busyDone := make(chan result, 1)
	go func() {
		busyDone <- callJSON(t, srv, "POST", "/v2/run", map[string]any{
			"threadId": id,
			"query":    "换个问题",
		})
	}()

	select {
	case busy := <-busyDone:
		if busy.code != consts.StatusConflict {
			t.Fatalf("a query in the approval window must be refused: %d (%s)", busy.code, busy.body)
		}
		var body errBody
		if err := json.Unmarshal([]byte(busy.body), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, busy.body)
		}
		if body.Code != codeSessionBusy {
			t.Fatalf("code = %q, want %q", body.Code, codeSessionBusy)
		}
		if !body.Retryable {
			t.Fatal("the window closes on its own, so the conflict is retryable")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the query in the approval window was not refused")
	}

	// ★ And the approval must land behind the window.
	gate.open()
	if got := <-runDone; got.code != 200 {
		t.Fatalf("run = %d (%s)", got.code, got.body)
	}
	waitForPending(t, sess, 1)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	if got.code != 200 {
		t.Fatalf("messages = %d (%s)", got.code, got.body)
	}
	var view struct {
		PendingApprovals []struct {
			InterruptID string `json:"interruptId"`
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
}

// activeRunID reads the run that owns the session.
func activeRunID(t *testing.T, srv *Server, id string) string {
	t.Helper()
	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/runs", nil)
	if got.code != 200 {
		t.Fatalf("runs = %d (%s)", got.code, got.body)
	}
	var view runsView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.ActiveRunID == "" {
		t.Fatalf("the session must have an active run: %s", got.body)
	}
	return view.ActiveRunID
}

type interruptingBuilder struct{}

func (interruptingBuilder) Build(context.Context, string, string, contract.TurnKind, string) (
	adk.TypedAgent[*schema.Message], string, error) {
	return &interruptingAgent{}, "", nil
}

// Contexts reports what each agent's context allows. The fake has no
// models, so there are no windows.
func (interruptingBuilder) Contexts() map[string]contract.AgentContext { return nil }

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

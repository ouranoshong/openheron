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

// /agents 的会话级切换：状态落在 state.json，并且立刻反映到 threadMeta。
//
// ★ 切换只影响之后的轮次，所以历史不改写；测试要钉的是「下一个请求读到的
// 是 task」而不是「消息里的内容变了」。

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

	// ★ 读路径拿到的必须是刚落盘的那份 —— 只改内存的话，刷新页面就回到 main。
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

// 没有切过的会话必须读成 main，而不是空串 —— 客户端只认具体名字。
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

// 只有 main 的直接子 agent 能当根。孙 agent 需要自带一条委派链，
// 放行会让用户以为换了边界，其实只换了个起点。
func TestSwitchAgentRejectsUndeclaredAgent(t *testing.T) {
	srv, _ := newAgentServer(t)
	id := createThread(t, srv)
	// ★ 只有**没声明**的才拒绝 —— 嵌套的 agent 也是能选的（见
	//   TestSwitchSessionToAnyDeclaredAgent）。
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

// 待审批时切换会打断 resume 的归属，必须 409 而不是悄悄覆盖。
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

	// ★ 建会话时定根：会话一旦存在，它的根就是会话的属性。
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

// ── 入口 agent 记在 state 里 ───────────────────────────────────────────────

// recordingSM 记下每次建会话时**交给会话层**的入口 agent。
//
// ★ 用它而不是读回包：回包里的 agentId 是投影，老会话的补齐也会填上它
//
//	—— 只有「建会话那一刻交出去的是什么」能证明根是**创建时**定的。
type recordingSM struct {
	storage.SessionManager
	created []string
}

func (r *recordingSM) Create(ctx context.Context, agentID string) (storage.Session, error) {
	r.created = append(r.created, agentID)
	return r.SessionManager.Create(ctx, agentID)
}

// 建会话时就把根定下来：没给就用默认根（main），给了就用校验过的那个。
func TestCreateSessionPicksTheRootAtCreationTime(t *testing.T) {
	srv, cfg := newAgentServer(t)
	spy := &recordingSM{SessionManager: srv.sm}
	srv.sm = spy

	// ★ 默认 agent 就是 main，**不是配置项**：每个 agent 都能当会话的
	//   agent，main 只是「新会话没指定时用哪个」的那个约定。
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

// 老会话（state 里没有根）按当前默认根补齐，并**写回**。
//
// ★ 写回是补齐的最后一环：不写回的话，每个读它的人都要自己回退，
//
//	而「回退」与「state 里的值」未必是同一个。
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

// state 里的根优先：它是会话的属性，不是配置的投影。
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

// ── 运行中切换 ───────────────────────────────────────────────────────────

// blockingAgent 永不结束，用来把会话固定在 running 相位。
//
// ★ onStop 是可选钩子：它在 agent 的 ctx 被取消时触发 —— 也就是「这一轮
//
//	真的停了」的时刻。中止那条路的验收点就在这儿：只验「端点返 200」
//	不够，那正是「假停止」在服务端的样子。
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

// 运行中切换会留下「前半段 main、后半段 task」的记录，而两边都自洽 ——
// 这种历史最难查，所以直接拒绝。
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

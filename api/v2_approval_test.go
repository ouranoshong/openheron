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

	"openheron/agentkit/run"
	"openheron/contract"
	"openheron/storage"
	"openheron/storage/filestore"
	"openheron/transport"
)

// ★ 审批的**全链路**：中断 → checkpoint 落在会话目录 → 待批在 state.json →
//
//	界面看得见那张卡 → 答复 → 恢复 → 审计里留下这一笔。
//
//	这条链上每一段今天都动过（checkpoint 的位置、待批的磁盘形态、resume 的
//	取法），所以它是「审批功能是否正常」最直接的答案。
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

	// ── ① 一轮跑出中断 ───────────────────────────────────────────────
	// ★ 走**真的 HTTP 路径**：渲染层（transport）认得中断事件，才会把待批
	//
	//	交给编排层落盘 —— 直接调 startRun 就没有消费方，事件流没人接，
	//	这一轮永远结算不了。
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

	// ── ② checkpoint 落在会话自己的 checkpoints/ 子目录里 ─────────────
	ckpt := filepath.Join(root, id, "checkpoints", id+".ckpt")
	info, err := os.Stat(ckpt)
	if err != nil {
		t.Fatalf("the checkpoint must live in the session's checkpoints directory: %v", err)
	}
	raw, _ := os.ReadFile(ckpt)
	t.Logf("checkpoint file = %d bytes, payload = %d bytes", info.Size(), len(raw))

	// ── ③ 待批在盘上（state.json），界面看得见那张卡 ─────────────────
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

	// ── ④ 答复 → 恢复 ────────────────────────────────────────────────
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

	// 待批被取走（答复过了）。
	waitForPending(t, sess, 0)

	// ★ 这一轮随后**起跑失败**：合成 agent 的 checkpoint 里没有 runner 状态，
	//	框架恢复不了（真实 agent 的工具调用才有）。
	//
	//	接口必须**报出**这件事，而不是在 nil 事件流上崩掉 ——
	//	判死的轮次没有事件流（那是 makeOnAgentEvents 才挂上的）。
	if !strings.Contains(captured.buf.String(), "RUN_ERROR") {
		t.Fatalf("the dead turn must be reported, not crashed on: %s", captured.buf.String())
	}

	// ── ⑤ 审计里留下这一笔 ───────────────────────────────────────────
	waitForDecisions(t, sess, 1)
	recs := sess.Decisions()
	if recs[0].InterruptID != "int-e2e" || !recs[0].Approved {
		t.Fatalf("the decision must be audited: %+v", recs[0])
	}
	// 审计在盘上（下次重启还在）。
	st, err := readSessionState(t, root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Decisions) != 1 || st.Decisions[0].InterruptID != "int-e2e" {
		t.Fatalf("the audit must be on disk: %+v", st.Decisions)
	}
}

// waitForPending 等到会话的待批数量变成 want。
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

// waitForDecisions 等到审计里有 want 条。
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

// readSessionState 读盘上的 state.json（审计要能跨重启存活）。
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

// state 是 state.json 的磁盘形态（够用即可）。
type state struct {
	Decisions []storage.ApprovalRecord `json:"decisions"`
}

// interruptingBuilder 让 agent 在第一轮就中断，等人审批。
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
		// ★ 中断之后还要**退出**这一轮：框架在循环停下时才会结算，
		//	而结算才把待批落盘（见 agentkit/run/loop.go 的 archive）。
		gen.Send(&adk.TypedAgentEvent[*schema.Message]{
			Action: &adk.AgentAction{Exit: true},
		})
	}()
	return iter
}

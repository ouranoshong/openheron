package filestore

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

// ★ 这组用例守的是 P1 的核心承诺：审批详情必须**跨进程**存活。
//   旧实现只存 interrupt_id 与 cursor，工具名、参数、发起 agent 只存在于
//   那一轮的 SSE 流里；响应一断，GET /v2/sessions/{id} 就答不出「在批什么」。

func TestPendingApprovalDetailSurvivesReload(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)

	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	detail := &contract.InterruptDetail{
		ToolCallID:       "call-9x2",
		ToolName:         "execute",
		Arguments:        json.RawMessage(`{"command":"go test ./..."}`),
		Message:          "running go test requires approval",
		RequestedByAgent: "main",
		SubagentRunID:    "researcher",
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{
			InterruptID: "ic-7f3a",
			Detail:      detail,
		}},
	}); err != nil {
		t.Fatalf("SetPendingApproval: %v", err)
	}

	// 模拟重启：全新的 FileManager，从磁盘读
	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatalf("NewFileManager: %v", err)
	}
	reopened, err := fresh.Get(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	got, err := reopened.PendingApproval()
	if err != nil {
		t.Fatalf("PendingApproval: %v", err)
	}
	if got == nil {
		t.Fatal("the pending approval must survive a reload")
	}
	if len(got.Interrupts) != 1 {
		t.Fatalf("want 1 pending interrupt, got %d", len(got.Interrupts))
	}
	d := got.Interrupts[0].Detail
	if d == nil {
		t.Fatal("the detail must survive a reload")
	}
	if d.ToolName != "execute" || d.ToolCallID != "call-9x2" {
		t.Fatalf("tool identity lost: %+v", d)
	}
	if d.RequestedByAgent != "main" || d.SubagentRunID != "researcher" {
		t.Fatalf("attribution lost: %+v", d)
	}
	if d.Arguments == nil || !json.Valid(d.Arguments) {
		t.Fatalf("arguments must round-trip as valid JSON, got %s", d.Arguments)
	}
	var args struct{ Command string }
	if err := json.Unmarshal(d.Arguments, &args); err != nil || args.Command != "go test ./..." {
		t.Fatalf("arguments round-trip failed: %v %+v", err, args)
	}
}

// ★ 一轮里可以有**多个**独立待批（模型一次请求四个都要审批的工具）。
//
//	它们必须**各自**存各自的 id 与详情 —— 少存一个，用户就再也答不了它。
func TestPendingApprovalSetSurvivesReload(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)
	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{
			{InterruptID: "ic-1", Detail: &contract.InterruptDetail{ToolName: "write_file"}},
			{InterruptID: "ic-2", Detail: &contract.InterruptDetail{ToolName: "execute"}},
			{InterruptID: "ic-3", Detail: &contract.InterruptDetail{ToolName: "edit_file"}},
		},
	}); err != nil {
		t.Fatalf("SetPendingApproval: %v", err)
	}

	fresh, _ := NewFileManager(root, Options{})
	reopened, err := fresh.Get(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	got, err := reopened.PendingApproval()
	if err != nil || got == nil {
		t.Fatalf("PendingApproval: %v %+v", err, got)
	}
	if len(got.Interrupts) != 3 {
		t.Fatalf("want 3 pending interrupts, got %d", len(got.Interrupts))
	}
	for i, want := range []string{"ic-1", "ic-2", "ic-3"} {
		if got.Interrupts[i].InterruptID != want {
			t.Fatalf("interrupt[%d] = %q, want %q", i, got.Interrupts[i].InterruptID, want)
		}
	}
	if !got.Has("ic-2") || got.Has("ic-9") {
		t.Fatalf("Has must answer per interrupt, got %v", got.Interrupts)
	}
}

// 兼容：state.json 里没有 detail 字段的旧文件必须照常加载，不能报错。
func TestPendingApprovalWithoutDetailStillLoads(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)
	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 手写成 v1 时代的形状：只有 interrupt_id 与 cursor
	legacy := `{"version":1,"pending":{"interrupt_id":"ic-old","cursor":2},"cursor":2}` + "\n"
	if err := os.WriteFile(statePath(root, sess.ID()), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	fresh, _ := NewFileManager(root, Options{})
	reopened, err := fresh.Get(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("legacy state must load: %v", err)
	}
	got, err := reopened.PendingApproval()
	if err != nil || got == nil {
		t.Fatalf("legacy pending approval must load: %v %+v", err, got)
	}
	// ★ 旧格式是**单个**待批 —— 它必须被读成一个只有一项的集合。
	//
	//	否则升级之后磁盘上已有的会话会变成「没有待批」，用户再也答不了
	//	那一问 —— 而那正是最难排查的一类数据丢失。
	if len(got.Interrupts) != 1 {
		t.Fatalf("a legacy pending approval must load as a one-item set, got %d", len(got.Interrupts))
	}
	if got.Interrupts[0].InterruptID != "ic-old" {
		t.Fatalf("interrupt id = %q, want ic-old", got.Interrupts[0].InterruptID)
	}
	if got.Interrupts[0].Detail != nil {
		t.Fatalf("detail must be nil for a legacy state, got %+v", got.Interrupts[0].Detail)
	}
}

// Take 之后 detail 也必须消失，否则下一次审批会读到上一轮的残留。
func TestTakeClearsDetail(t *testing.T) {
	fm := newManager(t, Options{})
	sess, _ := fm.Create(t.Context(), "")
	_ = sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{
			InterruptID: "ic-1",
			Detail:      &contract.InterruptDetail{ToolName: "execute"},
		}},
	})

	taken, err := sess.TakePendingApproval()
	if err != nil || taken == nil {
		t.Fatalf("TakePendingApproval: %v %+v", err, taken)
	}
	if len(taken.Interrupts) != 1 || taken.Interrupts[0].Detail == nil ||
		taken.Interrupts[0].Detail.ToolName != "execute" {
		t.Fatalf("the taken approval must still carry its detail: %+v", taken)
	}
	after, err := sess.PendingApproval()
	if err != nil {
		t.Fatalf("PendingApproval after take: %v", err)
	}
	if after != nil {
		t.Fatalf("nothing must remain pending, got %+v", after)
	}
}

// 标题缓存：首条 user 消息落盘后，List 不必再读 transcript。
func TestTitleIsCachedOnFirstUserMessage(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)
	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	msg := message.NewUser("explain the SSE framing in transport")
	if err := sess.Append(msg); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// The title must already be in state.json.
	st, _, err := readState(statePath(root, sess.ID()))
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Title == "" {
		t.Fatal("the title must be cached on the first user message")
	}
	if st.MessageCount != 1 {
		t.Fatalf("message count = %d, want 1", st.MessageCount)
	}
	if st.CreatedAt.IsZero() {
		t.Fatal("created_at must be recorded so List() need not open the transcript")
	}

	// 重新加载后仍然命中缓存
	fresh, _ := NewFileManager(root, Options{})
	reopened, err := fresh.Get(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reopened.Len() != 1 {
		t.Fatalf("Len = %d, want 1", reopened.Len())
	}
}

// List 必须按最近活跃排序，而不是目录顺序。
func TestListSortsByRecency(t *testing.T) {
	fm := newManager(t, Options{})
	for _, q := range []string{"first", "second", "third"} {
		s, err := fm.Create(t.Context(), "")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := s.Append(message.NewUser(q)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	metas, err := fm.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(metas))
	}
	for i := 1; i < len(metas); i++ {
		if metas[i-1].UpdatedAt.Before(metas[i].UpdatedAt) {
			t.Fatalf("metas must be newest-first, got %+v", metas)
		}
	}
	if metas[0].MessageCount != 1 || metas[0].Title == "" {
		t.Fatalf("listing must carry title and count without parsing the transcript: %+v", metas[0])
	}
}

// 新会话初始就有标题，侧栏不必回落到 "New Session" 的二次推导。
func TestNewSessionHasTitleAndCount(t *testing.T) {
	fm := newManager(t, Options{})
	if _, err := fm.Create(t.Context(), ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	metas, err := fm.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("want 1 session, got %d", len(metas))
	}
	if metas[0].Title != "New Session" {
		t.Fatalf("title = %q, want New Session", metas[0].Title)
	}
	if metas[0].MessageCount != 0 {
		t.Fatalf("message count = %d, want 0", metas[0].MessageCount)
	}
}

// 只追加非 user 消息不得改写标题。
func TestTitleIgnoresNonUserMessages(t *testing.T) {
	fm := newManager(t, Options{})
	sess, _ := fm.Create(t.Context(), "")
	if err := sess.Append(&schema.Message{Role: schema.Assistant, Content: "hi"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	metas, _ := fm.List(t.Context())
	if metas[0].Title != "New Session" {
		t.Fatalf("a non-user message must not become the title, got %q", metas[0].Title)
	}
}

// newManagerAt builds a manager over an explicit root so the test can open a
// second manager over the same directory to simulate a restart.
func newManagerAt(t *testing.T, root string) *FileManager {
	t.Helper()
	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatalf("NewFileManager: %v", err)
	}
	return fm
}

// ★ 审计记录必须**扛得住后续的写**。
//
//	stateForWrite 是**重建**整个状态的 —— 它不带审计的话，
//	追加一条消息就把审批记录抹干净了。而「抹掉审计」是那种
//	平时看不出来、出事时才发现的故障。
func TestApprovalDecisionsSurviveLaterWrites(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)

	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.RecordDecision(storage.ApprovalRecord{
		InterruptID: "ic-1",
		ToolName:    "write_file",
		Approved:    true,
		Reason:      "ok",
		DecidedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	// 之后的正常写入：追加消息、改标题。
	if err := sess.Append(message.NewUser("hello")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := sess.SetTitle("a title"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}

	// 重开：审计还在吗？
	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := fresh.Get(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	recs := reopened.Decisions()
	if len(recs) != 1 {
		t.Fatalf("decisions = %d, want 1 — a later write wiped the audit", len(recs))
	}
	if recs[0].ToolName != "write_file" || !recs[0].Approved {
		t.Fatalf("the audit record changed: %+v", recs[0])
	}
}

// 审计有上限 —— 它不是日志，无上限会让 state.json 无限增长。
func TestApprovalDecisionsAreCapped(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)

	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < maxAuditRecords+10; i++ {
		if err := sess.RecordDecision(storage.ApprovalRecord{
			InterruptID: fmt.Sprintf("ic-%d", i),
			Approved:    true,
			DecidedAt:   time.Now().UTC(),
		}); err != nil {
			t.Fatalf("RecordDecision: %v", err)
		}
	}
	recs := sess.Decisions()
	if len(recs) != maxAuditRecords {
		t.Fatalf("decisions = %d, want %d", len(recs), maxAuditRecords)
	}
	// 截断的是**最旧的**，留下的要按原顺序。
	if recs[0].InterruptID != "ic-10" {
		t.Fatalf("the oldest records must be dropped, got %q first", recs[0].InterruptID)
	}
}

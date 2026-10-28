package filestore

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

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

func TestPendingApprovalWithoutDetailStillLoads(t *testing.T) {
	root := t.TempDir()
	fm := newManagerAt(t, root)
	sess, err := fm.Create(t.Context(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
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

	fresh, _ := NewFileManager(root, Options{})
	reopened, err := fresh.Get(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reopened.Len() != 1 {
		t.Fatalf("Len = %d, want 1", reopened.Len())
	}
}

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

	if err := sess.Append(message.NewUser("hello")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := sess.SetTitle("a title"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}

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
	if recs[0].InterruptID != "ic-10" {
		t.Fatalf("the oldest records must be dropped, got %q first", recs[0].InterruptID)
	}
}

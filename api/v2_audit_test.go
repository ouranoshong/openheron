package api

import (
	"encoding/json"
	"testing"
	"time"

	"openheron/storage"
)

// 审批的**审计**要能被读出来 ——
//
// ★ 记了却读不到等于没记：审计的价值在于事后能回答「批过什么」。
//
//	接口用 GET /v2/sessions/{id} 的 decisions 字段，与待批
//	（pendingApprovals）分开放 —— 前者是「已经发生的」，后者是「还欠着的」。
func TestV2SessionDetailCarriesTheApprovalAudit(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	sess, err := srv.sm.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if err := sess.RecordDecision(storage.ApprovalRecord{
		InterruptID:      "ic-1",
		ToolName:         "write_file",
		Approved:         true,
		Reason:           "ok",
		DecidedAt:        time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC),
		RequestedByAgent: "main",
	}); err != nil {
		t.Fatalf("record decision: %v", err)
	}

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	if got.code != 200 {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var detail struct {
		Decisions []struct {
			InterruptID string `json:"interruptId"`
			ToolName    string `json:"toolName"`
			Approved    bool   `json:"approved"`
			Reason      string `json:"reason"`
			DecidedAt   string `json:"decidedAt"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(got.body), &detail); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(detail.Decisions) != 1 {
		t.Fatalf("decisions = %d, want 1 (%s)", len(detail.Decisions), got.body)
	}
	d := detail.Decisions[0]
	if d.InterruptID != "ic-1" || d.ToolName != "write_file" || !d.Approved || d.Reason != "ok" {
		t.Fatalf("decision = %+v", d)
	}
	if d.DecidedAt == "" {
		t.Fatal("decidedAt must be exposed")
	}
}

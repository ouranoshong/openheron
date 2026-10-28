package filestore

import (
	"context"
	"testing"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

func TestAgentIDSurvivesApprovalWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(message.NewUser("do the thing")); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetAgentID("worker"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{
			InterruptID: "int-7",
			Detail:      &contract.InterruptDetail{ToolName: "write_file"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AgentID(); got != "worker" {
		t.Fatalf("agent id was dropped by a later state write: %q", got)
	}
	if reloaded.Title() == "" {
		t.Fatal("title was dropped by a later state write")
	}
	pending, err := reloaded.PendingApproval()
	if err != nil || pending == nil || pending.Interrupts[0].InterruptID != "int-7" {
		t.Fatalf("pending approval was dropped: %v / %+v", err, pending)
	}
	if pending.Interrupts[0].Detail == nil || pending.Interrupts[0].Detail.ToolName != "write_file" {
		t.Fatalf("the approval detail was dropped: %+v", pending.Interrupts[0].Detail)
	}
}

func TestLegacyStateWithoutAgentIDReadsEmpty(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	sess, _ := fm.Create(ctx, "")
	if err := sess.SetAgentID("worker"); err != nil {
		t.Fatal(err)
	}

	st, _, err := readState(statePath(root, sess.ID()))
	if err != nil {
		t.Fatal(err)
	}
	st.AgentID = ""
	if err := writeStateFile(statePath(root, sess.ID()), &st); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatalf("a legacy state file must still load: %v", err)
	}
	if got := reloaded.AgentID(); got != "" {
		t.Fatalf("a legacy state must read as empty (= main), got %q", got)
	}
}

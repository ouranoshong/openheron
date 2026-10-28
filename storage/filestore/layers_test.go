package filestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

func TestListingDoesNotParseTheTranscript(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")

	if err := sess.Append(message.NewUser("hello there")); err != nil {
		t.Fatal(err)
	}

	path := messagesPath(root, sess.ID())
	if err := os.WriteFile(path, []byte("{not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	metas, err := fresh.List(ctx)
	if err != nil {
		t.Fatalf("List must not need the transcript: %v", err)
	}
	if len(metas) != 1 || metas[0].Title != "hello there" {
		t.Fatalf("List = %+v", metas)
	}

	report, err := fresh.Inspect(ctx)
	if err != nil {
		t.Fatalf("Inspect must not need the transcript: %v", err)
	}
	if report.Clean != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestOpeningASessionFailsOnACorruptTranscript(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")

	path := messagesPath(root, sess.ID())
	if err := os.WriteFile(path, []byte("{not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	if _, err := fresh.Get(ctx, sess.ID()); err == nil {
		t.Fatal("a corrupt transcript must fail the session open, not yield an empty conversation")
	}
}

func TestEachSessionHasItsOwnCheckpointDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	first, _ := fm.Create(ctx, "")
	second, _ := fm.Create(ctx, "")

	store1, _ := first.GetCheckPointStore()
	if err := store1.Set(ctx, first.ID(), []byte("one")); err != nil {
		t.Fatal(err)
	}

	p1 := filepath.Join(root, first.ID(), ckptDir, first.ID()+ckptExt)
	p2 := filepath.Join(root, second.ID(), ckptDir, second.ID()+ckptExt)
	if !fileExists(p1) {
		t.Fatalf("the first session must own its checkpoint: %s", p1)
	}
	if fileExists(p2) {
		t.Fatalf("the second session must not see the first one's checkpoint: %s", p2)
	}
}

func TestCreateLaysDownTheWholeSession(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "main")
	id := sess.ID()

	for _, p := range []string{
		filepath.Join(root, id),
		messagesPath(root, id),
		statePath(root, id),
		workspacePath(root, id),
	} {
		if !fileExists(p) {
			t.Fatalf("missing %s", p)
		}
	}
	st, _, err := readState(statePath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentID != "main" {
		t.Fatalf("agent id = %q, want main", st.AgentID)
	}
}

func TestDeletingASessionTwiceIsANoOp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	id := sess.ID()

	if err := fm.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := fm.Delete(ctx, id); err != nil {
		t.Fatalf("the second delete must be a no-op: %v", err)
	}
	if fileExists(sessionDir(root, id)) {
		t.Fatal("the session directory must be gone")
	}
}

func TestCheckpointStoreFollowsTheHeaderID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	id := sess.ID()

	other := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	rewriteHeader(t, messagesPath(root, id), func(h *logHeader) { h.ID = other })

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ID() != other {
		t.Fatalf("the header wins: want %q, got %q", other, reloaded.ID())
	}

	store, _ := reloaded.GetCheckPointStore()
	if err := store.Set(ctx, reloaded.ID(), []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(root, other, ckptDir, other+ckptExt)) {
		t.Fatal("the checkpoint store must follow the header's id")
	}
}

func TestInspectionKeepsThePendingApproval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	id := sess.ID()

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	report, err := fresh.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Orphan != 1 {
		t.Fatalf("report = %+v", report)
	}

	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := reloaded.PendingApproval()
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.Interrupts) != 1 {
		t.Fatalf("the pending approval must survive the inspection: %+v", pending)
	}
}

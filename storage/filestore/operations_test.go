package filestore

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/ouranoshong/openheron/storage"
)

// The log appends one line and reads it back, and the log owns the number
// and the time.
func TestTheOperationLogAppendsAndReadsBack(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	log := sess.Operations()
	if log == nil {
		t.Fatal("the session must have an operation log")
	}

	if err := log.Append(ctx, storage.Operation{
		Kind:  storage.OpSessionRenamed,
		Actor: storage.ActorUser,
		Title: "renamed",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	rows, err := log.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Seq != 1 {
		t.Fatalf("want seq 1, got %d", rows[0].Seq)
	}
	if rows[0].Kind != storage.OpSessionRenamed {
		t.Fatalf("want the rename, got %q", rows[0].Kind)
	}
	if rows[0].Title != "renamed" {
		t.Fatalf("want the title, got %q", rows[0].Title)
	}
	if rows[0].At.IsZero() {
		t.Fatal("the log stamps the time when the caller left it empty")
	}
}

// A session that never had an operation has no rows, and that is a normal
// state — not a missing file.
func TestTheOperationLogOfASessionThatNeverHadOne(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := sess.Operations().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %d", len(rows))
	}
}

// A torn last line — a crash between two writes — is dropped, and the rows
// before it stay.
func TestTheOperationLogSkipsAMalformedLine(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	log := sess.Operations()
	if err := log.Append(ctx, storage.Operation{
		Kind:  storage.OpSessionCreated,
		Actor: storage.ActorUser,
	}); err != nil {
		t.Fatal(err)
	}

	path := operationsPath(fm.root, sess.ID())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":2,"kind":"sessi`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	rows, err := log.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want the one good row, got %d", len(rows))
	}
	if rows[0].Kind != storage.OpSessionCreated {
		t.Fatalf("want the creation, got %q", rows[0].Kind)
	}
}

// The number is seeded from the file. A counter that only lives in memory
// would hand out a number the file already uses, and two lines would claim
// the same place in the timeline.
func TestTheOperationLogContinuesTheSequenceAfterARestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Operations().Append(ctx, storage.Operation{
		Kind:  storage.OpSessionCreated,
		Actor: storage.ActorUser,
	}); err != nil {
		t.Fatal(err)
	}

	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Operations().Append(ctx, storage.Operation{
		Kind:  storage.OpSessionRenamed,
		Actor: storage.ActorUser,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := reloaded.Operations().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[1].Seq != 2 {
		t.Fatalf("want seq 2 after the restart, got %d", rows[1].Seq)
	}
}

// ★ The number and the line have to land in the same order. Two writers are
//
//	normal — the API layer and the runtime both write — and a number
//	allocated apart from its line can reach the file after a bigger one,
//	which leaves the file's order and the numbers' order disagreeing.
func TestConcurrentAppendsKeepTheFileAndTheNumbersInTheSameOrder(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	log := sess.Operations()

	const writers = 32
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := log.Append(ctx, storage.Operation{
				Kind:  storage.OpFileDeleted,
				Actor: storage.ActorUser,
				Path:  string(rune('a' + i%26)),
			}); err != nil {
				t.Errorf("Append: %v", err)
			}
		}(i)
	}
	wg.Wait()

	rows, err := log.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != writers {
		t.Fatalf("want %d rows, got %d", writers, len(rows))
	}
	for i, r := range rows {
		if r.Seq != uint64(i+1) {
			t.Fatalf("row %d carries seq %d: the file's order and the numbers' order disagree",
				i, r.Seq)
		}
	}
}

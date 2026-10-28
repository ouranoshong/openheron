package kb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"openheron/persist"
)

func TestReconcileDoesNotLoseAnotherWritersEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")

	a, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	reconcile(t, a, ReconcileRequest{
		Fresh: []Entry{ent("a1.md", "t", "甲的"), ent("a2.md", "t", "甲的")},
		Scope: []string{"a1.md", "a2.md"},
	})

	reconcile(t, b, ReconcileRequest{
		Fresh: []Entry{ent("b1.md", "t", "乙的")},
		Scope: []string{"b1.md"},
	})

	for _, p := range []string{"a1.md", "a2.md", "b1.md"} {
		if _, ok := findEntry(t, dir, p); !ok {
			t.Fatalf("entry %s was lost by a concurrent writer; catalog has %v", p, allPaths(t, dir))
		}
	}
}

func TestReconcileDoesNotRevertAnotherWritersUpdate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	a, _ := NewStore(dir)
	b, _ := NewStore(dir)

	reconcile(t, a, ReconcileRequest{Fresh: []Entry{ent("x.md", "t", "第一版")}})
	reconcile(t, a, ReconcileRequest{Fresh: []Entry{ent("x.md", "t", "第二版")}})
	reconcile(t, b, ReconcileRequest{
		Fresh: []Entry{ent("y.md", "t", "乙的")},
		Scope: []string{"y.md"},
	})

	got, ok := findEntry(t, dir, "x.md")
	if !ok {
		t.Fatal("x.md disappeared")
	}
	if got.Summary != "第二版" {
		t.Fatalf("x.md = %q, want 第二版 — a stale merge base reverted it", got.Summary)
	}
}

func TestReconcileSurvivesAConcurrentWriterMidFlight(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	a, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewStore(dir)

	reconcile(t, a, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "种子")}})

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := b.Reconcile(context.Background(), ReconcileRequest{
				Fresh: []Entry{ent("b-own.md", "t", "乙的")},
				Scope: []string{"b-own.md"},
			}); err != nil {
				t.Errorf("writer B: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 30; i++ {
		reconcile(t, a, ReconcileRequest{
			Fresh: []Entry{ent("a-own.md", "t", "甲的")},
			Scope: []string{"a-own.md"},
		})
	}
	close(stop)
	<-done

	for _, p := range []string{"seed.md", "a-own.md", "b-own.md"} {
		if _, ok := findEntry(t, dir, p); !ok {
			t.Fatalf("entry %s was lost to a concurrent writer; catalog has %v", p, allPaths(t, dir))
		}
	}
}

func TestReconcileRevisionIncreasesPerWrite(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	first := s.Catalog().Header.Revision
	if first == 0 {
		t.Fatal("revision stayed at 0 after a write")
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s2")}})
	if got := s.Catalog().Header.Revision; got <= first {
		t.Fatalf("revision = %d after a second write, want > %d", got, first)
	}
}

func TestReconcileDoesNotBlockOtherWritersWhileEmbedding(t *testing.T) {
	_, corpus := seedProject(t, map[string]string{
		"a.md": "# A\n\n## 一\n\n内容一\n",
		"b.md": "# B\n\n## 二\n\n内容二\n",
	})
	st := newTestStore(t)
	other, err := NewStore(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer stop()

	var writesOK int
	emb := newFakeEmbedder(16)
	emb.beforeEach = func() {
		reconcile(t, other, ReconcileRequest{Fresh: []Entry{ent("other.md", "t", "另一路")}})
		writesOK++
	}
	ix := newTestIndexer(t, st, emb, 10)

	res, err := Index(context.Background(), ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Changed != 2 {
		t.Fatalf("changed = %d, want 2", res.Changed)
	}
	if writesOK == 0 {
		t.Fatal("another writer was never able to commit while embedding; the expensive part must not block")
	}
}

func TestRunLockIsCancellable(t *testing.T) {
	st := newTestStore(t)
	other, err := NewStore(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = other.run.Try()
	if err == nil {
		t.Fatal("a second run was accepted while another held the lock")
	}
	if !errors.Is(err, persist.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Fatalf("the lock check took %s; it must not queue", d)
	}
	_ = ctx
}

func TestStartRunFailsFastWhileAnotherProcessIndexes(t *testing.T) {
	st := newTestStore(t)
	other, _ := NewStore(st.Dir())

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	started := time.Now()
	_, err = other.StartRun()
	if !errors.Is(err, persist.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if d := time.Since(started); d > 500*time.Millisecond {
		t.Fatalf("StartRun waited %s; it must not queue", d)
	}
	if !strings.Contains(err.Error(), "pid ") {
		t.Errorf("the error should name the holder so it can be checked, got: %v", err)
	}
}

func TestReconcileReadBackCheckCatchesAnOverwrite(t *testing.T) {
	s := newTestStore(t)
	md := filepath.Join(s.Dir(), catalogFile)

	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "种子")}})

	var once sync.Once
	s.afterWrite = func() {
		once.Do(func() {
			b, err := os.ReadFile(md)
			if err != nil {
				t.Errorf("read for interference: %v", err)
				return
			}
			cat, err := ParseCatalog(b)
			if err != nil {
				t.Errorf("parse for interference: %v", err)
				return
			}
			cat.Entries = append(cat.Entries, ent("other.md", "t", "别人加的"))
			if err := os.WriteFile(md, RenderCatalog(cat), 0o644); err != nil {
				t.Errorf("interference write: %v", err)
			}
		})
	}

	res := reconcile(t, s, ReconcileRequest{
		Fresh: []Entry{ent("new.md", "t", "新的")},
		Scope: []string{"new.md"},
	})

	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the overwrite must be detected)", res.Attempts)
	}
	for _, p := range []string{"seed.md", "new.md", "other.md"} {
		if _, ok := findEntry(t, s.Dir(), p); !ok {
			t.Fatalf("entry %s was lost to a forced collision; catalog has %v", p, allPaths(t, s.Dir()))
		}
	}
}

func findEntry(t *testing.T, dir, path string) (Entry, bool) {
	t.Helper()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cat := s.Catalog()
	return cat.Get(path)
}

func allPaths(t *testing.T, dir string) []string {
	t.Helper()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries := s.Entries()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

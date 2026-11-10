package kb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "docs"))
	t.Cleanup(func() { _ = s.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// ★ The store keeps its run-lock file open; on Windows an open handle
	//   stops TempDir from being removed.
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ent(path, title, summary string) Entry {
	return Entry{Path: path, Title: title, Summary: summary}
}

func reconcile(t *testing.T, s *Store, req ReconcileRequest) *ReconcileResult {
	t.Helper()
	res, err := s.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	t.Cleanup(func() { _ = s.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	byModel(t, s, "local/qwen3")
	reconcile(t, s, ReconcileRequest{
		Corpus: "docs",
		Model:  "local/qwen3",
		Dim:    2,
		Fresh:  []Entry{ent("a.md", "配置写入", "第一条"), ent("b.md", "会话归档", "第二条")},
	})
	if err := s.SetVectors(map[string][]float32{
		"a.md": {1, 0},
		"b.md": {0, 1},
	}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	s2, err := NewStore(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(s2.Entries()); got != 2 {
		t.Fatalf("after reopen entries = %d, want 2", got)
	}
	if got := s2.Sidecar().Model; got != "local/qwen3" {
		t.Errorf("sidecar model = %q, want local/qwen3", got)
	}
	docs := s2.Docs()
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	if len(docs[0].Vector) != 2 || len(docs[1].Vector) != 2 {
		t.Fatalf("vectors lost on reopen: %v", docs)
	}
}

func TestEmptyStoreIsNotAnError(t *testing.T) {
	s := newTestStore(t)
	if got := len(s.Entries()); got != 0 {
		t.Fatalf("fresh store entries = %d, want 0", got)
	}
	if got := s.Docs(); len(got) != 0 {
		t.Fatalf("docs on empty store = %v, want none", got)
	}
	if s.Catalog().Header.Created.IsZero() {
		t.Error("a fresh store has no creation time")
	}
}

func TestReconcileIsIdempotentByID(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "第一版")}})
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "第二版")}})

	got := s.Entries()
	if len(got) != 1 {
		t.Fatalf("entries = %d after re-reconcile, want 1", len(got))
	}
	if got[0].Summary != "第二版" {
		t.Fatalf("summary = %q, want 第二版", got[0].Summary)
	}
}

func TestReconcileRejectsModelMismatch(t *testing.T) {
	s := newTestStore(t)
	byModel(t, s, "m1")
	reconcile(t, s, ReconcileRequest{Model: "m1", Dim: 2, Fresh: []Entry{ent("a.md", "t", "s")}})

	_, err := s.Reconcile(context.Background(), ReconcileRequest{
		Model: "m2", Dim: 2, Fresh: []Entry{ent("b.md", "t", "s")},
	})
	if err == nil {
		t.Fatal("a different model was accepted")
	}
	if !strings.Contains(err.Error(), "delete") {
		t.Errorf("error should tell the user how to recover, got: %v", err)
	}
	if got := len(s.Entries()); got != 1 {
		t.Fatalf("entries = %d after a rejected reconcile, want 1", got)
	}
	if got := s.Sidecar().Model; got != "m1" {
		t.Errorf("sidecar model = %q after a rejected reconcile, want m1", got)
	}
}

func TestReconcileRejectsBadInput(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Reconcile(context.Background(), ReconcileRequest{
		Dim: -1, Fresh: []Entry{ent("a.md", "t", "s")},
	}); err == nil {
		t.Error("negative dim accepted")
	}
	if _, err := s.Reconcile(context.Background(), ReconcileRequest{
		Fresh: []Entry{{Title: "t", Summary: "s"}},
	}); err == nil {
		t.Error("an entry with an empty path was accepted")
	}
}

func TestCosineHandlesUnnormalizedVectors(t *testing.T) {
	if got := cosine([]float32{1, 1}, []float32{2, 2}); got < 0.9999 {
		t.Errorf("cosine of parallel unnormalized vectors = %v, want ≈1", got)
	}
	if got := cosine([]float32{1, 1}, []float32{-1, -0.01}); got >= 0 {
		t.Errorf("cosine of opposed vectors = %v, want negative", got)
	}
}

func TestCosineZeroVectorIsZero(t *testing.T) {
	if got := cosine([]float32{0, 0}, []float32{1, 1}); got != 0 {
		t.Errorf("cosine with a zero vector = %v, want 0 (not NaN)", got)
	}
	if got := cosine(nil, []float32{1}); got != 0 {
		t.Errorf("cosine with nil = %v, want 0", got)
	}
}

func TestSearchRespectsTopK(t *testing.T) {
	rt := newTestRetriever(t, newTestStore(t), nil, 5)
	fresh := make([]Entry, 0, 10)
	vecs := map[string][]float32{}
	for i := 0; i < 10; i++ {
		p := string(rune('a'+i)) + ".md"
		fresh = append(fresh, ent(p, "t", "x"))
		vecs[p] = []float32{1, float32(i) / 10}
	}
	reconcile(t, rt.config.Store, ReconcileRequest{Fresh: fresh})
	if err := rt.config.Store.SetVectors(vecs); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	if got := len(rt.search(parseQuery("x"), 3, []float32{1, 0}, nil)); got != 3 {
		t.Fatalf("TopK 3 returned %d hits", got)
	}
}

func TestSearchDuringReconcileIsSafe(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "seed")}})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Docs()
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "seed")}})
	}
	close(stop)
	wg.Wait()
}

func TestSidecarWrittenAfterCatalog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	t.Cleanup(func() { _ = s.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s, ReconcileRequest{
		Dim:   2,
		Fresh: []Entry{ent("a.md", "t", "s")},
	})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}
	for _, f := range []string{catalogFile, sidecarFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
	}
	s2, err := NewStore(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := s2.Sidecar().Dim; got != 2 {
		t.Errorf("reopened dim = %d, want 2", got)
	}
}

func TestCorruptSidecarSelfHeals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	t.Cleanup(func() { _ = s.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sidecarFile), []byte("not a gob"), 0o644); err != nil {
		t.Fatalf("corrupt sidecar: %v", err)
	}

	s2, err := NewStore(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("reopen with a corrupt sidecar must not fail: %v", err)
	}
	if got := len(s2.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1 (the catalog survives a broken sidecar)", got)
	}
	if got := len(s2.Sidecar().Entries); got != 0 {
		t.Fatalf("sidecar entries = %d, want 0 (it was cleared)", got)
	}
}

func TestSidecarIndexVersionMismatchClearsVectors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	t.Cleanup(func() { _ = s.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	if err := writeSidecarWithVersion(filepath.Join(dir, sidecarFile), IndexVersion+1); err != nil {
		t.Fatalf("rewrite sidecar: %v", err)
	}

	s2, err := NewStore(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(s2.Sidecar().Entries); got != 0 {
		t.Fatalf("vectors survived a version bump: %v", s2.Sidecar().Entries)
	}
	if got := len(s2.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1 (the catalog is untouched)", got)
	}
}

func TestCatalogIndexVersionMismatchRejects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := RenderCatalog(Catalog{
		Header: Header{IndexVersion: IndexVersion + 1, Writer: "deadbeef"},
	})
	if err := os.WriteFile(filepath.Join(dir, catalogFile), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewStore(dir)
	if err == nil {
		t.Fatal("a catalog from a newer extraction version was accepted")
	}
	if !strings.Contains(err.Error(), "re-run") {
		t.Errorf("error should say how to recover, got: %v", err)
	}
}

func TestHumanEditSurvivesReindex(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "机器写的摘要")}})

	cat := s.Catalog()
	for i := range cat.Entries {
		cat.Entries[i].Summary = "人写的摘要"
	}
	body := RenderCatalog(cat)
	if err := os.WriteFile(filepath.Join(s.Dir(), catalogFile), body, 0o644); err != nil {
		t.Fatalf("human edit: %v", err)
	}

	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "机器写的新摘要")}})

	got := s.Entries()
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if got[0].Summary != "人写的摘要" {
		t.Fatalf("the machine overwrote the human's summary: %q", got[0].Summary)
	}
	if got[0].Stale {
		t.Error("a human edit against an unchanged source must not be marked stale")
	}
}

func TestHumanEditMarksStaleWhenSourceChanges(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "机器写的摘要")}})

	cat := s.Catalog()
	for i := range cat.Entries {
		cat.Entries[i].Summary = "人写的摘要"
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), catalogFile), RenderCatalog(cat), 0o644); err != nil {
		t.Fatalf("human edit: %v", err)
	}

	reconcile(t, s, ReconcileRequest{Fresh: []Entry{{
		Path: "a.md", Title: "标题", Summary: "机器写的摘要", MTime: 99, Size: 99,
	}}})

	got := s.Entries()
	if got[0].Summary != "人写的摘要" {
		t.Fatalf("the machine overwrote the human's summary: %q", got[0].Summary)
	}
	if !got[0].Stale {
		t.Error("a human summary against a changed source must be marked stale")
	}

	cat = s.Catalog()
	for i := range cat.Entries {
		cat.Entries[i].Summary = "人改过第二次"
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), catalogFile), RenderCatalog(cat), 0o644); err != nil {
		t.Fatalf("second human edit: %v", err)
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{{
		Path: "a.md", Title: "标题", Summary: "机器写的摘要", MTime: 99, Size: 99,
	}}})

	got = s.Entries()
	if got[0].Summary != "人改过第二次" {
		t.Fatalf("summary = %q, want 人改过第二次", got[0].Summary)
	}
	if got[0].Stale {
		t.Error("a fresh human edit must clear the stale flag")
	}
}

func TestReconcileRemovesDeletedDocs(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{
		Fresh: []Entry{ent("a.md", "a", "a"), ent("b.md", "b", "b")},
	})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}, "b.md": {0, 1}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "a", "a")}})
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1", res.Removed)
	}
	if got := len(s.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
	if _, ok := s.Sidecar().Entries["b.md"]; ok {
		t.Error("the removed doc kept a orphan vector in the sidecar")
	}
}

func TestScopedReconcileKeepsUntouchedDocs(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{
		Fresh: []Entry{ent("a.md", "a", "a"), ent("b.md", "b", "b")},
	})

	reconcile(t, s, ReconcileRequest{
		Scope: []string{"a.md"},
		Fresh: []Entry{ent("a.md", "a", "改过的 a")},
	})
	got := s.Entries()
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 (b.md was not in scope)", len(got))
	}
	for _, e := range got {
		if e.Path == "a.md" && e.Summary != "改过的 a" {
			t.Errorf("a.md summary = %q, want 改过的 a", e.Summary)
		}
	}
}

func TestReconcileDetectsConcurrentWrite(t *testing.T) {
	s := newTestStore(t)
	md := filepath.Join(s.Dir(), catalogFile)

	var once sync.Once
	s.afterWrite = func() {
		once.Do(func() {
			done := make(chan error, 1)
			go func() {
				b, err := os.ReadFile(md)
				if err != nil {
					done <- err
					return
				}
				cat, err := ParseCatalog(b)
				if err != nil {
					done <- err
					return
				}
				cat.Entries[0].Summary = "人改的摘要"
				done <- os.WriteFile(md, RenderCatalog(cat), 0o644)
			}()
			if err := <-done; err != nil {
				t.Errorf("interference: %v", err)
			}
		})
	}

	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})

	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the conflicting write must be detected)", res.Attempts)
	}

	b, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), "人改的摘要") {
		t.Fatalf("the human's summary was overwritten by the retry:\n%s", b)
	}
}

func TestReconcileReportsAttempts(t *testing.T) {
	s := newTestStore(t)
	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	if res.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 on a clean run", res.Attempts)
	}
}

func TestReconcileCountsReEmbeddedVsUnchanged(t *testing.T) {
	s := newTestStore(t)
	md := filepath.Join(s.Dir(), catalogFile)

	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})
	if res.ReEmbedded != 1 || res.Unchanged != 0 {
		t.Fatalf("first index: re-embedded=%d unchanged=%d, want 1/0",
			res.ReEmbedded, res.Unchanged)
	}

	cat, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	parsed, err := ParseCatalog(cat)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	parsed.Entries[0].Summary = "人写的摘要"
	if err := os.WriteFile(md, RenderCatalog(parsed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})
	if res.ReEmbedded != 1 || res.Unchanged != 0 {
		t.Fatalf("human summary edit: re-embedded=%d unchanged=%d, want 1/0",
			res.ReEmbedded, res.Unchanged)
	}
	if got := s.Entries()[0].Summary; got != "人写的摘要" {
		t.Fatalf("human summary = %q, want it kept", got)
	}

	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})
	if res.ReEmbedded != 0 || res.Unchanged != 1 {
		t.Fatalf("no-op re-run: re-embedded=%d unchanged=%d, want 0/1",
			res.ReEmbedded, res.Unchanged)
	}

	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{
		{Path: "a.md", Title: "标题", Summary: "正文", MTime: 99, Size: 4242},
	}})
	if res.ReEmbedded != 0 || res.Unchanged != 1 {
		t.Fatalf("mtime-only change: re-embedded=%d unchanged=%d, want 0/1",
			res.ReEmbedded, res.Unchanged)
	}

	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{
		{Path: "a.md", Title: "标题", Summary: "正文", Sections: []Section{{Title: "新章", Line: 1}}},
	}})
	if res.ReEmbedded != 1 || res.Unchanged != 0 {
		t.Fatalf("text change: re-embedded=%d unchanged=%d, want 1/0",
			res.ReEmbedded, res.Unchanged)
	}
}

func TestStoreRefreshesFromOtherProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s1, err := NewStore(dir)
	t.Cleanup(func() { _ = s1.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s1, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})

	s2, err := NewStore(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	reconcile(t, s2, ReconcileRequest{
		Fresh: []Entry{ent("b.md", "t", "s")},
		Scope: []string{"b.md"},
	})

	if got := len(s1.Entries()); got != 2 {
		t.Fatalf("first store saw %d entries, want 2 (it must notice the other process)", got)
	}
}

func TestStartRunIsExclusive(t *testing.T) {
	s := newTestStore(t)
	other, err := NewStore(s.Dir())
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	defer other.Close()

	stop, err := s.StartRun()
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := other.StartRun(); err == nil {
		t.Fatal("a second instance acquired the run lock")
	}
	stop()
	if _, err := other.StartRun(); err != nil {
		t.Fatalf("StartRun after release: %v", err)
	}
}

func TestStoreCloseReleasesRunLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	t.Cleanup(func() { _ = s.Close() })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.StartRun(); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := NewStore(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s2.StartRun(); err != nil {
		t.Fatalf("a stale run lock survived Close: %v", err)
	}
}

func TestDerOfFeedsIncrement(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})

	de, ok := s.derOf("a.md")
	if !ok {
		t.Fatal("derOf missed a reconciled path")
	}
	if de.EmbedText == "" {
		t.Error("the sidecar recorded no EmbedText; incremental detection would never fire")
	}
	if de.WroteSummary != "正文" {
		t.Errorf("WroteSummary = %q, want 正文 (it is how human edits are detected)", de.WroteSummary)
	}
	if de.ObsSummary != "正文" {
		t.Errorf("ObsSummary = %q, want 正文 (it is how staleness is cleared)", de.ObsSummary)
	}
}

func TestOwnsHuman(t *testing.T) {
	cases := []struct {
		current, wrote string
		want           bool
	}{
		{"", "", false},
		{"a", "", true},
		{"a", "a", false},
		{"b", "a", true},
		{"", "a", false},
	}
	for _, c := range cases {
		if got := ownsHuman(c.current, c.wrote); got != c.want {
			t.Errorf("ownsHuman(%q, %q) = %v, want %v", c.current, c.wrote, got, c.want)
		}
	}
}

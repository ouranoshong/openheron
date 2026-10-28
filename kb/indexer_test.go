package kb

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func newTestIndexer(t *testing.T, st *Store, emb *fakeEmbedder, batch int) *FileIndexer {
	t.Helper()
	cfg := &IndexerConfig{Store: st, BatchSize: batch}
	if emb != nil {
		cfg.Embedding = emb
	}
	ix, err := NewIndexer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	return ix
}

func docsOf(entries ...Entry) []*schema.Document {
	out := make([]*schema.Document, 0, len(entries))
	for _, e := range entries {
		out = append(out, newDocOf(e))
	}
	return out
}

func TestIndexerActuallyBatches(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix, err := NewIndexer(context.Background(), &IndexerConfig{
		Store: st, Embedding: emb, BatchSize: 4, MaxBatchChars: 3,
	})
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}

	var entries []Entry
	for i := 0; i < 10; i++ {
		entries = append(entries, ent(string(rune('a'+i))+".md", "标题", "文本"))
	}
	ids, err := ix.Store(context.Background(), docsOf(entries...))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if len(ids) != 10 {
		t.Fatalf("ids = %d, want 10", len(ids))
	}
	got := emb.batches()
	want := make([]int, 10)
	for i := range want {
		want[i] = 1
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

func TestIndexerBatchesByCount(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix := newTestIndexer(t, st, emb, 4)

	var entries []Entry
	for i := 0; i < 10; i++ {
		entries = append(entries, ent(string(rune('a'+i))+".md", "标题", "文本"))
	}
	if _, err := ix.Store(context.Background(), docsOf(entries...)); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got := emb.batches()
	want := []int{4, 4, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

func TestIndexerStoresVectorInSidecar(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix := newTestIndexer(t, st, emb, 10)

	if _, err := ix.Store(context.Background(), docsOf(ent("x.md", "配置写入", "走原子写"))); err != nil {
		t.Fatalf("Store: %v", err)
	}

	de, ok := st.derOf("x.md")
	if !ok {
		t.Fatal("x.md is missing from the sidecar")
	}
	if len(de.Vector) != 8 {
		t.Errorf("vector len = %d, want 8", len(de.Vector))
	}
	cat := st.Catalog()
	e, ok := cat.Get("x.md")
	if !ok {
		t.Fatal("x.md missing from the catalog")
	}
	if e.Title != "配置写入" {
		t.Errorf("title = %q, want 配置写入", e.Title)
	}
}

func TestIndexerIsIdempotent(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix := newTestIndexer(t, st, emb, 10)
	docs := docsOf(ent("x.md", "同一条", "同一段"))

	for i := 0; i < 3; i++ {
		if _, err := ix.Store(context.Background(), docs); err != nil {
			t.Fatalf("Store #%d: %v", i, err)
		}
	}
	if got := len(st.Entries()); got != 1 {
		t.Fatalf("entries = %d after storing the same id three times, want 1", got)
	}
}

func TestIndexerRejectsDocWithoutEntry(t *testing.T) {
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)

	_, err := ix.Store(context.Background(), []*schema.Document{
		{ID: "x.md", Content: "只有正文没有 Entry", MetaData: map[string]any{}},
	})
	if err == nil {
		t.Fatal("a doc with no Entry in metadata was accepted")
	}
	if !strings.Contains(err.Error(), "kb_entry") {
		t.Errorf("error should name the missing key, got: %v", err)
	}
}

func TestIndexerRejectsMismatchedPath(t *testing.T) {
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)

	d := newDocOf(ent("a.md", "t", "s"))
	d.ID = "b.md"
	if _, err := ix.Store(context.Background(), []*schema.Document{d}); err == nil {
		t.Fatal("a doc whose ID differs from its entry path was accepted")
	} else if !strings.Contains(err.Error(), "must equal") {
		t.Errorf("error should explain the constraint, got: %v", err)
	}
}

func TestIndexerRejectsEmbeddingFailure(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(4)
	emb.failWhen = func(int) error { return errBoom }
	ix := newTestIndexer(t, st, emb, 10)

	if _, err := ix.Store(context.Background(), docsOf(ent("x.md", "a", "b"))); err == nil {
		t.Fatal("Store succeeded despite the embedder failing")
	}
	if got := len(st.Entries()); got != 0 {
		t.Fatalf("catalog has %d entries after a failed Store, want 0", got)
	}
}

func TestIndexerRejectsDimMismatch(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetIdentity("m1", 4); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)
	if _, err := ix.Store(context.Background(), docsOf(ent("a.md", "t", "s"))); err != nil {
		t.Fatalf("Store: %v", err)
	}

	if err := st.SetIdentity("m1", 8); err == nil {
		t.Fatal("a different dim was accepted")
	} else if !strings.Contains(err.Error(), "delete") {
		t.Errorf("error should tell the user how to recover, got: %v", err)
	}
}

func TestHumanSummaryEditReEmbeds(t *testing.T) {
	_, corpus := seedProject(t, map[string]string{
		"a.md": "# 标题\n\n## 一节\n\n正文在这里。\n",
	})
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix := newTestIndexer(t, st, emb, 10)
	ctx := context.Background()

	if _, err := Index(ctx, ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	v1 := append([]float32(nil), st.Sidecar().Entries["a.md"].Vector...)
	if len(v1) == 0 {
		t.Fatal("the first pass produced no vector")
	}

	res, err := Index(ctx, ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("no-op Index: %v", err)
	}
	if res.ReEmbedded != 0 || res.Unchanged != 1 {
		t.Fatalf("no-op re-run: re-embedded=%d unchanged=%d, want 0/1",
			res.ReEmbedded, res.Unchanged)
	}

	md := filepath.Join(st.Dir(), catalogFile)
	b, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("read %s: %v", md, err)
	}
	cat, err := ParseCatalog(b)
	if err != nil {
		t.Fatalf("parse %s: %v", md, err)
	}
	cat.Entries[0].Summary = "人写的摘要"
	if err := os.WriteFile(md, RenderCatalog(cat), 0o644); err != nil {
		t.Fatalf("write %s: %v", md, err)
	}

	res, err = Index(ctx, ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("Index after the edit: %v", err)
	}
	if res.Changed != 0 {
		t.Errorf("changed = %d, want 0 (the source file was not touched)", res.Changed)
	}
	if res.ReEmbedded != 1 {
		t.Errorf("re-embedded = %d, want 1 (a human edited the summary, so it must be re-embedded)", res.ReEmbedded)
	}

	texts := emb.embeds()
	if len(texts) != 2 {
		t.Fatalf("sent %d texts in total, want 2 (one for the first pass, one after the summary edit)", len(texts))
	}
	if !strings.Contains(texts[1], "人写的摘要") {
		t.Errorf("the text sent to the vectorizer is the machine-written one, not the human-written one — "+
			"the catalog shows the human summary but the ranking uses the machine one:\n%q", texts[1])
	}

	if sameVector(v1, st.Sidecar().Entries["a.md"].Vector) {
		t.Error("the summary changed but the vector did not — it still embedded the machine-written one")
	}
}

func TestNoOpTouchDoesNotReEmbed(t *testing.T) {
	_, corpus := seedProject(t, map[string]string{
		"a.md": "# 标题\n\n## 一节\n\n正文在这里。\n",
	})
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix := newTestIndexer(t, st, emb, 10)
	ctx := context.Background()

	if _, err := Index(ctx, ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	path := filepath.Join(corpus, "a.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	res, err := Index(ctx, ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("changed = %d, want 1 (the mtime really changed)", res.Changed)
	}
	if res.ReEmbedded != 0 {
		t.Errorf("re-embedded = %d, want 0 — not a single character of the text changed, re-embedding is pure waste", res.ReEmbedded)
	}
}

func TestNewIndexerValidatesConfig(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(4)

	if _, err := NewIndexer(context.Background(), &IndexerConfig{Store: st}); err != nil {
		t.Errorf("nil embedding rejected, but it must be optional: %v", err)
	}
	if _, err := NewIndexer(context.Background(), &IndexerConfig{Embedding: emb}); err == nil {
		t.Error("nil store accepted")
	}
	if _, err := NewIndexer(context.Background(), nil); err == nil {
		t.Error("nil config accepted")
	}
	if _, err := NewIndexer(context.Background(), &IndexerConfig{
		Store: st, Embedding: emb, BatchSize: -1,
	}); err == nil {
		t.Error("negative batch size accepted")
	}
	if _, err := NewIndexer(context.Background(), &IndexerConfig{
		Store: st, Embedding: emb, MaxBatchChars: -1,
	}); err == nil {
		t.Error("negative max batch chars accepted")
	}
}

func TestIndexerWritesIdentity(t *testing.T) {
	st := newTestStore(t)
	byModel(t, st, "local/Qwen3-Embedding-0.6B-GGUF")
	ix := newTestIndexer(t, st, newFakeEmbedder(16), 10)

	if _, err := ix.Store(context.Background(), docsOf(ent("x.md", "a", "b"))); err != nil {
		t.Fatalf("Store: %v", err)
	}
	der := st.Sidecar()
	if der.Model != "local/Qwen3-Embedding-0.6B-GGUF" {
		t.Errorf("sidecar model = %q", der.Model)
	}
	if der.Dim != 16 {
		t.Errorf("sidecar dim = %d, want 16", der.Dim)
	}
}

func TestReconcileWithoutEmbeddingIssuesNoRequests(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix := newTestIndexer(t, st, nil, 10)

	if _, err := ix.Reconcile(context.Background(), ReconcileRequest{
		Fresh: []Entry{ent("a.md", "标题", "正文")},
	}, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := emb.batches(); len(got) != 0 {
		t.Fatalf("lexical-only mode issued %d embedding requests: %v", len(got), got)
	}
	if got := len(st.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
}

var errBoom = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }

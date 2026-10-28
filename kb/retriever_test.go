package kb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

func newTestRetriever(t *testing.T, st *Store, emb *fakeEmbedder, topK int) *FileRetriever {
	t.Helper()
	cfg := &RetrieverConfig{Store: st, TopK: topK}
	if emb != nil {
		cfg.Embedding = emb
	}
	rt, err := NewRetriever(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	return rt
}

const (
	docHot  = "热更新 先装配成功 再发布 运行实例"
	docCold = "会话历史 归档 checkpoint 落库"
)

func seedProject(t *testing.T, files map[string]string) (root, corpus string) {
	t.Helper()
	root = t.TempDir()
	corpus = filepath.Join(root, "docs")
	for name, body := range files {
		p := filepath.Join(corpus, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		touch(t, p, 1_700_000_000)
	}
	return root, corpus
}

func seededStore(t *testing.T) (*Store, *fakeEmbedder) {
	t.Helper()
	_, corpus := seedProject(t, map[string]string{
		"a.md": "# 热更新\n\n## 发布\n\n" + docHot + "\n",
		"b.md": "# 归档\n\n## 落库\n\n" + docCold + "\n",
	})
	st := newTestStore(t)
	emb := newFakeEmbedder(64)
	ix := newTestIndexer(t, st, emb, 10)
	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st, emb
}

func TestRetrieveRanksRelevantFirst(t *testing.T) {
	st, emb := seededStore(t)
	rt := newTestRetriever(t, st, emb, 5)

	docs, err := rt.Retrieve(context.Background(), "热更新 装配 成功 发布")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2 (one per source file)", len(docs))
	}
	seenB := false
	for _, d := range docs {
		if strings.HasPrefix(d.ID, "b.md") {
			seenB = true
			continue
		}
		if seenB {
			t.Fatalf("a chunk from b.md ranked above one from a.md: %v", idsOf(docs))
		}
	}
	for i := 1; i < len(docs); i++ {
		if docs[i-1].Score() < docs[i].Score() {
			t.Fatalf("scores are not descending at %d: %v then %v", i, docs[i-1].Score(), docs[i].Score())
		}
	}
}

func TestRetrieveRespectsTopK(t *testing.T) {
	_, corpus := seedProject(t, map[string]string{
		"a.md": "# 热更新\n\n## 发布\n\n" + docHot + "\n",
		"b.md": "# 归档\n\n## 落库\n\n" + docCold + "\n",
		"c.md": "# 会话\n\n## 存储\n\n会话历史 归档 checkpoint 落库 恢复\n",
	})
	st := newTestStore(t)
	emb := newFakeEmbedder(64)
	ix := newTestIndexer(t, st, emb, 10)
	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rt := newTestRetriever(t, st, emb, 1)

	docs, err := rt.Retrieve(context.Background(), "热更新")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}

	docs, err = rt.Retrieve(context.Background(), "热更新", retriever.WithTopK(3))
	if err != nil {
		t.Fatalf("Retrieve with WithTopK(3): %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("WithTopK(3) returned %d docs, want 3", len(docs))
	}
}

func directStore(t *testing.T) (*Store, *fakeEmbedder) {
	t.Helper()
	st := newTestStore(t)
	emb := newFakeEmbedder(64)
	ix := newTestIndexer(t, st, emb, 10)
	if _, err := ix.Store(context.Background(), []*schema.Document{
		newDoc("hot", docHot, nil),
		newDoc("cold", docCold, nil),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st, emb
}

func TestRetrieveReturnsDenseVectorAndScore(t *testing.T) {
	st, emb := seededStore(t)
	rt := newTestRetriever(t, st, emb, 5)

	docs, err := rt.Retrieve(context.Background(), "热更新")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	v := docs[0].DenseVector()
	if len(v) != 64 {
		t.Errorf("dense vector len = %d, want 64", len(v))
	}
	if docs[0].Score() <= 0 {
		t.Errorf("score = %v, want > 0", docs[0].Score())
	}
	if docs[0].Content == "" {
		t.Error("content is empty")
	}
}

func TestRetrieveRejectsUnknownReturnField(t *testing.T) {
	st, emb := seededStore(t)
	rt, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: emb, ReturnFields: []string{FieldContent, "nope"},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	if _, err := rt.Retrieve(context.Background(), "热更新"); err == nil {
		t.Fatal("Retrieve accepted a ReturnFields entry that no record carries")
	}
}

func TestRetrieverRejectsIndexDirMismatch(t *testing.T) {
	st := newTestStore(t)
	if _, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: newFakeEmbedder(4), Index: "not-the-dir",
	}); err == nil {
		t.Fatal("a mismatched Index was accepted")
	}

	ok, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: newFakeEmbedder(4), Index: filepath.Base(st.Dir()),
	})
	if err != nil {
		t.Fatalf("matching Index rejected: %v", err)
	}
	if ok.config.Index != filepath.Base(st.Dir()) {
		t.Errorf("Index = %q", ok.config.Index)
	}
}

func TestNewRetrieverValidatesConfig(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(4)
	if _, err := NewRetriever(context.Background(), &RetrieverConfig{Store: st}); err != nil {
		t.Errorf("nil embedding rejected (v2 allows lexical-only retrieval): %v", err)
	}
	if _, err := NewRetriever(context.Background(), &RetrieverConfig{Embedding: emb}); err == nil {
		t.Error("nil store accepted")
	}
	if _, err := NewRetriever(context.Background(), nil); err == nil {
		t.Error("nil config accepted")
	}
	if _, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: emb, TopK: -1,
	}); err == nil {
		t.Error("negative TopK accepted")
	}
}

func TestRetrieveOnEmptyIndexReturnsNothing(t *testing.T) {
	st := newTestStore(t)
	rt := newTestRetriever(t, st, newFakeEmbedder(4), 5)
	docs, err := rt.Retrieve(context.Background(), "任意查询")
	if err != nil {
		t.Fatalf("Retrieve on an empty index: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("got %d docs from an empty index", len(docs))
	}
}

func TestGetTypeIsFile(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(4)
	ix := newTestIndexer(t, st, emb, 10)
	rt := newTestRetriever(t, st, emb, 5)

	if ix.GetType() != "File" || rt.GetType() != "File" {
		t.Errorf("GetType = %q / %q, want File", ix.GetType(), rt.GetType())
	}
	if !ix.IsCallbacksEnabled() || !rt.IsCallbacksEnabled() {
		t.Error("callbacks should be enabled, same as the Redis components")
	}
}

func ptr[T any](v T) *T { return &v }

func idsOf(docs []*schema.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}

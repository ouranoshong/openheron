package kb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testIndexOpts(corpus string) IndexOptions {
	return IndexOptions{
		CorpusDir: corpus,
		Headers:   map[string]string{"##": "section", "###": "subsection"},
	}
}

func seedCorpus(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		touch(t, p, 1_700_000_000)
	}
	return dir
}

func touch(t *testing.T, p string, unix int64) {
	t.Helper()
	when := time.Unix(unix, 0)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

const twoDocs = `# 指南

前言段落。

## 热更新

先装配成功再发布运行实例。

### 为什么

装配失败时进程会对外报告一份它并未运行的配置。

## 会话

双文件加 checkpoint。
`

func TestIndexFullCorpus(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{
		"a.md": twoDocs,
		"b.md": "# B\n\n## 只有一节\n\n内容。\n",
	})
	st := newTestStore(t)
	emb := newFakeEmbedder(32)
	byModel(t, st, "local/test")
	ix := newTestIndexer(t, st, emb, 10)

	res, err := Index(context.Background(), ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Changed != 2 || res.Skipped != 0 {
		t.Errorf("changed=%d skipped=%d, want 2/0", res.Changed, res.Skipped)
	}
	if res.Files != 2 {
		t.Errorf("files = %d, want 2 (one per source file)", res.Files)
	}
	if res.Total != len(st.Entries()) {
		t.Errorf("result total %d != store entries %d", res.Total, len(st.Entries()))
	}

	der := st.Sidecar()
	if der.Model != "local/test" {
		t.Errorf("sidecar model = %q", der.Model)
	}
	if der.Dim != 32 {
		t.Errorf("sidecar dim = %d, want 32", der.Dim)
	}
	entries := st.Entries()
	if entries[0].Path != "a.md" || entries[1].Path != "b.md" {
		t.Errorf("entries not sorted by path: %v", entries)
	}
}

func TestIndexCarriesSectionLineNumbers(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("Index: %v", err)
	}
	cat := st.Catalog()
	e, ok := cat.Get("a.md")
	if !ok {
		t.Fatal("a.md is missing from the catalog")
	}
	found := map[string]int{}
	for _, s := range e.Sections {
		found[s.Title] = s.Line
	}
	for _, want := range []string{"热更新", "为什么", "会话"} {
		line, ok := found[want]
		if !ok {
			t.Fatalf("section %q missing: %v", want, e.Sections)
		}
		if line <= 0 {
			t.Errorf("section %q has line %d; every section must carry a line number", want, line)
		}
	}
	body, err := os.ReadFile(filepath.Join(corpus, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLinesForTest(string(body))
	if got := lines[found["热更新"]-1]; got != "## 热更新" {
		t.Errorf("line %d = %q, want ## 热更新", found["热更新"], got)
	}
}

func TestIndexSkipsUnchangedFiles(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs, "b.md": "# B\n\n## 一\n\nx\n"})
	st := newTestStore(t)
	emb := newFakeEmbedder(32)
	ix := newTestIndexer(t, st, emb, 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("first index: %v", err)
	}
	before := emb.batches()

	res, err := Index(context.Background(), ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if res.Changed != 0 || res.Skipped != 2 {
		t.Errorf("changed=%d skipped=%d, want 0/2", res.Changed, res.Skipped)
	}
	if got := len(emb.batches()); got != len(before) {
		t.Errorf("the second run issued %d embedding requests; it must issue none", got-len(before))
	}
}

func TestIndexReindexesOnlyChangedFile(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs, "b.md": "# B\n\n## 一\n\nx\n"})
	st := newTestStore(t)
	emb := newFakeEmbedder(32)
	ix := newTestIndexer(t, st, emb, 10)
	opts := testIndexOpts(corpus)

	if _, err := Index(context.Background(), ix, opts, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}

	if err := os.WriteFile(filepath.Join(corpus, "b.md"), []byte("# B\n\n## 改过了\n\n新内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(corpus, "b.md"), 1_700_000_999)

	res, err := Index(context.Background(), ix, opts, nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if res.Changed != 1 || res.Skipped != 1 {
		t.Errorf("changed=%d skipped=%d, want 1/1", res.Changed, res.Skipped)
	}
	if res.Removed != 0 {
		t.Errorf("removed=%d, want 0 (b.md is still there)", res.Removed)
	}
}

func TestIndexDropsDeletedFiles(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{
		"a.md": twoDocs,
		"b.md": "# B\n\n## 一节\n\n会被删掉的独特内容 zebra\n",
	})
	st := newTestStore(t)
	emb := newFakeEmbedder(32)
	ix := newTestIndexer(t, st, emb, 10)
	opts := testIndexOpts(corpus)

	if _, err := Index(context.Background(), ix, opts, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}
	before := len(st.Entries())

	if err := os.Remove(filepath.Join(corpus, "b.md")); err != nil {
		t.Fatal(err)
	}
	res, err := Index(context.Background(), ix, opts, nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1", res.Removed)
	}
	if len(st.Entries()) >= before {
		t.Fatalf("entries = %d, want fewer than %d after deleting a file", len(st.Entries()), before)
	}
	if e := st.Entries(); len(e) != 1 || e[0].Path != "a.md" {
		t.Errorf("catalog still lists the deleted file: %v", e)
	}
	if _, ok := st.Sidecar().Entries["b.md"]; ok {
		t.Error("the deleted doc kept an orphan vector in the sidecar")
	}
	s2, err := NewStore(st.Dir())
	t.Cleanup(func() { _ = s2.Close() })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(s2.Entries()) != len(st.Entries()) {
		t.Errorf("after reopen entries = %d, want %d", len(s2.Entries()), len(st.Entries()))
	}
}

func TestIndexSelectedPathsOnly(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs, "b.md": "# B\n\n## 一\n\nx\n"})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("full index: %v", err)
	}
	res, err := Index(context.Background(), ix, testIndexOpts(corpus), []string{"a.md"})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Changed != 0 {
		t.Errorf("changed = %d, want 0 (a.md was untouched)", res.Changed)
	}
	if got := len(st.Entries()); got != 2 {
		t.Errorf("entries = %d, want 2 (b.md was not in scope, so it stays)", got)
	}
}

func TestIndexIsIdempotent(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)
	opts := testIndexOpts(corpus)

	var first *Result
	for i := 0; i < 3; i++ {
		res, err := Index(context.Background(), ix, opts, nil)
		if err != nil {
			t.Fatalf("run #%d: %v", i, err)
		}
		if first == nil {
			first = res
			continue
		}
		if res.Total != first.Total {
			t.Fatalf("run #%d total = %d, want %d", i, res.Total, first.Total)
		}
	}
}

func TestIndexMissingCorpusIsNotAnError(t *testing.T) {
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)
	opts := testIndexOpts(filepath.Join(t.TempDir(), "nope"))

	res, err := Index(context.Background(), ix, opts, nil)
	if err != nil {
		t.Fatalf("a missing corpus directory must not be an error: %v", err)
	}
	if res.Changed != 0 || res.Files != 0 {
		t.Errorf("res = %+v, want all zeros", res)
	}
}

func TestIndexCorpusMustBeADirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(p), nil); err == nil {
		t.Fatal("a corpus that is a file was accepted")
	}
}

func TestIndexIgnoresNonMarkdownAndDotDirs(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{
		"a.md":       twoDocs,
		"notes.txt":  "不该被索引",
		".git/x.md":  "更不该",
		"sub/b/c.md": "子目录里的也算",
	})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("Index: %v", err)
	}
	paths := map[string]bool{}
	for _, e := range st.Entries() {
		paths[e.Path] = true
	}
	if !paths["a.md"] || !paths["sub/b/c.md"] {
		t.Errorf("catalog = %v, want a.md and sub/b/c.md", paths)
	}
	if paths["notes.txt"] {
		t.Error("a .txt file was indexed; the extraction rules only make sense for markdown")
	}
	if paths[".git/x.md"] {
		t.Error("a file under a dot directory was indexed")
	}
}

func TestIndexDropsSectionsOfAShrunkFile(t *testing.T) {
	long := "# A\n\n## 一\n\n内容一\n\n## 二\n\n内容二\n\n## 三\n\n内容三\n"
	short := "# A\n\n## 一\n\n内容一\n"
	root := t.TempDir()
	corpus := filepath.Join(root, "docs")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(corpus, "a.md")
	if err := os.WriteFile(p, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, p, 1_700_000_000)

	st := newTestStore(t)
	byModel(t, st, "local/test")
	ix := newTestIndexer(t, st, newFakeEmbedder(16), 10)
	opts := testIndexOpts(corpus)

	if _, err := Index(context.Background(), ix, opts, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}
	before := len(st.Entries()[0].Sections)
	if before < 3 {
		t.Fatalf("long version produced %d sections; the test needs several to shrink", before)
	}

	if err := os.WriteFile(p, []byte(short), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, p, 1_700_000_999)
	if _, err := Index(context.Background(), ix, opts, nil); err != nil {
		t.Fatalf("second index: %v", err)
	}

	after := len(st.Entries()[0].Sections)
	if after >= before {
		t.Fatalf("sections = %d after shrinking a file from %d; stale sections were kept", after, before)
	}
	de, ok := st.derOf("a.md")
	if !ok {
		t.Fatal("a.md is missing from the sidecar")
	}
	if de.EmbedText != st.Entries()[0].EmbedText() {
		t.Errorf("the sidecar's EmbedText is stale:\n sidecar: %q\n catalog: %q",
			de.EmbedText, st.Entries()[0].EmbedText())
	}
}

func TestIndexWithoutEmbeddingStillWritesCatalog(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, nil, 10)

	res, err := Index(context.Background(), ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("Index without an embedder: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("files = %d, want 1", res.Files)
	}
	if got := len(st.Entries()); got != 1 {
		t.Fatalf("catalog entries = %d, want 1", got)
	}
	if got := st.Sidecar().Dim; got != 0 {
		t.Errorf("dim = %d, want 0 in lexical-only mode", got)
	}
	if got := countDocs(st); got != 0 {
		t.Errorf("vectors = %d, want 0", got)
	}
}

func countDocs(st *Store) int {
	n := 0
	for _, d := range st.Docs() {
		if len(d.Vector) > 0 {
			n++
		}
	}
	return n
}

func TestIndexRejectsModelSwitch(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	byModel(t, st, "model-a")
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("first index: %v", err)
	}
	if err := st.SetIdentity("model-b", 0); err == nil {
		t.Fatal("switching the embedding model was accepted")
	}
}

func TestIndexAcceptsButIgnoresHeaders(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	with := testIndexOpts(corpus)
	with.Headers = map[string]string{"##": "totally-different-key"}
	if _, err := Index(context.Background(), ix, with, nil); err != nil {
		t.Fatalf("Index with headers set: %v", err)
	}
	cat := st.Catalog()
	e, ok := cat.Get("a.md")
	if !ok {
		t.Fatal("a.md missing")
	}
	if len(e.Sections) < 3 {
		t.Errorf("sections = %v, want the line-scanned ones regardless of Headers", e.Sections)
	}
}

func splitLinesForTest(body string) []string {
	var out []string
	start := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			out = append(out, body[start:i])
			start = i + 1
		}
	}
	if start < len(body) {
		out = append(out, body[start:])
	}
	return out
}

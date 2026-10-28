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
		Corpus:  corpus,
		Headers: map[string]string{"##": "section", "###": "subsection"},
	}
}

// corpus 写一组 markdown，并把它们的时间戳钉死 —— 增量判定看 mtime，
// 不钉死的话测试之间会随机地互相「变」。
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

// ★ 全量建索引的验收之一：文档数、侧车身份、来源元数据都要对。
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
	// ★ **一个源文件 = 一条文档**（§14）。v1 那里是「至少 3 块」——
	//   v2 的单元变了，这条断言就是那次变化最直接的证据。
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
	// 稳定顺序：同样的语料两次索引出来的目录必须逐字节相同（除 updated 外）。
	entries := st.Entries()
	if entries[0].Path != "a.md" || entries[1].Path != "b.md" {
		t.Errorf("entries not sorted by path: %v", entries)
	}
}

// ★ 章节与行号要落进目录 —— §14.1「章节一律带行号」。
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
	// 行号必须指向真正的标题行 —— 指错了比不指更糟：模型会去读错的地方。
	body, err := os.ReadFile(filepath.Join(corpus, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLinesForTest(string(body))
	if got := lines[found["热更新"]-1]; got != "## 热更新" {
		t.Errorf("line %d = %q, want ## 热更新", found["热更新"], got)
	}
}

// ★ 没有变就一条都别重切。这条不只是省时间：全量索引从 10 分钟降到十几秒，
// 靠的就是它。
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

	// 只改 b，且**大小也变** —— mtime 与 size 任一变化都要被认出来。
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
		t.Errorf("removed=%d, want 0 (b.md 还在)", res.Removed)
	}
}

// ★ 删掉的源文件必须从目录里消失。不清的后果是「删了一整份文档它仍然能被
//
//	检索到」—— 自信的、无声的过期答案。
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
	// ★ 向量也要跟着删掉，否则侧车里留着一份孤儿。
	if _, ok := st.Sidecar().Entries["b.md"]; ok {
		t.Error("the deleted doc kept an orphan vector in the sidecar")
	}
	// 重开一次索引，确认删除真的落盘了而不是只在内存里。
	s2, err := NewStore(st.Dir())
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

	// 先把 b 也建进去，再只增量 a —— 那时 b 必须**还在**（它不在 scope 里）。
	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("full index: %v", err)
	}
	res, err := Index(context.Background(), ix, testIndexOpts(corpus), []string{"a.md"})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Changed != 0 {
		// a.md 没变，所以增量它也不该重抽。
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

// ★ 语料目录不存在不是错误 —— 那是「配了但还没建索引」，与「配错了」是两种
//
//	状态，不该让 `openheron kb index` 崩掉。
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

// ★ 文档**变短**时，多出来的那些章节必须消失。
//
//	删除清扫只处理「整个文件没了」，管不到「文件瘦了」—— 而两者的后果一模
//	一样：一个从三节删到一节的文档，后两节还留在目录里、还答得出来。
//	这是最贵的那类错：自信的、无声的过期答案。
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
	// ★ 侧车里的 EmbedText 必须跟上 —— 否则下一轮增量会以为它没变。
	de, ok := st.derOf("a.md")
	if !ok {
		t.Fatal("a.md is missing from the sidecar")
	}
	if de.EmbedText != st.Entries()[0].EmbedText() {
		t.Errorf("the sidecar's EmbedText is stale:\n sidecar: %q\n catalog: %q",
			de.EmbedText, st.Entries()[0].EmbedText())
	}
}

// ★ 无嵌入服务时索引照常写目录（§8.5）。这不是降级模式 —— 词法那一路
// 真的能用。
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
	// ★ 没有向量是**预期**的，而它必须能被看出来 —— 否则「为什么查不到」
	//   会变成一个查不出答案的问题。
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

// ★ 换了 embedding 模型 ⇒ 拒绝写，而不是悄悄混进两套不具可比性的向量。
func TestIndexRejectsModelSwitch(t *testing.T) {
	corpus := seedCorpus(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	byModel(t, st, "model-a")
	ix := newTestIndexer(t, st, newFakeEmbedder(32), 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("first index: %v", err)
	}
	// 装配层会用新模型的名字再 SetIdentity 一次 —— 那一步就该报错。
	if err := st.SetIdentity("model-b", 0); err == nil {
		t.Fatal("switching the embedding model was accepted")
	}
}

// ★ Headers 在 v2 起不生效但仍接受（§14）。配了不该报错，但也不该假装它
// 还在起作用。
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
	// ★ 章节仍然由行扫描产出，与 Headers 无关 —— 那正是「不生效」的意思。
	if len(e.Sections) < 3 {
		t.Errorf("sections = %v, want the line-scanned ones regardless of Headers", e.Sections)
	}
}

// splitLinesForTest 是测试里读文件行的小工具，避免与包内的 splitLines 混淆。
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

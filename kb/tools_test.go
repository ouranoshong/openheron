package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func mustSearchTool(t *testing.T, st *Store, emb *fakeEmbedder, topK int) searchInvoker {
	t.Helper()
	rt, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: emb, TopK: topK, ReturnFields: ResultFields,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	tl, err := NewSearchTool(SearchConfig{Retriever: rt, TopK: topK})
	if err != nil {
		t.Fatalf("NewSearchTool: %v", err)
	}
	return tl
}

func mustIndexTool(t *testing.T, st *Store, ix *FileIndexer, corpus, projectRoot string) toolInvoker {
	t.Helper()
	tl, err := NewIndexTool(IndexToolConfig{
		Store:       st,
		Indexer:     ix,
		CorpusDir:   corpus,
		ProjectRoot: projectRoot,
		Headers:     map[string]string{"##": "section", "###": "subsection"},
	})
	if err != nil {
		t.Fatalf("NewIndexTool: %v", err)
	}
	return tl
}

type searchInvoker = tool.InvokableTool
type toolInvoker = tool.InvokableTool

func TestToolNamesAreVerbs(t *testing.T) {
	if ToolSearch != "kb_search" || ToolIndex != "kb_index" {
		t.Fatalf("tool names = %q / %q", ToolSearch, ToolIndex)
	}
	for _, n := range []string{ToolSearch, ToolIndex} {
		if strings.HasSuffix(n, "er") {
			t.Errorf("%q ends in -er: that reads as a component (the implementation's name), "+
				"not as an action the model wants to take", n)
		}
	}
}

func TestSearchToolDescribesNaturalLanguage(t *testing.T) {
	st := newTestStore(t)
	tl := mustSearchTool(t, st, newFakeEmbedder(8), 5)
	info, err := tl.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	for _, want := range []string{"natural-language", "keyword"} {
		if !strings.Contains(info.Desc, want) {
			t.Errorf("tool description should mention %q so the model stops sending keywords:\n%s",
				want, info.Desc)
		}
	}
	if info.Name != ToolSearch {
		t.Errorf("tool name = %q", info.Name)
	}
	if info.ParamsOneOf == nil {
		t.Fatal("tool has no parameter schema")
	}
}

// ── kb_search ────────────────────────────────────────────────────────────

func TestSearchToolRendersSourceAndSections(t *testing.T) {
	st, emb := seededStore(t)
	tl := mustSearchTool(t, st, emb, 5)

	out, err := tl.InvokableRun(context.Background(), `{"query":"热更新 先装配成功 再发布 运行实例"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "a.md") {
		t.Errorf("output should carry the source file:\n%s", out)
	}
	if !strings.Contains(out, "发布") {
		t.Errorf("output should carry the section name:\n%s", out)
	}
	if !strings.Contains(out, "(L") {
		t.Errorf("output should carry section line numbers:\n%s", out)
	}
	if !strings.Contains(out, "热更新") {
		t.Errorf("output should carry the document title:\n%s", out)
	}
	if !strings.Contains(out, "score") {
		t.Errorf("output should carry the score:\n%s", out)
	}
}

func TestSearchToolOnEmptyIndexSaysSo(t *testing.T) {
	st := newTestStore(t)
	tl := mustSearchTool(t, st, newFakeEmbedder(8), 5)
	out, err := tl.InvokableRun(context.Background(), `{"query":"任意问题"}`)
	if err != nil {
		t.Fatalf(" InvokableRun: %v", err)
	}
	if !strings.Contains(out, "No matching passages") || !strings.Contains(out, "kb status") {
		t.Errorf("an empty index should be explained, got: %q", out)
	}
}

// ── kb_index ─────────────────────────────────────────────────────────────

func TestIndexToolRefusesFullRebuild(t *testing.T) {
	root, corpus := seedProject(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	emb := newFakeEmbedder(16)
	ix := newTestIndexer(t, st, emb, 10)
	tl := mustIndexTool(t, st, ix, corpus, root)

	for _, args := range []string{`{}`, `{"paths":[]}`, `{"paths":null}`} {
		out, err := tl.InvokableRun(context.Background(), args)
		if err == nil {
			t.Fatalf("args %s: a full rebuild was accepted (output %q)", args, out)
		}
		if !strings.Contains(err.Error(), "openheron kb index") {
			t.Errorf("args %s: the error must point at the CLI, got: %v", args, err)
		}
	}
	if len(emb.batches()) != 0 {
		t.Errorf("a refused call still made %d embedding requests", len(emb.batches()))
	}
	if got := len(st.Entries()); got != 0 {
		t.Errorf("a refused call wrote %d entries", got)
	}
}

func TestIndexToolIndexesNamedFiles(t *testing.T) {
	root, corpus := seedProject(t, map[string]string{
		"a.md": twoDocs,
		"b.md": "# B\n\n## 一节\n\n内容\n",
	})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(16), 10)
	tl := mustIndexTool(t, st, ix, corpus, root)

	out, err := tl.InvokableRun(context.Background(), `{"paths":["docs/a.md"]}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var res Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output is not the Result JSON (%v): %q", err, out)
	}
	if res.Changed != 1 || res.Skipped != 0 {
		t.Errorf("res = %+v, want changed 1 / skipped 0", res)
	}
	if paths := catalogPaths(st); len(paths) != 1 || paths[0] != "a.md" {
		t.Errorf("catalog = %v, want only a.md", paths)
	}
}

func TestIndexToolIsIncremental(t *testing.T) {
	root, corpus := seedProject(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	emb := newFakeEmbedder(16)
	ix := newTestIndexer(t, st, emb, 10)
	tl := mustIndexTool(t, st, ix, corpus, root)

	if _, err := tl.InvokableRun(context.Background(), `{"paths":["docs/a.md"]}`); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := len(emb.batches())

	out, err := tl.InvokableRun(context.Background(), `{"paths":["docs/a.md"]}`)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	var res Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Changed != 0 || res.Skipped != 1 {
		t.Errorf("res = %+v, want changed 0 / skipped 1", res)
	}
	if got := len(emb.batches()) - before; got != 0 {
		t.Errorf("an unchanged re-index made %d embedding requests; it must make none", got)
	}
}

func TestIndexToolRejectsPathsOutsideCorpus(t *testing.T) {
	root, corpus := seedProject(t, map[string]string{"a.md": twoDocs})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(16), 10)
	tl := mustIndexTool(t, st, ix, corpus, root)

	_, err := tl.InvokableRun(context.Background(), `{"paths":["README.md"]}`)
	if err == nil {
		t.Fatal("a path outside the corpus was accepted")
	}
	if !strings.Contains(err.Error(), "outside the corpus") {
		t.Errorf("error should say why, got: %v", err)
	}
}

// ── CorpusRel ────────────────────────────────────────────────────────────

func TestCorpusRelResolvesAgainstProjectRoot(t *testing.T) {
	root := "/proj"
	corpus := "/proj/docs"

	got, err := CorpusRel(root, corpus, []string{"docs/a.md", "docs/sub/b.md"})
	if err != nil {
		t.Fatalf("CorpusRel: %v", err)
	}
	if len(got) != 2 || got[0] != "a.md" || got[1] != "sub/b.md" {
		t.Fatalf("got %v, want [a.md sub/b.md]", got)
	}

	got, err = CorpusRel(root, corpus, []string{"/proj/docs/a.md"})
	if err != nil || len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("absolute path inside the corpus: got %v err %v", got, err)
	}
}

func TestCorpusRelRejectsEscapes(t *testing.T) {
	for _, p := range []string{"README.md", "../docs/a.md", "/etc/passwd"} {
		if _, err := CorpusRel("/proj", "/proj/docs", []string{p}); err == nil {
			t.Errorf("%q was accepted; it is not under the corpus", p)
		}
	}
}

func TestToolsValidateTheirConfig(t *testing.T) {
	if _, err := NewSearchTool(SearchConfig{}); err == nil {
		t.Error("kb_search accepted a nil retriever")
	}
	if _, err := NewIndexTool(IndexToolConfig{}); err == nil {
		t.Error("kb_index accepted an empty config")
	}
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)
	if _, err := NewIndexTool(IndexToolConfig{Indexer: ix, CorpusDir: "x"}); err == nil {
		t.Error("kb_index accepted a nil store")
	}
	if _, err := NewIndexTool(IndexToolConfig{Store: st, CorpusDir: "x"}); err == nil {
		t.Error("kb_index accepted a nil indexer")
	}
	if _, err := NewIndexTool(IndexToolConfig{Store: st, Indexer: ix}); err == nil {
		t.Error("kb_index accepted an empty corpus")
	}
}

func catalogPaths(st *Store) []string {
	entries := st.Entries()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func TestSearchToolCapsSectionsByRelevance(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)

	e := Entry{Path: "big.md", Title: "大文档", Summary: "很多章节"}
	for i := 1; i <= 30; i++ {
		e.Sections = append(e.Sections, Section{Title: fmt.Sprintf("第 %d 节", i), Line: i * 10})
	}
	e.Sections[25] = Section{Title: "配置写入为什么不能写合并视图", Line: 260}
	seedEntry(t, st, e, nil)

	out, err := mustSearchTool(t, st, emb, 5).InvokableRun(
		context.Background(), `{"query":"配置写入为什么不能写合并视图"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	if !strings.Contains(out, "(L260)") {
		t.Errorf("the relevant 26th section was not listed — that means the first 12 sections were picked:\n%s", out)
	}
	if !strings.Contains(out, "[+18 more]") {
		t.Errorf("30 sections list only 12 of them, it must report [+18 more]:\n%s", out)
	}
}

func TestSearchToolDistinguishesTheTwoTruncations(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)

	e := Entry{
		Path: "dropped.md", Title: "被砍过的文档", Summary: "摘要",
		Sections:          []Section{{Title: "留下的一节", Line: 3}},
		SectionsTruncated: 42,
	}
	seedEntry(t, st, e, nil)

	out, err := mustSearchTool(t, st, emb, 5).InvokableRun(
		context.Background(), `{"query":"被砍过的文档"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "42 headings were dropped") {
		t.Errorf("the storage hard cap was hit but not reported:\n%s", out)
	}
	if strings.Contains(out, "[+42") {
		t.Errorf("the incident was rendered like a display budget ([+N]) — the two must be distinguishable at a glance:\n%s", out)
	}
}

func TestSearchToolDoesNotRepeatTheOutline(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	e := Entry{
		Path: "a.md", Title: "热更新", Summary: "装配先成功",
		Sections: []Section{{Title: "发布", Line: 3}},
		Links:    []string{"b.md"},
	}
	seedEntry(t, st, e, nil)

	out, err := mustSearchTool(t, st, emb, 5).InvokableRun(
		context.Background(), `{"query":"热更新 装配"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if n := strings.Count(out, "发布"); n != 1 {
		t.Errorf("the section name appeared %d times, want 1 (the copy in the body must not be printed again):\n%s", n, out)
	}
	if n := strings.Count(out, "热更新"); n != 1 {
		t.Errorf("the title appeared %d times, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "links: b.md") {
		t.Errorf("the outgoing link was not emitted:\n%s", out)
	}
}

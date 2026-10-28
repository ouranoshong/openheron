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
		Corpus:      corpus,
		ProjectRoot: projectRoot,
		Headers:     map[string]string{"##": "section", "###": "subsection"},
	})
	if err != nil {
		t.Fatalf("NewIndexTool: %v", err)
	}
	return tl
}

// 两个小别名：工具只需要能跑，不必暴露它到底是 InvokableTool 还是别的。
type searchInvoker = tool.InvokableTool
type toolInvoker = tool.InvokableTool

// ── 工具名与描述 ─────────────────────────────────────────────────────────

// ★ 名字取动词、且必须与 config 的默认档位表一致 —— 两边漂移的后果是
//
//	kb_index 落回兜底的 allow，写索引不再要审批，症状看起来像
//	「索引悄悄不审批了」。
//	（跨包一致性由 config 侧的 TestDefaultPolicyCoversKBTools 钉住。）
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

// ★ 描述里必须写明用自然语言提问。实测（§9.3）：同一段内容，自然语句一次
//
//	命中，两个关键词两次都错 —— 提问方式对召回的影响远大于参数调优，而模型
//	默认倾向塞关键词。所以这句提示是功能的一部分，不是文档修辞。
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
	// ★ query 是必填 —— 少了它模型可能传个空对象，而空查询的召回是随机的。
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
	// ★ 出处与**章节行号**都要进来（§14.1）。少了行号，模型拿到「发布」这一
	//   节也不知道该读第几行，于是要自己重新定位一遍。
	if !strings.Contains(out, "a.md") {
		t.Errorf("output should carry the source file:\n%s", out)
	}
	if !strings.Contains(out, "发布") {
		t.Errorf("output should carry the section name:\n%s", out)
	}
	if !strings.Contains(out, "(L") {
		t.Errorf("output should carry section line numbers:\n%s", out)
	}
	// ★ 输出是**提纲**，不是正文（§10）：`Content` 是 EmbedText —— 标题、摘要、
	//   章节名。正文由模型照着行号 `read_file` 去读，塞进搜索结果反而会把
	//   上下文预算烧在它不需要整段读的文件上。所以这里钉的必须是提纲的形状，
	//   而不是「某段正文逐字出现」—— 后者在 v2 里**不该**出现。
	if !strings.Contains(out, "热更新") {
		t.Errorf("output should carry the document title:\n%s", out)
	}
	// ★ 分数要透出来：没有它，模型无从判断「这条够不够用」。
	if !strings.Contains(out, "score") {
		t.Errorf("output should carry the score:\n%s", out)
	}
}

// ★ 空索引不该给一个空字符串 —— 模型会把「没有输出」读成「工具坏了」，
//
//	而真相是「还没建索引」，那有明确的下一步。
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

// ★ 空 paths 必须**拒绝**而不是全量重建。全量在自己的语料上是十分钟量级
//
//	（实测 405 块 / 9m49s），一个十分钟的工具调用在 turn 里必然超时或被中断，
//	而中断点落在「写了一半」的索引上。
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
	// ★ 被拒时**一次向量化都不能发生** —— 那才是「不执行」的意思。
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

	// ★ 路径按**项目根**算，与 corpus 同一基准 —— 与 `openheron kb index` 一致。
	out, err := tl.InvokableRun(context.Background(), `{"paths":["docs/a.md"]}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var res Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output is not the Result JSON (%v): %q", err, out)
	}
	// ★ 结果必须能区分「有改动」与「没改动」：模型要靠 Changed == 0 判断
	//   「不用再调了」。
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
	// ★ 没变就一次向量化都不该发。这条是 §9.2 那个十分钟量级能被接受的唯一
	//   理由 —— 否则每次调用都要重付一遍。
	if got := len(emb.batches()) - before; got != 0 {
		t.Errorf("an unchanged re-index made %d embedding requests; it must make none", got)
	}
}

// ★ 语料外面的路径必须报错。放行的后果不是「多索引一个文件」，而是它下一轮
//
//	会被删除清扫扫掉 —— 「索引了却永远留不住」，且没有任何提示。
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

	// 绝对路径同样接受（只要在语料里）。
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

// ── 构造期校验 ───────────────────────────────────────────────────────────

func TestToolsValidateTheirConfig(t *testing.T) {
	if _, err := NewSearchTool(SearchConfig{}); err == nil {
		t.Error("kb_search accepted a nil retriever")
	}
	if _, err := NewIndexTool(IndexToolConfig{}); err == nil {
		t.Error("kb_index accepted an empty config")
	}
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)
	if _, err := NewIndexTool(IndexToolConfig{Indexer: ix, Corpus: "x"}); err == nil {
		t.Error("kb_index accepted a nil store")
	}
	if _, err := NewIndexTool(IndexToolConfig{Store: st, Corpus: "x"}); err == nil {
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

// ★ 显示预算（§11.1）：一条命中最多列 [maxSectionsShown] 节，而且**按与查询的
// 相关度挑**，不是「前 N 节」。
//
//	这条用例把相关的那一节放在**第 26 位**：如果实现退化成「取前 12 节」，
//	它就一定不会被列出来 —— 而那正是设计里点名不要的退化（长文档的开头是
//	标题、目标、非目标，恰好是最不需要提示的部分）。
func TestSearchToolCapsSectionsByRelevance(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)

	e := Entry{Path: "big.md", Title: "大文档", Summary: "很多章节"}
	for i := 1; i <= 30; i++ {
		e.Sections = append(e.Sections, Section{Title: fmt.Sprintf("第 %d 节", i), Line: i * 10})
	}
	// 第 26 节（索引 25）才是该被挑中的那一个，且它在文档的后半段。
	e.Sections[25] = Section{Title: "配置写入为什么不能写合并视图", Line: 260}
	// 向量留空：这几条验的是**输出形状**，不需要余弦那一路参与。
	seedEntry(t, st, e, nil)

	out, err := mustSearchTool(t, st, emb, 5).InvokableRun(
		context.Background(), `{"query":"配置写入为什么不能写合并视图"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	if !strings.Contains(out, "(L260)") {
		t.Errorf("相关的第 26 节没被列出来 —— 说明挑的是「前 12 节」：\n%s", out)
	}
	if !strings.Contains(out, "[+18 more]") {
		t.Errorf("30 节列 12 节，应当报 [+18 more]：\n%s", out)
	}
	// ★ 不主张「无关的那些必须消失」。绝大多数章节与查询得 0 分，而 0 分之间
	//   是并列 —— 并列按原顺序填满剩下的名额，于是开头那几节照旧在列。这是
	//   **对的**：它是一份提纲，从头开始读得通才有用；这条用例要钉的只有
	//   「排在后面的相关章节也**进得来**」，而那正是「取前 12 节」做不到的。
}

// ★ 两个 `[+N]` 的意思完全不同，**写法也必须不同**（§11.1）：
//
//   - 显示预算没列全 ⇒ `[+18 more]`，常态；
//
//   - 建索引时撞了存储硬线 ⇒ 一行告警，事故。
//
//     同形会让告警看起来像一次正常截断，而那正是把两者分开的理由。
func TestSearchToolDistinguishesTheTwoTruncations(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)

	e := Entry{
		Path: "dropped.md", Title: "被砍过的文档", Summary: "摘要",
		Sections:          []Section{{Title: "留下的一节", Line: 3}},
		SectionsTruncated: 42, // 建索引时就丢了 42 节
	}
	// 向量留空：这几条验的是**输出形状**，不需要余弦那一路参与。
	seedEntry(t, st, e, nil)

	out, err := mustSearchTool(t, st, emb, 5).InvokableRun(
		context.Background(), `{"query":"被砍过的文档"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "42 headings were dropped") {
		t.Errorf("存储硬线被撞了却没报：\n%s", out)
	}
	if strings.Contains(out, "[+42") {
		t.Errorf("事故被写成了显示预算的样子（[+N]）—— 两者必须能一眼分开：\n%s", out)
	}
}

// ★ 正文不再重复印（§11.1）：`Content` 就是标题 + 摘要 + 章节名 + 出链的拼接，
// 而上面四行已经逐字段给过了。重复的代价实测是一次检索 36.8 KB → 6.2 KB。
func TestSearchToolDoesNotRepeatTheOutline(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	e := Entry{
		Path: "a.md", Title: "热更新", Summary: "装配先成功",
		Sections: []Section{{Title: "发布", Line: 3}},
		Links:    []string{"b.md"},
	}
	// 向量留空：这几条验的是**输出形状**，不需要余弦那一路参与。
	seedEntry(t, st, e, nil)

	out, err := mustSearchTool(t, st, emb, 5).InvokableRun(
		context.Background(), `{"query":"热更新 装配"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	// 章节名只该出现一次（`sections:` 那一行，带行号）。
	if n := strings.Count(out, "发布"); n != 1 {
		t.Errorf("章节名出现了 %d 次，want 1（正文那一份不该再印一遍）：\n%s", n, out)
	}
	if n := strings.Count(out, "热更新"); n != 1 {
		t.Errorf("标题出现了 %d 次，want 1：\n%s", n, out)
	}
	// 出链要单独给出来 —— 它是目录相对正文唯一的增量信息。
	if !strings.Contains(out, "links: b.md") {
		t.Errorf("出链没给出来：\n%s", out)
	}
}

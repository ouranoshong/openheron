package kb

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// recording 记下它收到的查询原文。
type recording struct {
	got  []string
	docs []*schema.Document
}

func (r *recording) Retrieve(_ context.Context, query string, _ ...retriever.Option) (
	[]*schema.Document, error) {
	r.got = append(r.got, query)
	return r.docs, nil
}

// ★ 这是 docs/kb-design.md §9.3 那条：Qwen3-Embedding 是非对称训练的，指令
//
//	前缀**只加在查询侧**。加错不报错，只是召回质量悄悄变差 —— 所以必须有
//	测试钉住「只加一边」，而这是唯一能钉住它的地方。
func TestInstructionAppliesToQuerySideOnly(t *testing.T) {
	rec := &recording{docs: []*schema.Document{newDoc("x", "c", nil)}}
	rt := Instruct(rec, "Instruct: 检索设计文档\nQuery: ")

	if _, err := rt.Retrieve(context.Background(), "热更新"); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("inner retriever called %d times", len(rec.got))
	}
	if want := "Instruct: 检索设计文档\nQuery: 热更新"; rec.got[0] != want {
		t.Fatalf("query passed down = %q, want %q", rec.got[0], want)
	}
}

// ★ 索引侧绝不能带前缀。这条用索引流水线自己的断言来钉：切分出来的文本
//
//	必须与源文件里的**逐字一致**，而不是被加了任何东西。
func TestInstructionDoesNotAffectIndexSide(t *testing.T) {
	const body = "## 热更新\n\n先装配成功再发布。\n"
	corpus := seedCorpus(t, map[string]string{"a.md": body})
	st := newTestStore(t)
	emb := newFakeEmbedder(16)
	ix := newTestIndexer(t, st, emb, 10)

	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("Index: %v", err)
	}
	for _, sent := range emb.embeds() {
		if strings.Contains(sent, "Instruct:") || strings.Contains(sent, "Query:") {
			t.Fatalf("the index side sent %q; the instruction prefix belongs to queries only", sent)
		}
	}
}

func TestInstructWithEmptyPrefixIsPassthrough(t *testing.T) {
	rec := &recording{}
	if got := Instruct(rec, ""); got != retriever.Retriever(rec) {
		t.Error("an empty prefix should return the inner retriever unchanged")
	}
	if got := Instruct(nil, "x"); got != nil {
		t.Error("Instruct(nil, …) should return nil")
	}
}

func TestQueryRejectsEmptyQuery(t *testing.T) {
	rec := &recording{}
	if _, err := Query(context.Background(), rec, "", "   ", 5); err == nil {
		t.Fatal("a blank query was accepted")
	}
}

// ★ Source / Title / Sections 必须真的回来。它们是「这条答案出自哪里」的
//
//	唯一线索，而缺了它们的结果看上去仍然正常 —— 只是模型无法核对、
//	用户无法追溯，也无法照着行号去读那一节。
func TestQueryReturnsSourceAndSections(t *testing.T) {
	entry := Entry{
		Path:    "a.md",
		Title:   "配置架构设计",
		Summary: "配置进版本库，运行数据不进。",
		Sections: []Section{
			{Title: "热更新", Line: 12},
			{Title: "校验", Line: 40},
		},
	}
	docs := []*schema.Document{{
		ID:      "a.md",
		Content: entry.EmbedText(),
		MetaData: map[string]any{
			MetaSource: "a.md",
			MetaEntry:  entry,
		},
	}}
	docs[0].WithScore(0.75)

	rec := &recording{docs: docs}
	hits, err := Query(context.Background(), rec, "", "热更新", 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d", len(hits))
	}
	h := hits[0]
	if h.Source != "a.md" {
		t.Errorf("source = %q, want a.md", h.Source)
	}
	if h.Title != "配置架构设计" {
		t.Errorf("title = %q", h.Title)
	}
	if h.Summary != "配置进版本库，运行数据不进。" {
		t.Errorf("summary = %q", h.Summary)
	}
	// ★ **行号必须原样透出来**（§14.1）—— 没有它，模型拿到「热更新」也
	//   不知道该读第几行，于是要自己重新定位一遍。
	if len(h.Sections) != 2 || h.Sections[0].Line != 12 {
		t.Errorf("sections = %+v, want both with line numbers", h.Sections)
	}
	if h.Content != entry.EmbedText() {
		t.Errorf("content = %q", h.Content)
	}
	// ★ 分数要透出来：没有它，调 top_k 就只能靠猜。
	if h.Score != 0.75 {
		t.Errorf("score = %v, want 0.75", h.Score)
	}
}

// ★ ResultFields 与缺省的 ReturnFields 不是一个东西：前者是 openheron 要的
//
//	出处字段，后者是与 Redis 版对齐的联合默认值。把它们混为一谈，换后端
//	时行为会悄悄变。
func TestResultFieldsExtendTheRedisDefault(t *testing.T) {
	has := func(fs []string, want string) bool {
		for _, f := range fs {
			if f == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{FieldContent, MetaSource, MetaEntry} {
		if !has(ResultFields, want) {
			t.Errorf("ResultFields is missing %q", want)
		}
	}
	// ★ **不能再要求向量**：Embedding 可选（§8.5），而要求一个不存在的
	//   字段会把一次能用的检索变成报错。
	if has(ResultFields, FieldVector) {
		t.Error("ResultFields must not require the vector; embedding is optional in v2")
	}

	st := newTestStore(t)
	rc, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: newFakeEmbedder(4),
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	if len(rc.config.ReturnFields) != 2 {
		t.Errorf("the retriever default = %v, want exactly the Redis pair", rc.config.ReturnFields)
	}
}

// ★ 无向量时要求 vector_content **不能**报错（§8.5）。v1 里这里会报
// "requested but record has no vector"，而 v2 没有向量是常态。
func TestRetrieverToleratesMissingVectors(t *testing.T) {
	st := newTestStore(t)
	// 纯词法：一条向量都没有。
	seedEntry(t, st, ent("a.md", "配置写入", "走原子写"), nil)
	seedEntry(t, st, ent("b.md", "会话归档", "checkpoint 落库"), nil)

	rt, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, ReturnFields: []string{FieldContent, FieldVector, MetaSource, MetaEntry},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	docs, err := rt.Retrieve(context.Background(), "配置写入")
	if err != nil {
		t.Fatalf("Retrieve with no vectors at all: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2 (lexical retrieval still works)", len(docs))
	}
	if len(docs[0].DenseVector()) != 0 {
		t.Errorf("dense vector = %v, want none", docs[0].DenseVector())
	}
	if docs[0].MetaData[MetaSource] == nil {
		t.Error("source metadata is missing")
	}
}

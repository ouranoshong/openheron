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

// newTestRetriever 造一个**按 eino 缺省返回字段**的检索器。
//
// ★ 它的 `ReturnFields` 是空的，于是走的 [NewRetriever] 那条缺省
// （`{content, vector}`）—— 与 Redis 版一字不差。后果是**文档里没有
// `MetaEntry`**，于是由它拼出来的 [Hit] 没有标题、摘要、章节、出链。
//
// ★ 要验工具输出（那些字段必须出现）时用它就会得到一份「只有出处和分数」的
//
//	结果，而看起来像渲染坏了。那种场景用 [mustSearchTool]，它显式传
//	[ResultFields] —— 与 `main.go` / `assembleSearch` 同一条路。
func newTestRetriever(t *testing.T, st *Store, emb *fakeEmbedder, topK int) *FileRetriever {
	t.Helper()
	cfg := &RetrieverConfig{Store: st, TopK: topK}
	// ★ 必须判 nil 再赋值：把一个 nil 的 `*fakeEmbedder` 塞进接口，接口本身
	//   **不是 nil**（类型与值分开），于是 `Embedding != nil` 为真、走上有
	//   embedder 的那条路，然后对着空指针发请求 —— 症状是一个莫名其妙的
	//   panic，而不是「没配 embedder」。
	if emb != nil {
		cfg.Embedding = emb
	}
	rt, err := NewRetriever(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	return rt
}

// 两条语料的正文。导出来是因为阈值测试要用**逐字相同**的查询 —— 词袋
// 之下相似度高低取决于共享多少个词，随便写一句是测不出「距离≈0」的。
const (
	docHot  = "热更新 先装配成功 再发布 运行实例"
	docCold = "会话历史 归档 checkpoint 落库"
)

// seedProject 造一个项目根的布局：`<root>/docs/*.md`。
//
// ★ 语料在 `docs/` 下一层，而不是直接就是项目根 —— 真实配置是 `corpus: docs`，
//
//	而 [CorpusRel] 按项目根换算路径。把语料摊在根上的话，「路径算错了」这件事
//	在测试里根本不会发生。
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

// seededStore 走**真实的索引流水线**建一个小索引。
//
// ★ 不走的话就得手工凑 Metadata，而那与 Index 实际写进去的形状会悄悄分叉 ——
//
//	症状是「测试里能召回，真跑起来缺 source」。
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

// ★ 检索的第一条要是最相关的 —— 这条断言是整个包的立身之本。一个只会写、
// 不会找的索引没有任何用处。
func TestRetrieveRanksRelevantFirst(t *testing.T) {
	st, emb := seededStore(t)
	rt := newTestRetriever(t, st, emb, 5)

	docs, err := rt.Retrieve(context.Background(), "热更新 装配 成功 发布")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// ★ **一个源文件一条**（§6「单元是文件，不是块」）。v1 这里是 4 块（每个
	//   文件切 `#` 与 `##` 两层），v2 目录按文件存、检索按文件回 —— 块数只是
	//   切分参数的副产品，不该出现在断言里。
	//
	// ★ 断言的是「a.md 整体排在 b.md 前面」，**不是**「某一小块排第一」——
	//   替身 embedder 用的是词袋：块越长，命中的那几个词被稀释得越厉害，
	//   于是短标题块常常压过长正文块。那是替身的粗糙，不是检索的问题，
	//   拿它去钉具体名次会得到一条与真实模型无关的、易碎的断言。
	//   「问热更新，a.md 排在 b.md 前」才是两个 embedder 都该满足的性质。
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
	// ★ 自己造**三份**语料，不用 [seededStore] —— 那个只有两份，于是
	//   「要 3 给 3」会退化成「要 3 给 2」：断言与 TopK 无关，测的只是
	//   语料够不够，而不够恰恰是这条最容易漏掉的情况。
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

	// 调用期的 WithTopK 必须能覆盖配置值 —— 它进的是 eino 的选项通道。
	docs, err = rt.Retrieve(context.Background(), "热更新", retriever.WithTopK(3))
	if err != nil {
		t.Fatalf("Retrieve with WithTopK(3): %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("WithTopK(3) returned %d docs, want 3", len(docs))
	}
}

// directStore 直接写向量，**不走流水线** —— 阈值测的是 store/retriever 的
// 语义，而流水线会在块前拼上标题行，那样「逐字相同」就不再相同了。
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

// ★ ReturnFields 点名了一个库里没有的字段 → 报错，不是静默少一个字段。
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

// ★ Index 与 Store 目录是两份说法。不一致就报错 —— 留着这个字段是为了与
//
//	Redis 版对齐，可一旦允许「配了但不生效」，它就成了一个会安静撒谎的字段。
func TestRetrieverRejectsIndexDirMismatch(t *testing.T) {
	st := newTestStore(t)
	if _, err := NewRetriever(context.Background(), &RetrieverConfig{
		Store: st, Embedding: newFakeEmbedder(4), Index: "not-the-dir",
	}); err == nil {
		t.Fatal("a mismatched Index was accepted")
	}

	// 与目录同名时必须能过 —— 否则这个字段永远是死的。
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
	// ★ **Embedding 可选**（§8.5）：没有嵌入端点时词法那一路照常工作，
	//   这不是降级到「坏掉」，而是降级到「纯词法」。v1 里 nil 是配错，
	//   v2 里 nil 是一个合法的部署形态 —— 这条就是钉住那次语义翻转。
	if _, err := NewRetriever(context.Background(), &RetrieverConfig{Store: st}); err != nil {
		t.Errorf("nil embedding rejected (v2 允许纯词法检索): %v", err)
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

	// ★ 它会进 callbacks.RunInfo —— 观测里靠它区分后端。写成别的就分不开了。
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

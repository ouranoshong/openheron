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

// newTestIndexer 造一个索引器。emb 为 nil = **纯词法模式**（§8.5）。
//
// ★ 参数类型是 `*fakeEmbedder` 而不是接口：nil 接口与「装着一个 nil 指针的
//
//	接口」不是一回事，而后者会让 NewIndexer 认为嵌入是配好的 —— 于是
//	「无嵌入」这条路径在测试里根本走不到。
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

// docsOf 把若干条目包成 eino 文档 —— 这正是流水线交进去的形状。
func docsOf(entries ...Entry) []*schema.Document {
	out := make([]*schema.Document, 0, len(entries))
	for _, e := range entries {
		out = append(out, newDocOf(e))
	}
	return out
}

// ★ MaxBatchChars 如果只写进配置却不生效，那它就是个谎言：一批过大的请求
// 会把嵌入服务的上下文撑爆，而症状是「改了配置没区别」。这条断言是唯一
// 能抓住那种失效的地方。
func TestIndexerActuallyBatches(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	// ★ MaxBatchChars 限到 3 个字符，于是**每条文档单独成批** ——
	//   这里验的是体积上限，不是条数上限。
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
	// 10 条、每条都超 3 字符 → 10 批各 1 条。
	want := make([]int, 10)
	for i := range want {
		want[i] = 1
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

// ★ BatchSize 那一路同样要生效 —— 体积没超时才轮到条数上限。
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
	// 10 条、批 4 → 4 + 4 + 2。
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
	// ★ 向量在侧车里，**不在目录里** —— 目录是人可读可改的，里面没有向量
	//   字段。两处都放一份就是两份真相，而不一致时检索照常工作，只是
	//   那一份永远读不到。
	if len(de.Vector) != 8 {
		t.Errorf("vector len = %d, want 8", len(de.Vector))
	}
	// 目录里同样要有这条，且带着它的章节行号。
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

// ★ 索引器必须收下**带着 Entry 的**文档。少了它就重建不出章节行号（§14.1），
// 而重建不出来比报错更糟：行号丢了，模型照着读会读到错的地方。
func TestIndexerRejectsDocWithoutEntry(t *testing.T) {
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)

	_, err := ix.Store(context.Background(), []*schema.Document{
		// ★ 裸文档，不走 newDoc —— 那个函数会附上一份能过校验的 Entry，
		//   而这条要的正是「没有 Entry」。
		{ID: "x.md", Content: "只有正文没有 Entry", MetaData: map[string]any{}},
	})
	if err == nil {
		t.Fatal("a doc with no Entry in metadata was accepted")
	}
	if !strings.Contains(err.Error(), "kb_entry") {
		t.Errorf("error should name the missing key, got: %v", err)
	}
}

// ★ 文档 ID 必须与 Entry.Path 相等 —— 不等说明两份说法，而检索按前者、
// 目录按后者，于是「查得到但目录里没有」。
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
	// ★ **目录不能动**：向量化失败在写盘之前，而留一份没有向量的目录会让
	//   下一轮按 EmbedText 判定「没变」，于是那几条永远不会被重嵌。
	if got := len(st.Entries()); got != 0 {
		t.Fatalf("catalog has %d entries after a failed Store, want 0", got)
	}
}

// ★ 维度变了必须报错，而不是悄悄混进两套不具可比性的向量。
func TestIndexerRejectsDimMismatch(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetIdentity("m1", 4); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
	ix := newTestIndexer(t, st, newFakeEmbedder(4), 10)
	if _, err := ix.Store(context.Background(), docsOf(ent("a.md", "t", "s"))); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// 装配层用另一个维度再声明一次身份 —— 那一步就该报错。
	if err := st.SetIdentity("m1", 8); err == nil {
		t.Fatal("a different dim was accepted")
	} else if !strings.Contains(err.Error(), "delete") {
		t.Errorf("error should tell the user how to recover, got: %v", err)
	}
}

// ★ §7.2 / §13 M4 的验收：**人改一行摘要 ⇒ `ReEmbedded == 1` 而 `Changed == 0`**。
//
//	v2 的第一版把向量只挂在源文件上（人改摘要不重嵌），于是「摘要」在两个地方
//	各说各话：目录里显示人的、排序用机器的 —— 他花时间写的那句话对任何一次
//	相似度计算都无效。这条用例同时钉住两件事，**缺一不可**：
//
//	  `ReEmbedded == 1`  这一轮真的重算了向量；
//	  送进去的文本含他那句  算的是**他写的那份**，不是机器那份。
//
//	只验前者不够：机器那份也会让 `ReEmbedded` 变成 1。
//
//	`Changed == 0` 是这条的另一半，也是它区别于「改了源文件」的地方：源文件的
//	(mtime, size) 一动都没动，动的只有目录里那一行。两者混在一个数里的话，
//	「他在编辑器里改了摘要」与「语料真的变了」就再也分不开。
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
		t.Fatal("第一遍没有产出向量")
	}

	// ★ 什么都不改地再跑一遍 ⇒ 一条都不重嵌。没有这一条，下面就分不清
	//   「改摘要触发了重嵌」与「每轮都重嵌」—— 后者自查起来很像前者。
	res, err := Index(ctx, ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("no-op Index: %v", err)
	}
	if res.ReEmbedded != 0 || res.Unchanged != 1 {
		t.Fatalf("no-op re-run: re-embedded=%d unchanged=%d, want 0/1",
			res.ReEmbedded, res.Unchanged)
	}

	// 人改目录里那一行摘要（源文件一个字不碰）。
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
		t.Errorf("changed = %d, want 0（源文件没被碰过）", res.Changed)
	}
	if res.ReEmbedded != 1 {
		t.Errorf("re-embedded = %d, want 1（人改了摘要就该重嵌）", res.ReEmbedded)
	}

	texts := emb.embeds()
	if len(texts) != 2 {
		t.Fatalf("一共送了 %d 条文本，want 2（第一遍一条、改摘要后一条）", len(texts))
	}
	if !strings.Contains(texts[1], "人写的摘要") {
		t.Errorf("送去向量化的是机器那份，不是人写的那份 —— "+
			"摘要在目录里显示的是他的、排序用的却是机器的：\n%q", texts[1])
	}

	// 向量真的变了：`EmbedText` 含摘要，摘要变了它就该变。
	if sameVector(v1, st.Sidecar().Entries["a.md"].Vector) {
		t.Error("摘要变了而向量没变 —— 说明它嵌的还是机器那份")
	}
}

// TestHumanSummaryEditReEmbedsWithoutSourceChange 的补充：**只动 mtime 不改文字
// 不该重嵌**。它与上一条一起把「按内容判定」这句话夹住 —— 单独一条容易被
// 实现成「按 mtime 判定」而照样通过。
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
	// 把文件「重存」一遍：内容逐字节相同，只有 mtime 变。
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
		t.Errorf("changed = %d, want 1（mtime 真的变了）", res.Changed)
	}
	if res.ReEmbedded != 0 {
		t.Errorf("re-embedded = %d, want 0 —— 文字一个字没改，重嵌是纯粹的浪费", res.ReEmbedded)
	}
}

func TestNewIndexerValidatesConfig(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(4)

	// ★ **Embedding 为 nil 不再报错**（§8.5）：词法那一路是真的能用的。
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

// TestIndexerWritesIdentity 模型身份要跟着索引一起落盘 —— 否则下次建索引时
// 无从判断「换没换模型」。
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

// ★ 无嵌入时 Reconcile 照常写目录，且**一个嵌入请求都不发**（§8.5）。
func TestReconcileWithoutEmbeddingIssuesNoRequests(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	// 造一个「配了嵌入但这一轮不用」的索引器 —— Reconcile 收 nil 时会退回
	// 到配置里那个，所以这里直接用 nil 配置造一个纯词法的。
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

package kb

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// 这个文件是 K1 的验收：**「为后续快速替换做准备」的可执行定义**。
//
// 它不 import eino-ext 的 redis 包 —— 那会把 go-redis 拖进 go.mod（哪怕只
// 在测试里），而 openheron 的零外部依赖是这个包存在的理由。代价是本文件里的
// 「Redis 版长什么样」是抄来的，一旦上游改了字段名，这里不会自动红。
//
// ★ 抄一次就必须钉住：下表每一行都带 Redis 源的字段名，改本包字段时对着
//
//	上游核一遍，而不是对着这个表 —— 表记的是当初抄的那一刻。
var (
	// eino-ext/components/indexer/redis.IndexerConfig
	redisIndexerFields = []string{
		"Client",           // → Store（共享对象，见 docs/kb-design.md §6.2）
		"KeyPrefix",        // 不提供：写侧/读侧不对称，File 侧统一成 Index
		"DocumentToHashes", // → DocumentToFields
		"BatchSize",        //
		"Embedding",        //
	}

	// eino-ext/components/retriever/redis.RetrieverConfig
	redisRetrieverFields = []string{
		"Client",            // → Store
		"Index",             //
		"VectorField",       //
		"DistanceThreshold", //
		"Dialect",           // 不提供：RediSearch 查询方言版本
		"ReturnFields",      //
		"DocumentConverter", //
		"TopK",              //
		"Embedding",         //
	}
)

// TestIndexerConfigMirrorsRedis 逐字段核对 FileIndexerConfig。
func TestIndexerConfigMirrorsRedis(t *testing.T) {
	want := map[string]string{
		// File 字段名 → Redis 字段名（"-" = 本包专有/新增）
		"Store":            "Client",
		"DocumentToFields": "DocumentToHashes",
		"BatchSize":        "BatchSize",
		"Embedding":        "Embedding",
		// ★ 下面这个是**本包新增**的，Redis 侧没有对应字段 ——
		//   而 Redis 那个 BatchSize 只按条数分批，挡不住「8 条 × 1500 字符」
		//   这种把 llama-server 撑爆的请求（实测进程会挂）。换后端时这个字段
		//   要一起搬过去，不是可以丢掉的 openheron 私货。
		"MaxBatchChars": "-",
	}
	assertFields(t, "IndexerConfig", reflect.TypeOf(IndexerConfig{}), want, redisIndexerFields)
}

// TestRetrieverConfigMirrorsRedis 逐字段核对 FileRetrieverConfig。
func TestRetrieverConfigMirrorsRedis(t *testing.T) {
	want := map[string]string{
		"Store":             "Client",
		"Index":             "Index",
		"VectorField":       "VectorField",
		"DistanceThreshold": "DistanceThreshold",
		"ReturnFields":      "ReturnFields",
		"TopK":              "TopK",
		"Embedding":         "Embedding",
		"DocumentConverter": "DocumentConverter",
	}
	assertFields(t, "RetrieverConfig", reflect.TypeOf(RetrieverConfig{}), want, redisRetrieverFields)
}

// TestMirroredFieldTypesMatch 核对**类型**也一致。
//
// ★ 名字一致而类型漂了，比名字不一致更坏：装配代码抄过去能编译，但语义
//
//	不同（例如 DistanceThreshold 从 *float64 变成 float64，就再也表达不了
//	「nil = 按 TopK」这个区分）。
func TestMirroredFieldTypesMatch(t *testing.T) {
	idx := reflect.TypeOf(IndexerConfig{})
	ret := reflect.TypeOf(RetrieverConfig{})

	cases := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"IndexerConfig.BatchSize", fieldOf(t, idx, "BatchSize").Type, "int"},
		{"IndexerConfig.Embedding", fieldOf(t, idx, "Embedding").Type, "embedding.Embedder"},
		{"RetrieverConfig.TopK", fieldOf(t, ret, "TopK").Type, "int"},
		{"RetrieverConfig.Index", fieldOf(t, ret, "Index").Type, "string"},
		{"RetrieverConfig.VectorField", fieldOf(t, ret, "VectorField").Type, "string"},
		{"RetrieverConfig.ReturnFields", fieldOf(t, ret, "ReturnFields").Type, "[]string"},
	}
	for _, c := range cases {
		if got := c.typ.String(); got != c.want {
			t.Errorf("%s type = %s, want %s", c.name, got, c.want)
		}
	}

	// ★ DistanceThreshold 必须是**指针**：nil 与 0 在这里不是一回事
	//   （nil = 按 TopK，0 = 只召回完全相同的）。
	dt := fieldOf(t, ret, "DistanceThreshold").Type
	if dt.Kind() != reflect.Ptr {
		t.Fatalf("RetrieverConfig.DistanceThreshold kind = %s, want pointer "+
			"(nil and 0 mean different things)", dt.Kind())
	}
	if dt.Elem().Kind() != reflect.Float64 {
		t.Errorf("DistanceThreshold elem = %s, want float64", dt.Elem())
	}
}

// ★ 冗余字段会在换后端时被漏掉，所以要显式说明「这几个是后加的」。
func TestIndexerConfigAdditionsAreDeclared(t *testing.T) {
	typ := reflect.TypeOf(IndexerConfig{})
	for _, name := range []string{"MaxBatchChars"} {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("%s is missing; it was added on purpose and must survive a backend swap", name)
		}
	}
}

// ── 镜像表的两处**刻意**偏离 ────────────────────────────────────────────

// ★ **第一处**（§8.5）：`Embedding` 从「必填」变成可选。
//
//	eino-ext 的 Redis 版在 Embedding 为 nil 时直接报错 —— 那边索引器除了写
//	向量确实没别的事可做。File 侧不一样：文档级目录本身就是内容，而**词法
//	检索这一层不需要模型**。
//
//	钉住它是为了让「换后端只改装配 3 行」这句话保持为真 —— 这里的 nil 不是
//	配错，是一个**合法的部署形态**（§8.5）。哪天有人把它改回必填，装配层
//	就会在没配嵌入端点的环境里炸掉，而那正是 v2 新开出来的能力。
func TestEmbeddingOptionalIsTheFirstMirrorDeviation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ix, err := NewIndexer(ctx, &IndexerConfig{Store: st, Embedding: nil, BatchSize: 10})
	if err != nil {
		t.Fatalf("indexer with no embedder was rejected (Redis 版会拒，File 版不该): %v", err)
	}
	if _, err := ix.Store(ctx, []*schema.Document{newDoc("a.md", "热更新 装配", nil)}); err != nil {
		t.Fatalf("store without an embedder: %v", err)
	}
	if got := len(st.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1 — 无模型也必须写出目录", got)
	}
	if v := st.Sidecar().Entries["a.md"].Vector; len(v) != 0 {
		t.Errorf("vector len = %d, want 0 — 没有 embedder 就不写向量", len(v))
	}

	if _, err := NewRetriever(ctx, &RetrieverConfig{Store: st}); err != nil {
		t.Fatalf("retriever with no embedder was rejected: %v", err)
	}
}

// ★ **第二处**（§8.4）：`DistanceThreshold` 的含义。
//
//	字段名字面是**余弦距离**（越小越像，`distance = 1 - cosine`），而 File 版
//	的 Score 是**相似度**、还是 `0.5*词法 + 0.5*余弦` 的融合 —— 融合之后这个
//	字段不再有对应量纲。Redis 版拿它卡距离；File 版不能照抄。
//
//	决定：**有 embedder 时忽略它**（套上就是骗人，而且方向还是反的）；
//	**无 embedder 时**当词法分的下限门槛（那时分数就是纯词法，量纲明确）。
//
// ★ 三段断言缺一不可，各排除一种读法：
//
//	① 无 embedder + 门槛 0.5 ⇒ 少一条 —— 它**生效**，且是下限门槛；
//	② 有 embedder + 门槛 2.0  ⇒ 照常两条 —— 没被当**分数下限**卡掉
//	   （融合分最多 1，真当下限用会一条不剩）；
//	③ 有 embedder + 门槛 -0.5 ⇒ 照常两条 —— 没被当**距离上限**卡掉
//	   （距离 ∈ [0,2]，负门槛在距离语义下一条都剩不下）。
func TestDistanceThresholdIsTheSecondMirrorDeviation(t *testing.T) {
	st, emb := directStore(t)
	ctx := context.Background()

	// ①
	floor, err := NewRetriever(ctx, &RetrieverConfig{
		Store: st, TopK: 5, DistanceThreshold: ptr(0.5),
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	docs, err := floor.Retrieve(ctx, docHot)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("无 embedder + 门槛 0.5 返回 %d 条，want 1 "+
			"(docHot 的词法分 1.0，docCold 是 0)", len(docs))
	}

	// ②③
	for _, tc := range []struct {
		th        float64
		ifApplied string
	}{
		{2.0, "分数下限会把全部滤掉（融合分最多 1）"},
		{-0.5, "距离上限会把全部滤掉（距离 ∈ [0,2]）"},
	} {
		strict, err := NewRetriever(ctx, &RetrieverConfig{
			Store: st, Embedding: emb, TopK: 5, DistanceThreshold: ptr(tc.th),
		})
		if err != nil {
			t.Fatalf("NewRetriever(%v): %v", tc.th, err)
		}
		docs, err := strict.Retrieve(ctx, "热更新 装配 成功 发布")
		if err != nil {
			t.Fatalf("Retrieve(%v): %v", tc.th, err)
		}
		if len(docs) != 2 {
			t.Fatalf("有 embedder + 门槛 %v 返回 %d 条，want 2（必须被忽略：%s）",
				tc.th, len(docs), tc.ifApplied)
		}
	}
}

// ── 批量 ─────────────────────────────────────────────────────────────────

// ★ 条数上限挡不住体积。docs/ 的 p95 是 1505 字符，8 条一批就是 12000 ——
//
//	那一批会让 llama-server 的 `-c` 装不下，**进程直接死**（实测）。
func TestIndexerRespectsCharBudget(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix, err := NewIndexer(context.Background(), &IndexerConfig{
		Store: st, Embedding: emb,
		BatchSize:     10, // 条数上限故意放宽
		MaxBatchChars: 100,
	})
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}

	var docs []*schema.Document
	for i := 0; i < 5; i++ {
		docs = append(docs, newDoc(string(rune('a'+i)), string(make([]byte, 90)), nil))
	}
	if _, err := ix.Store(context.Background(), docs); err != nil {
		t.Fatalf("Store: %v", err)
	}
	for _, n := range emb.batches() {
		if n > 2 {
			t.Errorf("a request carried %d texts, over the char budget of 100", n)
		}
	}
	if got := len(emb.batches()); got < 3 {
		t.Errorf("the char budget produced %d requests, want at least 3", got)
	}
}

// ★ 单块超过上限时**单独成批**，不报错 —— 那是切分粒度的问题，报在这里会
//
//	让用户以为是自己配错了。
func TestIndexerOversizedChunkGoesAlone(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix, err := NewIndexer(context.Background(), &IndexerConfig{
		Store: st, Embedding: emb, BatchSize: 10, MaxBatchChars: 10,
	})
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := ix.Store(context.Background(), []*schema.Document{
		newDoc("big", string(make([]byte, 5000)), nil),
		newDoc("small", "tiny", nil),
	}); err != nil {
		t.Fatalf("an oversized chunk must not be an error: %v", err)
	}
	if got := emb.batches(); len(got) != 2 {
		t.Errorf("batches = %v, want the oversized chunk alone then the small one", got)
	}
}

// ★ 失败信息必须指向可操作的方向：只说「embedding failed」的话，用户看到的
//
//	是一个放不下请求的端点，而真正的原因是我们塞了太多文本进去。
func TestEmbeddingFailureNamesTheLimits(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	emb.failWhen = func(int) error { return errBoom }
	ix := newTestIndexer(t, st, emb, 10)

	_, err := ix.Store(context.Background(), []*schema.Document{newDoc("x", "hello", nil)})
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"max_batch_chars", "batch_size", "context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func fieldOf(t *testing.T, typ reflect.Type, name string) reflect.StructField {
	t.Helper()
	f, ok := typ.FieldByName(name)
	if !ok {
		t.Fatalf("%s has no field %s", typ.Name(), name)
	}
	return f
}

// TestBatchSizeStructTag 与 Redis 版同名同标签 —— 它会被 JSON 序列化到
// 配置文件里，标签漂了会让已落盘的配置读不出来。
func TestBatchSizeStructTag(t *testing.T) {
	f, ok := reflect.TypeOf(IndexerConfig{}).FieldByName("BatchSize")
	if !ok {
		t.Fatal("no BatchSize field")
	}
	if got := f.Tag.Get("json"); got != "batch_size" {
		t.Errorf("BatchSize json tag = %q, want %q", got, "batch_size")
	}
}

// TestDefaultsMatchRedis 核对缺省值与 Redis 版一致。
func TestDefaultsMatchRedis(t *testing.T) {
	emb := newFakeEmbedder(8)
	st := newTestStore(t)

	ic, err := NewIndexer(context.Background(), &IndexerConfig{Store: st, Embedding: emb})
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if ic.config.BatchSize != 10 {
		t.Errorf("IndexerConfig.BatchSize default = %d, want 10 (redis: batchSize == 0 → 10)",
			ic.config.BatchSize)
	}

	rc, err := NewRetriever(context.Background(), &RetrieverConfig{Store: st, Embedding: emb})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	if rc.config.TopK != 5 {
		t.Errorf("RetrieverConfig.TopK default = %d, want 5 (redis: topK == 0 → 5)", rc.config.TopK)
	}
	if rc.config.VectorField != "vector_content" {
		t.Errorf("VectorField default = %q, want %q (redis: defaultReturnFieldVectorContent)",
			rc.config.VectorField, "vector_content")
	}
	if len(rc.config.ReturnFields) != 2 ||
		rc.config.ReturnFields[0] != "content" ||
		rc.config.ReturnFields[1] != "vector_content" {
		t.Errorf("ReturnFields default = %v, want [content vector_content]", rc.config.ReturnFields)
	}
	if rc.config.DistanceThreshold != nil {
		t.Errorf("DistanceThreshold default = %v, want nil", *rc.config.DistanceThreshold)
	}
}

// assertFields 比对本包配置结构体与 Redis 版的字段集合。
func assertFields(t *testing.T, name string, typ reflect.Type, want map[string]string,
	redisFields []string) {

	t.Helper()

	// 1) 本包的字段集合必须恰好是表里那些 —— 多一个少一个都是偏离。
	if typ.NumField() != len(want) {
		var got []string
		for i := 0; i < typ.NumField(); i++ {
			got = append(got, typ.Field(i).Name)
		}
		t.Fatalf("%s has fields %v, want exactly %v", name, got, keys(want))
	}

	// 2) 每个字段都必须在表里，且都指向一个真实的 Redis 字段或显式豁免。
	mirrored := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		dest, ok := want[f.Name]
		if !ok {
			t.Errorf("%s.%s is not accounted for in the mirror table", name, f.Name)
			continue
		}
		if dest == "-" {
			continue
		}
		if !contains(redisFields, dest) {
			t.Errorf("%s.%s mirrors %q, which no longer exists in the Redis config", name, f.Name, dest)
			continue
		}
		mirrored[dest] = true
	}

	// 3) 反向：Redis 版有、本包声称不提供的，必须真的一个都没镜像。
	for _, rf := range redisFields {
		if mirrored[rf] {
			continue
		}
		t.Logf("%s does not mirror Redis %s.%s (documented in docs/kb-design.md §6.1)",
			name, "IndexerConfig", rf)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

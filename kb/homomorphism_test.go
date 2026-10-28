package kb

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

var (
	// eino-ext/components/indexer/redis.IndexerConfig
	redisIndexerFields = []string{
		"Client",
		"KeyPrefix",
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
		"Dialect",
		"ReturnFields",      //
		"DocumentConverter", //
		"TopK",              //
		"Embedding",         //
	}
)

func TestIndexerConfigMirrorsRedis(t *testing.T) {
	want := map[string]string{
		"Store":            "Client",
		"DocumentToFields": "DocumentToHashes",
		"BatchSize":        "BatchSize",
		"Embedding":        "Embedding",
		"MaxBatchChars":    "-",
	}
	assertFields(t, "IndexerConfig", reflect.TypeOf(IndexerConfig{}), want, redisIndexerFields)
}

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

	dt := fieldOf(t, ret, "DistanceThreshold").Type
	if dt.Kind() != reflect.Ptr {
		t.Fatalf("RetrieverConfig.DistanceThreshold kind = %s, want pointer "+
			"(nil and 0 mean different things)", dt.Kind())
	}
	if dt.Elem().Kind() != reflect.Float64 {
		t.Errorf("DistanceThreshold elem = %s, want float64", dt.Elem())
	}
}

func TestIndexerConfigAdditionsAreDeclared(t *testing.T) {
	typ := reflect.TypeOf(IndexerConfig{})
	for _, name := range []string{"MaxBatchChars"} {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("%s is missing; it was added on purpose and must survive a backend swap", name)
		}
	}
}

func TestEmbeddingOptionalIsTheFirstMirrorDeviation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ix, err := NewIndexer(ctx, &IndexerConfig{Store: st, Embedding: nil, BatchSize: 10})
	if err != nil {
		t.Fatalf("indexer with no embedder was rejected (the Redis version rejects it, the File version must not): %v", err)
	}
	if _, err := ix.Store(ctx, []*schema.Document{newDoc("a.md", "热更新 装配", nil)}); err != nil {
		t.Fatalf("store without an embedder: %v", err)
	}
	if got := len(st.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1 — the catalog must be written even without a model", got)
	}
	if v := st.Sidecar().Entries["a.md"].Vector; len(v) != 0 {
		t.Errorf("vector len = %d, want 0 — no vector is written without an embedder", len(v))
	}

	if _, err := NewRetriever(ctx, &RetrieverConfig{Store: st}); err != nil {
		t.Fatalf("retriever with no embedder was rejected: %v", err)
	}
}

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
		t.Fatalf("no embedder + threshold 0.5 returned %d docs, want 1 "+
			"(docHot's lexical score is 1.0, docCold is 0)", len(docs))
	}

	// ②③
	for _, tc := range []struct {
		th        float64
		ifApplied string
	}{
		{2.0, "the score floor filters everything out (the fused score is at most 1)"},
		{-0.5, "the distance ceiling filters everything out (the distance is in [0,2])"},
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
			t.Fatalf("with an embedder + threshold %v returned %d docs, want 2 (it must be ignored: %s)",
				tc.th, len(docs), tc.ifApplied)
		}
	}
}

func TestIndexerRespectsCharBudget(t *testing.T) {
	st := newTestStore(t)
	emb := newFakeEmbedder(8)
	ix, err := NewIndexer(context.Background(), &IndexerConfig{
		Store: st, Embedding: emb,
		BatchSize:     10,
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

func TestBatchSizeStructTag(t *testing.T) {
	f, ok := reflect.TypeOf(IndexerConfig{}).FieldByName("BatchSize")
	if !ok {
		t.Fatal("no BatchSize field")
	}
	if got := f.Tag.Get("json"); got != "batch_size" {
		t.Errorf("BatchSize json tag = %q, want %q", got, "batch_size")
	}
}

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

func assertFields(t *testing.T, name string, typ reflect.Type, want map[string]string,
	redisFields []string) {

	t.Helper()

	if typ.NumField() != len(want) {
		var got []string
		for i := 0; i < typ.NumField(); i++ {
			got = append(got, typ.Field(i).Name)
		}
		t.Fatalf("%s has fields %v, want exactly %v", name, got, keys(want))
	}

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

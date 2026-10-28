package build

import (
	"context"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/tool"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/kb"
)

// main 这一层几乎没有测试 —— 它大多是「把已经测过的东西接起来」。但知识库
// 的工具装配是个例外：接线本身承载一条不变量，而它**写错了不报错**。

// wordEmbedder 是确定性的词袋替身：共享词的文本真的更相似，TopK 才可断言。
//
// ★ 它带 Dimensions() —— 装配层靠它做「换模型了没」的比对。
type wordEmbedder struct{ dim int }

func (w wordEmbedder) EmbedStrings(_ context.Context, texts []string,
	_ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		v := make([]float64, w.dim)
		for _, word := range strings.Fields(strings.ToLower(t)) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(word))
			v[int(h.Sum32()%uint32(w.dim))]++
		}
		var n float64
		for _, x := range v {
			n += x * x
		}
		if n > 0 {
			n = math.Sqrt(n)
			for j := range v {
				v[j] /= n
			}
		}
		out[i] = v
	}
	return out, nil
}

func (w wordEmbedder) Dimensions() int { return w.dim }

func kbConfig(corpus, model string) *spec.Config {
	c := &spec.Config{Models: map[string]*decl.Model{}}
	c.KB = decl.KB{
		Corpus:        corpus,
		Index:         "test",
		Model:         model,
		BatchSize:     10,
		MaxBatchChars: 1200,
		TopK:          5,
		Splitter: decl.KBSplitter{
			Headers: map[string]string{"##": "section"},
		},
	}
	return c
}

// ★ 这是本文件存在的原因：**两个工具必须共用同一个 Store 实例**。
//
// 各建一份的话，`kb_index` 写完之后 `kb_search` 读的仍是旧向量 —— 工具回报
// 「索引成功」，而检索结果没变。这与 `server/` 分区那个「保存成功但不生效」
// 是同一类失效，且同样不报错。
//
// 验法是走**跨进程**那条路径：另开一个 Store 实例往同一个目录写，然后看
// 工具能不能看到。共用实例 + refresh 两件事缺一不可。
func TestKBToolsShareOneStoreAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	corpus := filepath.Join(root, "docs")
	dataDir := filepath.Join(root, "state")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpus, "a.md"),
		[]byte("# 标题\n\n## 热更新\n\n先装配成功再发布。\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 装配需要模型档案；这里的 embedder 是注入的，所以只求引用能解析。
	c := kbConfig("docs", "local/embed")
	c.Models["local/embed"] = &decl.Model{ID: "embed", Kind: string(decl.ModelKindEmbedding), Dimensions: 32}

	emb := wordEmbedder{dim: 32}
	tools, err := buildKBToolsWithEmbedder(context.Background(), c, emb, root, dataDir)
	if err != nil {
		t.Fatalf("buildKBToolsWithEmbedder: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	names := map[string]tool.InvokableTool{}
	for _, tl := range tools {
		it, ok := tl.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%T is not invokable", tl)
		}
		info, err := it.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names[info.Name] = it
	}
	for _, want := range []string{kb.ToolSearch, kb.ToolIndex} {
		if names[want] == nil {
			t.Fatalf("tool %s was not registered; got %v", want, keysOf(names))
		}
	}

	// 空索引 → 检索说得出「还没索引」。
	out, err := names[kb.ToolSearch].InvokableRun(context.Background(), `{"query":"热更新"}`)
	if err != nil {
		t.Fatalf("search on an empty index: %v", err)
	}
	if !strings.Contains(out, "No matching passages") {
		t.Fatalf("empty index output = %q", out)
	}

	// 用索引工具写进去。
	if _, err := names[kb.ToolIndex].InvokableRun(context.Background(),
		`{"paths":["docs/a.md"]}`); err != nil {
		t.Fatalf("index: %v", err)
	}

	// ★ 同一个进程里立刻能搜到 —— 这是「共用 Store」的直接体现。
	out, err = names[kb.ToolSearch].InvokableRun(context.Background(), `{"query":"先装配成功 发布"}`)
	if err != nil {
		t.Fatalf("search after index: %v", err)
	}
	if !strings.Contains(out, "a.md") {
		t.Fatalf("search right after indexing returned nothing useful:\n%s", out)
	}

	// ★ 再走**另一个进程**的路径：另开一个 Store 往同一目录写，然后确认工具
	//   能看到。没有 refresh 的话这里会一直返回旧快照。
	other, err := kb.NewStore(filepath.Join(dataDir, spec.DirKBIndex, "test"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	otherIx, err := kb.NewIndexer(context.Background(), &kb.IndexerConfig{
		Store: other, Embedding: emb, BatchSize: 10, MaxBatchChars: 1200,
	})
	if err != nil {
		t.Fatalf("indexer: %v", err)
	}
	if _, err := kb.Index(context.Background(), otherIx, kb.IndexOptions{
		Corpus: corpus, Headers: c.KB.Splitter.Headers,
	}, []string{"a.md"}); err != nil {
		t.Fatalf("out-of-process index: %v", err)
	}

	out, err = names[kb.ToolSearch].InvokableRun(context.Background(), `{"query":"另一个进程写的 内容"}`)
	if err != nil {
		t.Fatalf("search after out-of-process write: %v", err)
	}
	if !strings.Contains(out, "score") {
		t.Fatalf("the tool did not see the out-of-process write:\n%s", out)
	}
}

func TestBuildKBToolsOffWhenCorpusUnset(t *testing.T) {
	c := &spec.Config{Models: map[string]*decl.Model{}}
	c.KB.Corpus = ""
	tools, err := buildKBToolsWithEmbedder(context.Background(), c, wordEmbedder{dim: 8}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("buildKBToolsWithEmbedder: %v", err)
	}
	// ★ 没配语料就**一个工具都不挂**：挂一个永远召回不到东西的 kb_search
	//   比不挂更糟 —— 模型会去用，然后拿到空结果，而不是干脆没有这个能力。
	if len(tools) != 0 {
		t.Fatalf("corpus is unset but %d tools were built", len(tools))
	}
}

// ★ 换成别的模型而索引还是旧的 —— 必须**在启动期**报错。跨模型的向量空间
// 不相容，检索会给出看起来正常的错结果。
func TestBuildKBToolsDetectsModelChange(t *testing.T) {
	root := t.TempDir()
	corpus := filepath.Join(root, "docs")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpus, "a.md"), []byte("## A\n\n内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "state")

	c := kbConfig("docs", "local/embed")
	c.Models["local/embed"] = &decl.Model{ID: "embed", Kind: string(decl.ModelKindEmbedding)}

	if _, err := buildKBToolsWithEmbedder(context.Background(), c, wordEmbedder{dim: 32}, root, dataDir); err != nil {
		t.Fatalf("first build: %v", err)
	}
	// 用同一份配置先建出索引。
	other, err := kb.NewStore(filepath.Join(dataDir, spec.DirKBIndex, "test"))
	if err != nil {
		t.Fatal(err)
	}
	// ★ 身份声明在 **Store** 上（装配层建索引前做的就是这件事），而不是
	//   每次调用传一个 model —— 身份只有一处。
	if err := other.SetIdentity("local/embed", 0); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
	ix, err := kb.NewIndexer(context.Background(), &kb.IndexerConfig{
		Store: other, Embedding: wordEmbedder{dim: 32}, BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kb.Index(context.Background(), ix, kb.IndexOptions{
		Corpus: corpus, Headers: c.KB.Splitter.Headers,
	}, nil); err != nil {
		t.Fatal(err)
	}

	// 现在把配置里的模型引用换掉 —— 索引里的向量还是旧模型产的。
	c2 := kbConfig("docs", "local/other")
	c2.Models["local/other"] = &decl.Model{ID: "other", Kind: string(decl.ModelKindEmbedding)}

	_, err = buildKBToolsWithEmbedder(context.Background(), c2, wordEmbedder{dim: 32}, root, dataDir)
	if err == nil {
		t.Fatal("a changed embedding model was accepted while the old index is still there")
	}
	if !strings.Contains(err.Error(), "delete") {
		t.Errorf("the error should say how to recover, got: %v", err)
	}
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

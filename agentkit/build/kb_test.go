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

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/kb"
)

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
		CorpusDir:     corpus,
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

	out, err := names[kb.ToolSearch].InvokableRun(context.Background(), `{"query":"热更新"}`)
	if err != nil {
		t.Fatalf("search on an empty index: %v", err)
	}
	if !strings.Contains(out, "No matching passages") {
		t.Fatalf("empty index output = %q", out)
	}

	if _, err := names[kb.ToolIndex].InvokableRun(context.Background(),
		`{"paths":["docs/a.md"]}`); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err = names[kb.ToolSearch].InvokableRun(context.Background(), `{"query":"先装配成功 发布"}`)
	if err != nil {
		t.Fatalf("search after index: %v", err)
	}
	if !strings.Contains(out, "a.md") {
		t.Fatalf("search right after indexing returned nothing useful:\n%s", out)
	}

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
		CorpusDir: corpus, Headers: c.KB.Splitter.Headers,
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
	c.KB.CorpusDir = ""
	tools, err := buildKBToolsWithEmbedder(context.Background(), c, wordEmbedder{dim: 8}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("buildKBToolsWithEmbedder: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("corpus is unset but %d tools were built", len(tools))
	}
}

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
	other, err := kb.NewStore(filepath.Join(dataDir, spec.DirKBIndex, "test"))
	if err != nil {
		t.Fatal(err)
	}
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
		CorpusDir: corpus, Headers: c.KB.Splitter.Headers,
	}, nil); err != nil {
		t.Fatal(err)
	}

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

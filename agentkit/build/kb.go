package build

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/kb"
)

func buildKBTools(ctx context.Context, c *spec.Config, projectRoot, dataDir string,
	log *slog.Logger, track func(io.Closer)) ([]tool.BaseTool, error) {
	if c == nil || c.KB.CorpusDir == "" {
		return nil, nil
	}
	prov, mdl, err := Resolve(c, c.KB.Model)
	if err != nil {
		return nil, err
	}
	emb, err := NewEmbedder(ctx, prov, mdl, log, configuredRequestDump(c))
	if err != nil {
		return nil, err
	}
	return buildKBToolsWithEmbedder(ctx, c, emb, projectRoot, dataDir, track)
}

func buildKBToolsWithEmbedder(ctx context.Context, c *spec.Config, emb embedding.Embedder,
	projectRoot, dataDir string, track func(io.Closer)) ([]tool.BaseTool, error) {

	if c == nil || c.KB.CorpusDir == "" {
		return nil, nil
	}

	indexName := c.KB.Index
	if indexName == "" {
		indexName = "default"
	}
	corpus := filepath.Join(projectRoot, c.KB.CorpusDir)

	store, err := kb.NewStore(filepath.Join(dataDir, spec.DirKBIndex, indexName))
	if err != nil {
		return nil, err
	}
	// ★ The store holds the run lock file open. Nothing else owns the store,
	//   so register it here or the handle outlives the build.
	if track != nil {
		track(store)
	}
	// ★ A failure below leaves no tools behind, so nobody would ever close
	//   the store those tools would have owned.
	built := false
	defer func() {
		if !built {
			_ = store.Close()
		}
	}()
	dim := 0
	if d, ok := emb.(interface{ Dimensions() int }); ok {
		dim = d.Dimensions()
	}
	if err := store.SetIdentity(c.KB.Model, dim); err != nil {
		return nil, err
	}

	ix, err := kb.NewIndexer(ctx, &kb.IndexerConfig{
		Store:         store,
		Embedding:     emb,
		BatchSize:     c.KB.BatchSize,
		MaxBatchChars: c.KB.MaxBatchChars,
	})
	if err != nil {
		return nil, err
	}
	rt, err := kb.NewRetriever(ctx, &kb.RetrieverConfig{
		Store:        store,
		Embedding:    emb,
		Index:        indexName,
		TopK:         c.KB.TopK,
		ReturnFields: kb.ResultFields,
	})
	if err != nil {
		return nil, err
	}

	search, err := kb.NewSearchTool(kb.SearchConfig{
		Retriever:   rt,
		Instruction: c.KB.QueryInstruction,
		TopK:        c.KB.TopK,
	})
	if err != nil {
		return nil, err
	}
	index, err := kb.NewIndexTool(kb.IndexToolConfig{
		Store:       store,
		Indexer:     ix,
		CorpusDir:   corpus,
		ProjectRoot: projectRoot,
		Headers:     c.KB.Splitter.Headers,
	})
	if err != nil {
		return nil, err
	}
	built = true
	return []tool.BaseTool{search, index}, nil
}

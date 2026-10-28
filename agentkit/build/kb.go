package build

import (
	"context"
	"path/filepath"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/kb"
)

func buildKBTools(ctx context.Context, c *spec.Config, projectRoot, dataDir string) ([]tool.BaseTool, error) {
	if c == nil || c.KB.Corpus == "" {
		return nil, nil
	}
	prov, mdl, err := Resolve(c, c.KB.Model)
	if err != nil {
		return nil, err
	}
	emb, err := NewEmbedder(ctx, prov, mdl)
	if err != nil {
		return nil, err
	}
	return buildKBToolsWithEmbedder(ctx, c, emb, projectRoot, dataDir)
}

func buildKBToolsWithEmbedder(ctx context.Context, c *spec.Config, emb embedding.Embedder,
	projectRoot, dataDir string) ([]tool.BaseTool, error) {

	if c == nil || c.KB.Corpus == "" {
		return nil, nil
	}

	indexName := c.KB.Index
	if indexName == "" {
		indexName = "default"
	}
	corpus := filepath.Join(projectRoot, c.KB.Corpus)

	store, err := kb.NewStore(filepath.Join(dataDir, spec.DirKBIndex, indexName))
	if err != nil {
		return nil, err
	}
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
		Corpus:      corpus,
		ProjectRoot: projectRoot,
		Headers:     c.KB.Splitter.Headers,
	})
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{search, index}, nil
}

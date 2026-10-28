package build

import (
	"context"
	"path/filepath"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/tool"

	"openheron/agentkit/spec"
	"openheron/kb"
)

// 本文件是知识库工具的**装配细节**：建 embedder / store / indexer / retriever。
//
// ★ 它从 main.go 搬进来：这些细节需要配置（c.KB）、embedder 与两个路径，
//
//	而这三样 builder 都有。留在 main 会让「配置 → 产物」的一半在 main、
//	一半在 agent 包 —— 热更新时只重跑其中一半就成了静默失效。
//
// ★ 对外只通过 [kbToolProvider] 暴露给注册器；失败由注册器的 Optional 处理。

// buildKBTools 造知识库的两个工具。`kb.corpus` 为空时返回 nil（知识库关着）。
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

// buildKBToolsWithEmbedder 是 [buildKBTools] 的**可注入 embedder** 版本。
//
// ★ 拆开只为让装配本身能被测：buildKBTools 会在里面建真的 embedder（会去连
//
//	端点），于是「两个工具共用同一个 Store」这条不变量除了跑起整个进程之外
//	没法验。而它恰恰是最容易写错、且写错了不报错的那条（见下）。
func buildKBToolsWithEmbedder(ctx context.Context, c *spec.Config, emb embedding.Embedder,
	projectRoot, dataDir string) ([]tool.BaseTool, error) {

	// ★ 没配语料就一个工具都不挂。挂一个永远召回不到东西的 kb_search 比不挂
	//   更糟 —— 模型会去用它，然后拿到空结果，而不是干脆没有这个能力。
	//   buildKBTools 里也有一份同样的早退，那是为了**连 embedder 都不建**；
	//   这一份是为了让「直接写错」也无从发生。
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
	// ★ 身份比对在启动期做：换模型之后旧向量还在库里，而跨模型的检索给出的是
	//   看起来正常的错结果。启动时说一句，好过让模型去发现答得不对。
	//
	//   维度是**声明**（可能为 0 = 未知），所以走可选接口而不是往 Embedder
	//   上加重方法 —— eino 的接口不该被 openheron 的需要撑大。
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

package kb

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
)

const typIndexer = "File"

type IndexerConfig struct {
	Store *Store

	DocumentToFields DocumentToFields

	BatchSize int `json:"batch_size"`

	MaxBatchChars int `json:"max_batch_chars"`

	Embedding embedding.Embedder
}

var _ indexer.Indexer = (*FileIndexer)(nil)

type FileIndexer struct {
	config *IndexerConfig
}

func (i *FileIndexer) StoreRef() *Store { return i.config.Store }

func NewIndexer(_ context.Context, config *IndexerConfig) (*FileIndexer, error) {
	if config == nil {
		return nil, fmt.Errorf("kb: indexer config not provided")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("kb: store not provided for file indexer")
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.BatchSize < 0 {
		return nil, fmt.Errorf("kb: batch size must be positive, got %d", config.BatchSize)
	}
	if config.MaxBatchChars == 0 {
		config.MaxBatchChars = defaultMaxBatchChars
	}
	if config.MaxBatchChars < 0 {
		return nil, fmt.Errorf("kb: max batch chars must be positive, got %d", config.MaxBatchChars)
	}
	return &FileIndexer{config: config}, nil
}

func (i *FileIndexer) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) (
	ids []string, err error) {

	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	options := indexer.GetCommonOptions(&indexer.Options{
		Embedding: i.config.Embedding,
	}, opts...)

	ctx = callbacks.OnStart(ctx, &indexer.CallbackInput{Docs: docs})

	fresh, err := entriesOf(docs)
	if err != nil {
		return nil, err
	}
	res, err := i.Reconcile(ctx, ReconcileRequest{Fresh: fresh}, options.Embedding)
	if err != nil {
		return nil, err
	}
	_ = res

	ids = make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	callbacks.OnEnd(ctx, &indexer.CallbackOutput{IDs: ids})
	return ids, nil
}

func entriesOf(docs []*schema.Document) ([]Entry, error) {
	out := make([]Entry, 0, len(docs))
	for _, d := range docs {
		if d == nil || d.ID == "" {
			return nil, fmt.Errorf("kb: document id not set")
		}
		raw, ok := d.MetaData[MetaEntry]
		if !ok {
			return nil, fmt.Errorf("kb: document %s has no %s in metadata; "+
				"the file indexer needs the extracted Entry", d.ID, MetaEntry)
		}
		e, ok := raw.(Entry)
		if !ok {
			return nil, fmt.Errorf("kb: document %s metadata %s is %T, want kb.Entry",
				d.ID, MetaEntry, raw)
		}
		if e.Path == "" {
			e.Path = d.ID
		}
		if e.Path != d.ID {
			return nil, fmt.Errorf("kb: document %s carries entry for %s; "+
				"the entry path must equal the document id", d.ID, e.Path)
		}
		out = append(out, e)
	}
	return out, nil
}

func (i *FileIndexer) Reconcile(ctx context.Context, req ReconcileRequest, emb embedding.Embedder) (
	*ReconcileResult, error) {

	if emb == nil {
		emb = i.config.Embedding
	}

	req.Embed = func(ctx context.Context, entries []Entry) (map[string][]float32, error) {
		return i.embedAll(ctx, entries, emb)
	}
	return i.config.Store.Reconcile(ctx, req)
}

func (i *FileIndexer) embedAll(ctx context.Context, entries []Entry, emb embedding.Embedder) (
	map[string][]float32, error) {

	if emb == nil {
		out := map[string][]float32{}
		for _, e := range entries {
			out[e.Path] = nil
		}
		return out, nil
	}

	need := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if cur, ok := i.config.Store.derOf(e.Path); ok && cur.EmbedText == e.EmbedText() && len(cur.Vector) > 0 {
			continue
		}
		need = append(need, e)
	}

	out := make(map[string][]float32, len(need))
	batch := make([]Entry, 0, i.config.BatchSize)
	batchedChars := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		vecs, err := embedEntries(ctx, batch, emb, i.config.BatchSize)
		if err != nil {
			return err
		}
		for k, e := range batch {
			out[e.Path] = vecs[k]
		}
		batch = batch[:0]
		batchedChars = 0
		return nil
	}

	for _, e := range need {
		chars := len(e.EmbedText())
		if batchedChars+chars > i.config.MaxBatchChars && len(batch) > 0 {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		batch = append(batch, e)
		batchedChars += chars
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

func embedEntries(ctx context.Context, batch []Entry, emb embedding.Embedder, batchSize int) (
	[][]float32, error) {

	bodies := make([]string, len(batch))
	for i, e := range batch {
		bodies[i] = e.EmbedText()
	}
	ectx := embeddingCtx(ctx, emb)

	out := make([][]float32, len(bodies))
	for start := 0; start < len(bodies); start += batchSize {
		end := start + batchSize
		if end > len(bodies) {
			end = len(bodies)
		}
		vectors, err := emb.EmbedStrings(ectx, bodies[start:end])
		if err != nil {
			return nil, fmt.Errorf("kb: embedding %d texts (%d chars, batch limit %d texts / %d chars) "+
				"failed: %w; lower kb.batch_size or kb.max_batch_chars, or raise the "+
				"embedding server's context size",
				end-start, charsOf(bodies[start:end]), batchSize, charsOf(bodies), err)
		}
		if len(vectors) != end-start {
			return nil, fmt.Errorf("kb: invalid vector length, expected=%d, got=%d", end-start, len(vectors))
		}
		for k, v := range vectors {
			f32 := make([]float32, len(v))
			for i, x := range v {
				f32[i] = float32(x)
			}
			out[start+k] = f32
		}
	}
	return out, nil
}

func embeddingCtx(ctx context.Context, emb embedding.Embedder) context.Context {
	runInfo := &callbacks.RunInfo{Component: components.ComponentOfEmbedding}
	if t, ok := components.GetType(emb); ok {
		runInfo.Type = t
	}
	runInfo.Name = runInfo.Type + string(runInfo.Component)
	return callbacks.ReuseHandlers(ctx, runInfo)
}

func charsOf(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

func (i *FileIndexer) GetType() string { return typIndexer }

func (i *FileIndexer) IsCallbacksEnabled() bool { return true }

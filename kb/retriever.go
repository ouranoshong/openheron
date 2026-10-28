package kb

import (
	"context"
	"fmt"
	"github.com/ouranoshong/openheron/agentkit/obs"
	"path/filepath"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

const typRetriever = "File"

type RetrieverConfig struct {
	Store *Store

	Index string

	VectorField string

	DistanceThreshold *float64

	ReturnFields []string

	TopK int

	Embedding embedding.Embedder

	DocumentConverter func(ctx context.Context, m Match) (*schema.Document, error)
}

var _ retriever.Retriever = (*FileRetriever)(nil)

type Match struct {
	Entry  Entry
	Vector []float32
	Score  float64
}

type FileRetriever struct {
	config *RetrieverConfig
}

func NewRetriever(_ context.Context, config *RetrieverConfig) (*FileRetriever, error) {
	if config == nil {
		return nil, fmt.Errorf("kb: retriever config not provided")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("kb: store not provided for file retriever")
	}
	if config.TopK == 0 {
		config.TopK = defaultTopK
	}
	if config.TopK < 0 {
		return nil, fmt.Errorf("kb: top k must be positive, got %d", config.TopK)
	}
	if config.VectorField == "" {
		config.VectorField = FieldVector
	}
	if len(config.ReturnFields) == 0 {
		config.ReturnFields = []string{FieldContent, FieldVector}
	}
	if config.DocumentConverter == nil {
		config.DocumentConverter = DefaultDocumentConverter(config.ReturnFields)
	}
	if config.Embedding != nil && config.DistanceThreshold != nil {
		obs.Logger().Warn("[kb] DistanceThreshold is ignored while an embedding is configured; " +
			"the fused score has no cosine-distance dimension (see docs/kb-design.md §8.4)")
	}
	if config.Index != "" && config.Index != filepath.Base(config.Store.Dir()) {
		return nil, fmt.Errorf("kb: retriever index %q does not match store dir %q",
			config.Index, filepath.Base(config.Store.Dir()))
	}
	return &FileRetriever{config: config}, nil
}

func (r *FileRetriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (
	docs []*schema.Document, err error) {

	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	co := retriever.GetCommonOptions(&retriever.Options{
		TopK:           &r.config.TopK,
		ScoreThreshold: r.config.DistanceThreshold,
		Embedding:      r.config.Embedding,
	}, opts...)

	ctx = callbacks.OnStart(ctx, &retriever.CallbackInput{
		Query:          query,
		TopK:           *co.TopK,
		ScoreThreshold: co.ScoreThreshold,
	})

	terms := parseQuery(query)

	var qvec []float32
	if co.Embedding != nil {
		if len(r.config.Store.Docs()) > 0 && hasAnyVector(r.config.Store) {
			vectors, err := co.Embedding.EmbedStrings(embeddingCtx(ctx, co.Embedding), []string{query})
			if err != nil {
				return nil, fmt.Errorf("kb: embedding query failed: %w", err)
			}
			if len(vectors) != 1 {
				return nil, fmt.Errorf("kb: invalid vector count for one query, got=%d", len(vectors))
			}
			qvec = make([]float32, len(vectors[0]))
			for i, x := range vectors[0] {
				qvec[i] = float32(x)
			}
		}
	}

	hits := r.search(terms, *co.TopK, qvec, thresholdOf(co))

	docs = make([]*schema.Document, 0, len(hits))
	for _, h := range hits {
		d, err := r.config.DocumentConverter(ctx, h)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}

	callbacks.OnEnd(ctx, &retriever.CallbackOutput{Docs: docs})
	return docs, nil
}

func hasAnyVector(st *Store) bool {
	for _, d := range st.Docs() {
		if len(d.Vector) > 0 {
			return true
		}
	}
	return false
}

func (r *FileRetriever) search(terms queryTerms, topK int, qvec []float32, threshold *float64) []Match {
	docs := r.config.Store.Docs()
	out := make([]Match, 0, topK)
	for _, d := range docs {
		s := scoreDoc(terms, d.Entry, d.Vector, qvec)
		if threshold != nil && s < *threshold {
			continue
		}
		out = append(out, Match{Entry: d.Entry, Vector: d.Vector, Score: s})
	}
	sortMatches(out)
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func sortMatches(ms []Match) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0; j-- {
			a, b := &ms[j-1], &ms[j]
			better := a.Score < b.Score ||
				(a.Score == b.Score && a.Entry.Path > b.Entry.Path)
			if !better {
				break
			}
			*a, *b = *b, *a
		}
	}
}

func thresholdOf(co *retriever.Options) *float64 {
	if co.Embedding != nil {
		return nil
	}
	return co.ScoreThreshold
}

func DefaultDocumentConverter(returnFields []string) func(context.Context, Match) (*schema.Document, error) {
	return func(_ context.Context, hit Match) (*schema.Document, error) {
		d := &schema.Document{
			ID:       hit.Entry.Path,
			Content:  hit.Entry.EmbedText(),
			MetaData: map[string]any{},
		}
		for _, f := range returnFields {
			switch f {
			case FieldVector:
				if len(hit.Vector) == 0 {
					continue
				}
				out := make([]float64, len(hit.Vector))
				for i, x := range hit.Vector {
					out[i] = float64(x)
				}
				d = d.WithDenseVector(out)
			case FieldContent:
			case MetaSource:
				d.MetaData[MetaSource] = hit.Entry.Path
			case MetaEntry:
				d.MetaData[MetaEntry] = hit.Entry
			default:
				return nil, fmt.Errorf("kb: return field %q is not carried by the file retriever; "+
					"known fields: %s, %s, %s, %s", f, FieldContent, FieldVector, MetaSource, MetaEntry)
			}
		}
		if d.MetaData[MetaSource] == nil {
			d.MetaData[MetaSource] = hit.Entry.Path
		}
		d = d.WithScore(hit.Score)
		return d, nil
	}
}

func (r *FileRetriever) GetType() string { return typRetriever }

func (r *FileRetriever) IsCallbacksEnabled() bool { return true }

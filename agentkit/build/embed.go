package build

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	aclembed "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/embedding"

	"github.com/ouranoshong/openheron/agentkit/obs/httplog"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

const embedTypeName = "OpenAICompat"

type Embedder struct {
	inner *aclembed.EmbeddingClient
	dim   int
}

var _ embedding.Embedder = (*Embedder)(nil)

func NewEmbedder(ctx context.Context, p *decl.Provider, m *decl.Model,
	log *slog.Logger, requestDump bool) (*Embedder, error) {
	if p == nil {
		return nil, fmt.Errorf("llm: provider is nil")
	}
	if m == nil {
		return nil, fmt.Errorf("llm: model is nil")
	}
	if !m.IsEmbedding() {
		return nil, fmt.Errorf("llm: model %q is kind %q, not embedding; "+
			"a chat endpoint has no /embeddings route", m.ID, m.Kind)
	}

	timeout := p.Timeout.Duration()
	if m.Timeout.Duration() > 0 {
		timeout = m.Timeout.Duration()
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	cli, err := aclembed.NewEmbeddingClient(ctx, &aclembed.EmbeddingConfig{
		APIKey:  p.APIKey,
		BaseURL: p.BaseURL,
		Model:   m.ID,
		HTTPClient: httplog.Wrap(&http.Client{Timeout: timeout}, p.ID, log,
			httplog.WithRequestBodyDump(requestDump)),
	})
	if err != nil {
		return nil, fmt.Errorf("llm: build embedder for %q: %w", m.ID, err)
	}
	return &Embedder{inner: cli, dim: m.Dimensions}, nil
}

func (e *Embedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) (
	[][]float64, error) {
	return e.inner.EmbedStrings(ctx, texts, opts...)
}

func (e *Embedder) GetType() string { return embedTypeName }

func (e *Embedder) IsCallbacksEnabled() bool { return true }

func (e *Embedder) Dimensions() int { return e.dim }

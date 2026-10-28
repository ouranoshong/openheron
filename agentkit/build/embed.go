package build

import (
	"context"
	"fmt"
	"net/http"
	"time"

	aclembed "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/embedding"

	"openheron/agentkit/spec/decl"
	"openheron/llm/common"
)

// embedTypeName 是这个 embedder 在回调链里自报的类型名。
//
// ★ 它必须是**协议**而不是厂商：观测里看到「OpenAICompat」才知道该用哪套
//
//	语义去解释那一段耗时，而看到「llama.cpp」只知道是谁实现的。
//	与 `Provider.Type` 的同一条理由（见 config/types.go）。
const embedTypeName = "OpenAICompat"

// Embedder 是按 `.openheron/llm/*.yaml` 装配出来的向量化组件。
//
// ★ 它**不经过 [common]** —— 那边打的是 `/chat/completions`，端点方言完全
//
//	不同。走 common 会得到一个「构造成功、每次调用都 404」的对象，而那类错
//	不响，只在真用时炸。
//
// 直接建在 `eino-ext/libs/acl/openai` 上（openheron 已经依赖它），而不是
// `components/embedding/openai` —— 后者只是同一个 client 加一层
// `GetType`/`IsCallbacksEnabled`，那两行这里自己写。
type Embedder struct {
	inner *aclembed.EmbeddingClient
	// dim 是模型**声明**的维度，0 = 未知（首次建索引时从响应里观测到）。
	dim int
}

var _ embedding.Embedder = (*Embedder)(nil)

// NewEmbedder 按一个 `kind: embedding` 的模型建向量化组件。
//
// ★ 它**要求** m 是 embedding 模型：config 的 validateKB 已经查过一遍，
//
//	这里是第二道 —— 因为「用 chat 模型建 embedder」的失败点在几百次
//	向量化之后才出现，而那时索引已经建了一半。
func NewEmbedder(ctx context.Context, p *decl.Provider, m *decl.Model) (*Embedder, error) {
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
		// ★ 不依赖 SDK 的隐式默认：各家差异极大，而 `context deadline
		//   exceeded` 的那种默认值极难归因。
		timeout = 2 * time.Minute
	}

	cli, err := aclembed.NewEmbeddingClient(ctx, &aclembed.EmbeddingConfig{
		APIKey:     p.APIKey,
		BaseURL:    p.BaseURL,
		Model:      m.ID,
		HTTPClient: common.WrapHTTPClient(&http.Client{Timeout: timeout}, p.ID),
		// ★ **Dimensions 故意不填。** 它在这个包里是「让服务端把输出截断到
		//   N 维」的**请求参数**（只有 text-embedding-3 支持），而
		//   decl.Model.Dimensions 是「这个模型本来就产 N 维」的**声明**。
		//   两个字段同名同类型而语义正交：实测 llama-server 收下
		//   `dimensions: 1024` 并静默忽略，换到 text-embedding-3 就是
		//   静默截断 —— 不报错、不告警，召回质量悄悄下降。
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

// GetType 让回调链里这段有名字 —— components.GetType 认它，
// 于是向量化在观测里不会是一段没名字的耗时。
func (e *Embedder) GetType() string { return embedTypeName }

func (e *Embedder) IsCallbacksEnabled() bool { return true }

// Dimensions 是这个模型**声明**的维度。0 = 未知。
//
// ★ 它只用来**建索引前**与索引 manifest 里记录的维度比对，不参与任何请求
//
//	（见 NewEmbedder 里关于 Dimensions 的注释）。
func (e *Embedder) Dimensions() int { return e.dim }

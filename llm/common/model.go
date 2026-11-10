// LogitBias / Modalities / Audio / User / ResponseFormat / MaxTokens /
package common

import (
	"context"
	"errors"
	"net/http"
	"time"

	acl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

var (
	_ model.ToolCallingChatModel = (*ChatModel)(nil)
	_ components.Typer           = (*ChatModel)(nil)
)

var (
	ErrNoConfig = errors.New("llm/common: config is nil")

	ErrNoBaseURL = errors.New("llm/common: base_url is required")
)

type Config struct {
	ProviderID string

	APIKey string

	BaseURL string

	Model string

	Timeout time.Duration

	HTTPClient *http.Client

	HintKeys []string

	ExtraFields map[string]any
}

type ChatModel struct {
	cli      *acl.Client
	id       string
	hintKeys []string
}

func New(ctx context.Context, c *Config) (*ChatModel, error) {
	if c == nil {
		return nil, ErrNoConfig
	}
	if c.BaseURL == "" {
		return nil, ErrNoBaseURL
	}

	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: c.Timeout}
	}
	// ★ The client arrives already wrapped by the assembly layer
	// (agentkit/obs/httplog). Request logging is observation, not protocol,
	// and this package keeps zero openheron dependencies.
	cli, err := acl.NewClient(ctx, &acl.Config{
		APIKey:      c.APIKey,
		BaseURL:     c.BaseURL,
		Model:       c.Model,
		HTTPClient:  hc,
		ExtraFields: c.ExtraFields,
	})
	if err != nil {
		return nil, err
	}
	return &ChatModel{cli: cli, id: c.ProviderID, hintKeys: c.HintKeys}, nil
}

func (cm *ChatModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	ctx = callbacks.EnsureRunInfo(ctx, cm.GetType(), components.ComponentOfChatModel)

	// ★ The shape is settled **here**, at the wire. A message that carries a
	//   body and a picture at the same time is refused by the client before
	//   the request is sent, and the history a transcript already holds can
	//   carry both — see [normalizeWireShape].
	in = normalizeWireShape(in)

	// ★ One request, no second attempt. Attachments are paths in the message,
	//
	//	not file or image blocks on the wire: there is nothing here to strip on
	//	failure. A provider that refuses a picture fails the turn with its own
	//	error, and the user can retry with a different model or prompt — an
	//	automatic retry without the picture used to hide that refusal.
	out, err := cm.cli.Generate(ctx, in, opts...)
	if err != nil {
		return nil, wrap(err, cm.hintKeys)
	}
	return out, nil
}

func (cm *ChatModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx = callbacks.EnsureRunInfo(ctx, cm.GetType(), components.ComponentOfChatModel)

	// ★ The same settling as Generate, and for the same reason.
	in = normalizeWireShape(in)

	// ★ The same one-request rule as Generate: a stream that fails before it
	//
	//	opens is reported, not retried with the attachment quietly removed.
	out, err := cm.cli.Stream(ctx, in, opts...)
	if err != nil {
		return nil, wrap(err, cm.hintKeys)
	}
	return attributingStream(out, cm.hintKeys), nil
}

func (cm *ChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cli, err := cm.cli.WithToolsForClient(tools)
	if err != nil {
		return nil, err
	}
	return &ChatModel{cli: cli, id: cm.id, hintKeys: cm.hintKeys}, nil
}

func (cm *ChatModel) GetType() string {
	if cm.id == "" {
		return "openai"
	}
	return cm.id
}

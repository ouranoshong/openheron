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
	hc = WrapHTTPClient(hc, c.ProviderID)

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

	out, err := cm.cli.Generate(ctx, in, opts...)
	return out, wrap(err, cm.hintKeys)
}

func (cm *ChatModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx = callbacks.EnsureRunInfo(ctx, cm.GetType(), components.ComponentOfChatModel)

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

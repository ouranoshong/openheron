package common

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/tidwall/sjson"
)

const nativeReasoningField = "reasoning_content"

type RequestOptions struct {
	Fields map[string]any

	Header map[string]string

	Model string

	Temperature *float32
	MaxTokens   *int
	TopP        *float32
	Stop        []string
}

func (r RequestOptions) Options() []model.Option {
	var opts []model.Option

	if r.Model != "" {
		opts = append(opts, model.WithModel(r.Model))
	}
	if r.Temperature != nil {
		opts = append(opts, model.WithTemperature(*r.Temperature))
	}
	if r.MaxTokens != nil {
		opts = append(opts, model.WithMaxTokens(*r.MaxTokens))
	}
	if r.TopP != nil {
		opts = append(opts, model.WithTopP(*r.TopP))
	}
	if len(r.Stop) > 0 {
		opts = append(opts, model.WithStop(r.Stop))
	}
	if len(r.Header) > 0 {
		opts = append(opts, WithExtraHeader(r.Header))
	}
	if len(r.Fields) > 0 {
		opts = append(opts, WithExtraFields(r.Fields))
	}
	return opts
}

func AttachRequestOptions(cm model.ToolCallingChatModel, r RequestOptions,
	field, from string) model.ToolCallingChatModel {

	opts := r.Options()

	if field != "" && field != nativeReasoningField {
		opts = append(opts, WithPayloadModifier(reasoningPayloadModifier(field, from)))
	}

	if len(opts) == 0 {
		return cm
	}
	return &optionedModel{inner: cm, opts: opts}
}

type optionedModel struct {
	inner model.ToolCallingChatModel
	opts  []model.Option
}

func (o *optionedModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	return o.inner.Generate(ctx, in, append(opts, o.opts...)...)
}

func (o *optionedModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return o.inner.Stream(ctx, in, append(opts, o.opts...)...)
}

func (o *optionedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := o.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &optionedModel{inner: inner, opts: o.opts}, nil
}

func (o *optionedModel) GetType() string {
	if t, ok := o.inner.(interface{ GetType() string }); ok {
		return t.GetType()
	}
	return ""
}

func reasoningPayloadModifier(field, from string) PayloadModifier {
	if from == "" {
		from = nativeReasoningField
	}
	return func(_ context.Context, rawBody []byte) ([]byte, error) {
		var raw struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := sonic.Unmarshal(rawBody, &raw); err != nil {
			return rawBody, nil
		}

		out := rawBody
		for i, m := range raw.Messages {
			if role, _ := m["role"].(string); role != "assistant" {
				continue
			}
			reasoning, _ := m[from].(string)
			if reasoning == "" {
				continue
			}
			b, err := sjson.SetBytes(out, fmt.Sprintf("messages.%d.%s", i, field), reasoning)
			if err != nil {
				return rawBody, nil
			}
			out = b

			if b, err = sjson.DeleteBytes(out, fmt.Sprintf("messages.%d.%s", i, from)); err == nil {
				out = b
			}
		}
		return out, nil
	}
}

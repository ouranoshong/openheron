package build

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

type stampedModel struct {
	inner model.ToolCallingChatModel
	ref   string
	tier  string
}

func stampModel(cm model.ToolCallingChatModel, ref, tier string) model.ToolCallingChatModel {
	if ref == "" && tier == "" {
		return cm
	}
	return &stampedModel{inner: cm, ref: ref, tier: tier}
}

func (s *stampedModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	msg, err := s.inner.Generate(ctx, in, opts...)
	if err == nil {
		message.StampModel(msg, s.ref, s.tier)
	}
	return msg, err
}

func (s *stampedModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := s.inner.Stream(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	first := true
	return schema.StreamReaderWithConvert(sr, func(m *schema.Message) (*schema.Message, error) {
		if first {
			first = false
			message.StampModel(m, s.ref, s.tier)
		}
		return m, nil
	}), nil
}

func (s *stampedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := s.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &stampedModel{inner: inner, ref: s.ref, tier: s.tier}, nil
}

func (s *stampedModel) GetType() string {
	if t, ok := s.inner.(interface{ GetType() string }); ok {
		return t.GetType()
	}
	return ""
}

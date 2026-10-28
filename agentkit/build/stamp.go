package build

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// stampedModel 是给响应消息**盖出处**的模型壳。
//
// ★ 存在的理由：一条消息落库之后，事后要能回答「这是哪个模型、哪个思考
//
//	档位产的」—— 而模型实例在装配时就把这两样定下了（ref 与档位），
//	事后查配置查不准（中途热更新过就查不到了）。
//
// ★ 盖在**响应消息**上而不是请求上：请求是「打算用」，响应是「真的用了」。
type stampedModel struct {
	inner model.ToolCallingChatModel
	ref   string
	tier  string
}

// stampModel 给模型套上盖戳的壳。
//
// ★ ref 为空时**原样返回** —— 没有模型信息可盖，多套一层壳只会让类型
//
//	断言与调试变复杂。
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

// Stream 同 [stampedModel.Generate]，但有个**必须只盖第一个 chunk**的理由。
//
// ★ eino 的 `schema.ConcatMessages` 会把各 chunk 的 `Extra` 合并，而
//
//	`internal.ConcatMaps` 对**同名键**是「收集成 []any」——
//	每个 chunk 都盖 ⇒ 合并后 `openheron_model` 变成一串重复值的切片，
//	读出来不是字符串。
//
//	只盖第一个 ⇒ 该键只出现一次 ⇒ 合并时原值保留 ✓
//	（`concatMaps` 里 `len(anyVals) == 1` 的分支）。
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

// WithTools **必须把壳转发下去** —— 同 [common.optionedModel.WithTools]：
// 漏了它，agent 绑了工具之后拿到的就是没盖戳的模型。
func (s *stampedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := s.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &stampedModel{inner: inner, ref: s.ref, tier: s.tier}, nil
}

// GetType 转发 —— 同 [common.optionedModel.GetType]。
func (s *stampedModel) GetType() string {
	if t, ok := s.inner.(interface{ GetType() string }); ok {
		return t.GetType()
	}
	return ""
}

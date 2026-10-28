package common

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/tidwall/sjson"
)

// nativeReasoningField 是 go-openai 原生的思维链字段名
// （ChatCompletionMessage.ReasoningContent 的 struct tag，写死在库里）。
//
// ★ 它**只作为默认值**出现在代码里；「从哪个字段搬到哪个字段」由配置说
//
//	（provider-id.yaml 的 reasoning_field / reasoning_field_from）——
//	端点特有的别名不进代码，见 TestNoVendorKnowledgeInCode。
const nativeReasoningField = "reasoning_content"

// RequestOptions 是一次调用要注入的**请求级**参数。
//
// ★ 为什么不烘进构造期：模型实例按 (provider, agent) 缓存，而思考档位
//
//	取决于「这个 agent × 这一档」。烘进构造就得为每个组合造一个模型实例，
//	连带重建 HTTP 连接 —— 而连接池是 provider 级共享的（见 Config.HTTPClient）。
type RequestOptions struct {
	// Fields 是**已经合完**的那一份端点方言字段：
	// provider.extra ← model.extra ← thinking[level]（后者覆盖前者）。
	//
	// ★ 必须是完整的一份而非局部，因为 WithExtraFields 是**替换**不是合并 ——
	//   只传档位那几个字段会把前两层一起丢掉。
	Fields map[string]any

	// Header 是自定义请求头（网关类端点常要）。
	Header map[string]string

	// Model 覆盖本次调用的模型名。
	//
	// ★ 它走 eino 的**通用** option 通道（acl 的 GetCommonOptions，
	//   chat_model.go:559 与 :588），所以**一个实例能服务多个模型**，
	//   不必为每个模型各建一个 ChatModel。
	Model string

	// Temperature / MaxTokens / TopP / Stop 同上，走通用 option 通道。
	Temperature *float32
	MaxTokens   *int
	TopP        *float32
	Stop        []string
}

// Options 把它翻译成 eino 的 option 列表。
//
// ★ 顺序有意义：WithExtraFields 必须**最后**带上完整的一份，
//
//	因为它对请求体是「按键覆盖」，不能靠它自己的顺序赢过前面积累的字段。
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

// AttachRequestOptions 给一个 ChatModel 挂上固定的请求级参数。
//
// field/from 是这个端点收发思维链的字段名（见 [reasoningPayloadModifier]）。
//
// ★ 什么都没配就**原样返回** cm：包一层会让类型断言与调试变复杂，
//
//	而「没配任何参数」是绝大多数端点的情况。
func AttachRequestOptions(cm model.ToolCallingChatModel, r RequestOptions,
	field, from string) model.ToolCallingChatModel {

	opts := r.Options()

	// 目标与源相同（或没配）⇒ 不需要改名器。
	if field != "" && field != nativeReasoningField {
		opts = append(opts, WithPayloadModifier(reasoningPayloadModifier(field, from)))
	}

	if len(opts) == 0 {
		return cm
	}
	return &optionedModel{inner: cm, opts: opts}
}

// optionedModel 是带请求级参数的模型壳。
//
// 它必须实现 4 个方法：Generate / Stream / WithTools（契约要求）+ GetType
// （components.Typer 上的**可选**方法，漏了 agent type 会退化成空串）。
type optionedModel struct {
	inner model.ToolCallingChatModel
	opts  []model.Option
}

// Generate 追加请求级参数后转发。
//
// ★ 追加在**调用方 opts 之后**：调用方（eino 内部）的显式指定应能覆盖
//
//	我们挂在壳上的默认值。
func (o *optionedModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	return o.inner.Generate(ctx, in, append(opts, o.opts...)...)
}

// Stream 同 [optionedModel.Generate]。
func (o *optionedModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return o.inner.Stream(ctx, in, append(opts, o.opts...)...)
}

// WithTools **必须把壳转发下去**。
//
// ★ 漏了它：一旦 agent 绑了工具，拿到的就是**没有请求级参数**的那个模型 ——
//
//	症状是「配了思考参数 / 方言参数，不报错也不生效」，极难排查。
func (o *optionedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := o.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &optionedModel{inner: inner, opts: o.opts}, nil
}

// GetType 转发 —— 见 [optionedModel] 的说明。
func (o *optionedModel) GetType() string {
	if t, ok := o.inner.(interface{ GetType() string }); ok {
		return t.GetType()
	}
	return ""
}

// reasoningPayloadModifier 改写**已序列化**的请求体，把历史 assistant 消息里的
// 思维链换成当前端点认识的字段名。
//
// ★ **为什么必须在 body 上改，而不是改 schema.Message**：
//
//	① ChatCompletionMessage.ReasoningContent 的 JSON 字段名**写死在 go-openai
//	   的 struct tag**（chat.go），ExtraFields 改不了它；
//	② schema.Message.Extra 在消息转换时**完全不被读**（acl 的 toOpenAIRole
//	   那段不碰 Extra）—— 塞进去会**静默丢失**。
//
// 而 RequestPayloadModifier 拿得到 rawBody，且 acl 在序列化之后才调它
// （chat_model.go:624 的 SetExtraFields 之后）—— 用 sjson 逐条改写，
// 是唯一能「把 A 端点的字段名换成 B 端点的」的路子。
//
// ★ **入参不修改 rawBody 之外的东西**：rawBody 每次请求都是新序列化的，
//
//	而 schema.Message 由调用方持有 —— 原地改会让「同一份历史第二次发往
//	另一个端点」带着上一次的改名结果。
func reasoningPayloadModifier(field, from string) PayloadModifier {
	if from == "" {
		from = nativeReasoningField
	}
	return func(_ context.Context, rawBody []byte) ([]byte, error) {
		// 源字段名从**配置**来，不从代码里猜 —— 猜就是把厂商方言写回代码。
		var raw struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := sonic.Unmarshal(rawBody, &raw); err != nil {
			// 解析不动就原样返回：宁可字段名不对，也不要把整个请求打坏。
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

			// ★ 换名后**必须删掉源字段**，否则两个名字都发出去 ——
			//   有些端点会因为不认识的字段直接 400。
			if b, err = sjson.DeleteBytes(out, fmt.Sprintf("messages.%d.%s", i, from)); err == nil {
				out = b
			}
		}
		return out, nil
	}
}

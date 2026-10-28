package common

import (
	"context"

	acl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// PayloadModifier 改写**已序列化之后**的请求体。
//
// ★ 它拿得到 rawBody，且 acl 在序列化之后才调它
// （`chat_model.go:624` 的 SetExtraFields 之后，`:722` 消费）——所以这是唯一
// 能「把 A 端点的字段名换成 B 端点的」的路子。
type PayloadModifier func(ctx context.Context, rawBody []byte) ([]byte, error)

// WithPayloadModifier 挂一个请求体改写器。
//
// ★ 承接 `eino-ext/components/model/openai` 的 WithRequestPayloadModifier，
// 但**收窄了签名**：上游那个带 `msg []*schema.Message` 参数，本包一个都没用。
//
// 少一个参数就少一个「以为它有用」的陷阱 —— 要改历史消息里的字段，
// 改 body 比改 schema.Message 可靠：schema.Message.Extra 在消息转换时
// **完全不被读**（acl 的 toOpenAIRole 那段不碰 Extra），塞进去会静默丢失。
func WithPayloadModifier(fn PayloadModifier) model.Option {
	return acl.WithRequestPayloadModifier(
		func(ctx context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
			return fn(ctx, rawBody)
		})
}

// WithExtraFields 注入**构造期**的端点方言字段。
//
// ★ **替换**语义而非合并 —— 调用方必须把 provider / model / 档位三层
// 合完的那一份整份递下来；只传档位那几个字段会把前两层的 extra 一起丢掉。
func WithExtraFields(fields map[string]any) model.Option {
	return acl.WithExtraFields(fields)
}

// WithExtraHeader 注入自定义请求头。
//
// 保留它是因为有些端点强制要求（多模型网关常要一个 Referer 类的头做归属统计）。
// 请求级 header 覆盖构造期同名字段。
func WithExtraHeader(header map[string]string) model.Option {
	return acl.WithExtraHeader(header)
}

// Package common 是「OpenAI Chat Completions」的**协议实现**，供所有说这个
// 方言的端点共用（openai / deepseek / qwen / openrouter / ark 兼容端点 /
// 任意自建网关）。
//
// ★ **它不装方言**：端点、字段名、形状、取值全在 `.openheron/llm/<id>.yaml`，
// 由上一层的 provider 查表后经 [Config.ExtraFields] 与
// [AttachRequestOptions] 注入。本包只负责请求组装、流式解析、工具绑定、
// 错误归因。
//
// 命名用 common（协议共有）而不是 vendor（方言）：方言在上一层，这里只有协议。
//
// # 来源与改动
//
// 本包由 `eino-ext/components/model/openai` 吸收而来（该包 Apache-2.0，
// Copyright 2024 CloudWeGo Authors；本仓的许可声明见仓库根的 LICENSE）。相对上游做了：
//
//   - [Config] 23 字段 → 7：删掉零消费方字段（ByAzure / APIVersion / Seed /
//     LogitBias / Modalities / Audio / User / ResponseFormat / MaxTokens /
//     Temperature / TopP / Stop …）。采样参数改走 eino 通用请求级 option
//     （model.WithTemperature 等），厂商私有参数走 ExtraFields，两条路都不经过它们。
//   - option 由 7 个收敛为 3 个，且 [WithRequestPayloadModifier] 的签名被收窄
//     （见 option.go）。
//   - 新增 [AttachRequestOptions] 与思维链字段改名（见 request.go）。
//   - 新增分层错误归因与流式错误归因（见 error.go / stream.go）。
//   - [ChatModel.GetType] 返回 provider-id 而非固定串，便于链路追踪区分实际
//     在跟哪个端点说话。
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

// 编译期断言：本类型必须满足 eino 的工具调用模型契约。
var (
	_ model.ToolCallingChatModel = (*ChatModel)(nil)
	_ components.Typer           = (*ChatModel)(nil)
)

var (
	// ErrNoConfig 表示没有传配置。**不允许返回 (nil, nil)** ——
	// 那会让故障推迟到第一次模型调用才以难懂的形态爆出来。
	ErrNoConfig = errors.New("llm/common: config is nil")

	// ErrNoBaseURL 表示端点地址为空。代码里**零内置端点**：内置一份是
	// 会过期的抄本，而端点恰恰是最该由用户掌控的（私有网关 / 区域端点 / 代理）。
	ErrNoBaseURL = errors.New("llm/common: base_url is required")
)

// Config 是构造 [ChatModel] 所需的**全部**参数。
//
// ★ 它是上游 23 个字段的**子集**——只留真正会传的。被删掉的字段不是
// 「以后可能用」，而是**零消费方**：采样参数走 eino 通用请求级 option，
// 厂商私有参数走 [Config.ExtraFields]，两条路都不经过它们。
type Config struct {
	// ProviderID 是 `.openheron/llm/<id>.yaml` 的文件名（不含扩展名）。
	// 它**只用于日志与链路追踪**——代码里没有按它分支的地方。
	//
	// ★ 因为 [ChatModel.GetType] 返回它，回调与 trace 里能看出这次调用
	// 实际在跟哪个端点说话，而不是一律显示 "openai"。
	ProviderID string

	// APIKey 是端点凭据。**空 = 无鉴权**（自建网关 / 本地 ollama 常见）。
	APIKey string

	// BaseURL 是**主机（可带前缀）**。
	//
	// ★ **不要写 /chat/completions** —— go-openai 自己会拼
	// （`client.go:290` + `chat.go:29`），写成完整路径会得到
	// `…/chat/completions/chat/completions`。
	BaseURL string

	// Model 是端点侧的模型标识。多模型网关里形如 `厂商/模型`。
	//
	// 它只是**默认值**——构造之后每次调用都能用 model.WithModel 覆盖
	// （acl 走通用 options 通道，见 chat_model.go:559 与 :588），
	// 所以一个实例就能服务同一 provider 下的多个模型。
	Model string

	// Timeout 是这个端点的超时。**不能依赖 SDK 的隐式默认**（各家差异极大，
	// Ark 10 分钟、DeepSeek 5 分钟）——排查 `context deadline exceeded` 时
	// 那种默认值极难归因。
	Timeout time.Duration

	// HTTPClient 由调用方构造并注入。
	//
	// ★ 它存在的意义是**连接池共享**：同一个 provider 下的多个模型共用一个
	// client，否则每建一个模型实例就多一个连接池（acl 在 HTTPClient 为 nil 时
	// 会各造一个，见 chat_model.go:203-207）。
	//
	// nil = 按 Timeout 现造一个。
	HTTPClient *http.Client

	// HintKeys 是**这个端点使用的厂商字段名**列表（思考类参数等）。
	//
	// ★ 它们只用于「400 里出现了你配的字段名」时给出准确提示。
	//   协议层不内置任何一家��字段名 —— 那是 `.openheron/llm/<id>.yaml` 的事实，
	//   见 TestNoVendorKnowledgeInCode。
	HintKeys []string

	// ExtraFields 是**构造期**注入的端点方言，原样进请求体。
	//
	// ★ **替换**语义而非合并（`go-openai/chat.go:415`），所以调用方必须把
	// provider / model / 档位三层合完的那一份整份递下来；只传其中几项会
	// 把其余的丢掉。
	ExtraFields map[string]any
}

// ChatModel 是全部 OpenAI 兼容端点共用的**唯一**实现。
//
// 它满足 eino 的三个方法（`components/model/interface.go:36`、`:99`）：
// Generate / Stream / WithTools。另实现可选的 [components.Typer]。
type ChatModel struct {
	cli      *acl.Client
	id       string
	hintKeys []string
}

// New 按 [Config] 造一个 ChatModel。
//
// 两条启动期硬校验见 [ErrNoConfig] / [ErrNoBaseURL] 的说明。
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
	// ★ 日志挂在最终请求体上（见 [WrapHTTPClient]）：默认一行摘要，
	// OPENHERON_LOG_LLM_REQUESTS=1 时打印完整 body。
	hc = WrapHTTPClient(hc, c.ProviderID)

	// ★ acl.Config 里没有 Timeout 字段——超时只走 HTTPClient。上面已保证 hc 非 nil。
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

// Generate 走非流式。
func (cm *ChatModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	// ★ EnsureRunInfo 必须在**调下游之前**：它把 RunInfo 塞进 ctx，回调处理器
	// 靠它区分「模型调用」与其他节点。
	ctx = callbacks.EnsureRunInfo(ctx, cm.GetType(), components.ComponentOfChatModel)

	out, err := cm.cli.Generate(ctx, in, opts...)
	return out, wrap(err, cm.hintKeys)
}

// Stream 走流式。
//
// ★ 端点错误（429/401/…）**不从这里返回**，而是在读取流的途中由 Recv 抛出来。
//
//	⇒ 归因做在 [attributingStream]（读端）；只归因这里的返回 error 一条都抓不到。
func (cm *ChatModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx = callbacks.EnsureRunInfo(ctx, cm.GetType(), components.ComponentOfChatModel)

	out, err := cm.cli.Stream(ctx, in, opts...)
	if err != nil {
		return nil, wrap(err, cm.hintKeys)
	}
	return attributingStream(out, cm.hintKeys), nil
}

// WithTools 绑定工具，返回带工具的新实例。
//
// ★ 走 WithToolsForClient 而不是 acl.Client.WithTools：后者返回接口
// （model.ToolCallingChatModel），类型擦除会把 New 里注入的 ExtraFields 与
// HTTPClient 丢掉。
func (cm *ChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cli, err := cm.cli.WithToolsForClient(tools)
	if err != nil {
		return nil, err
	}
	return &ChatModel{cli: cli, id: cm.id, hintKeys: cm.hintKeys}, nil
}

// GetType 返回 provider-id。
//
// ★ 它是 components.Typer 上的**可选**方法（adk 里是类型断言，
// `adk/callback.go:131`）。包装器若不转发它，agent type 会退化成空串。
func (cm *ChatModel) GetType() string {
	if cm.id == "" {
		return "openai"
	}
	return cm.id
}

// Package llm 组装厂商模型。
//
// 方言（端点、字段名、思考档位）属于 `.openheron/llm/<id>.yaml`，协议实现属于
// [common]。本包是两者之间那一层：把一份声明翻译成 [common.Config]。
//
// # 三条装配路径
//
//   - [BuildModel]：按 `<provider>/<model>` 引用造一个**带请求级参数**的
//     模型实例（思考档位与采样参数都来自 agent 的声明）。三处装配
//     —— 入口 agent、子 agent、skill 覆盖模型 —— 都走它。
//   - [FromConfig]：按一份声明造模型实例，不带请求级参数。
//   - [ConfigFromEnv]：从环境变量拼一份声明 —— **过渡形态**，只服务于
//     「根 agent 没有声明模型」那条环境兜底路径（见 agentkit/build/snapshot.go）。
//
// ★ env 不读方言：端点与字段名属于配置，不属于代码。env 只提供连接所需
//
//	的四件事：哪家、什么地址、什么凭据、超时。
package build

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"openheron/llm/common"
)

// ErrNoBaseURL 表示环境变量里找不到端点地址。
var ErrNoBaseURL = errors.New("llm: no endpoint configured; set OPENAI_BASE_URL or ARK_BASE_URL")

// ConfigFromEnv 把现有环境变量翻译成一份 [common.Config]。
//
// ★ **刻意不读方言**：端点与字段名属于配置，不属于代码。env 只提供连接所需
//
//	的四件事：哪家、什么地址、什么凭据、超时。
//
// `MODEL_TYPE` 只做**端点选择**（openai / ark 兼容端点 / 任意自定义网关），
// 不引入任何厂商专属字段。
func ConfigFromEnv() (*common.Config, error) {
	providerID := strings.TrimSpace(os.Getenv("MODEL_TYPE"))
	if providerID == "" {
		providerID = "openai"
	}

	c := &common.Config{
		ProviderID: providerID,
		BaseURL:    firstEnv("OPENAI_BASE_URL", "ARK_BASE_URL"),
		APIKey:     firstEnv("OPENAI_API_KEY", "ARK_API_KEY"),
		Model:      firstEnv("OPENAI_MODEL", "OPENAI_MODEL_ID", "ARK_MODEL", "ARK_MODEL_ID"),
		Timeout:    5 * time.Minute,
	}

	if c.BaseURL == "" {
		return nil, ErrNoBaseURL
	}
	if d, ok := envDuration("MODEL_TIMEOUT"); ok {
		c.Timeout = d
	}
	// ★ 端点方言由环境变量以**原始 JSON** 给出，代码里不写任何厂商字段名。
	//
	//	需要显式关思考时（某些端点默认开着，不关就是在为用不到的输出付费）：
	//	  MODEL_EXTRA_JSON='{"thinking":{"type":"disabled"}}'
	//
	//	等 `.openheron/llm/<id>.yaml` 落地后，这里换成读配置的 extra —— 形状不变。
	if v := strings.TrimSpace(os.Getenv("MODEL_EXTRA_JSON")); v != "" {
		var fields map[string]any
		if err := json.Unmarshal([]byte(v), &fields); err != nil {
			return nil, errors.New("llm: MODEL_EXTRA_JSON is not a JSON object: " + err.Error())
		}
		c.ExtraFields = fields
	}
	return c, nil
}

func envDuration(key string) (time.Duration, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, false
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

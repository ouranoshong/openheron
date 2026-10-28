package llm_test

import (
	"testing"

	"openheron/agentkit/build"
	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
)

func testProvider() *decl.Provider {
	p := &decl.Provider{
		ID: "ex", Type: "openai_compat", BaseURL: "https://x", APIKey: "k",
		Extra: map[string]any{"stream": true},
	}
	p.Models = []*decl.Model{
		{
			ID:    "reasoner",
			Extra: map[string]any{"context": "long"},
			Reasoning: map[string]map[string]any{
				"off":  {},
				"low":  {"reasoning_effort": "low"},
				"high": {"reasoning_effort": "high", "thinking": map[string]any{"type": "enabled"}},
			},
			ReasoningDefault: "low",
		},
		{ID: "plain"},
	}
	return p
}

func modelOf(t *testing.T, p *decl.Provider, id string) *decl.Model {
	t.Helper()
	for _, m := range p.Models {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("model %s does not exist", id)
	return nil
}

// 档位名 → 端点字段形状，全在配置里；代码不认得任何一家的键名。
func TestReasoningLevelResolvesToEndpointShape(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")

	got, ok := m.LevelFields("high")
	if !ok {
		t.Fatal("high 应在词汇表里")
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("the gear field shape is wrong: %#v", got)
	}
	if _, ok := m.LevelFields("nonexistent"); ok {
		t.Fatal("不存在的档位应返回 false")
	}
}

func TestModelWithoutReasoningDeclaresNone(t *testing.T) {
	m := modelOf(t, testProvider(), "plain")
	if _, ok := m.LevelFields("high"); ok {
		t.Fatal("没写词汇表的模型不该解析出档位")
	}
}

// ★ 合并链是 provider.extra ← model.extra ← reasoning[level]，且必须是
//
//	**完整一份** —— WithExtraFields 是替换语义，只传一层会把其余丢掉。
func TestExtraMergeChainKeepsAllLayers(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{Reasoning: "high"})

	if ro.Fields["stream"] != true {
		t.Errorf("the provider layer lost its fields: %#v", ro.Fields)
	}
	if ro.Fields["context"] != "long" {
		t.Errorf("the model layer lost its fields: %#v", ro.Fields)
	}
	if ro.Fields["reasoning_effort"] != "high" {
		t.Errorf("the gear layer lost its fields: %#v", ro.Fields)
	}
	if ro.Model != "reasoner" {
		t.Errorf("the request-level model name is wrong: %q", ro.Model)
	}
}

// 顶层键覆盖：model.extra 的同名键赢过 provider.extra。
func TestExtraLaterLayerWins(t *testing.T) {
	p := testProvider()
	p.Extra["context"] = "short"
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if ro.Fields["context"] != "long" {
		t.Fatalf("the model layer must override the provider layer, got %#v", ro.Fields["context"])
	}
}

// 档位不在词汇表里 ⇒ 不注入，但也不报错（config 侧已记进 deferred）。
func TestUnknownLevelInjectsNothingAndDoesNotPanic(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{Reasoning: "turbo"})
	if _, bad := ro.Fields["reasoning_effort"]; bad {
		t.Fatalf("an unknown gear must inject no reasoning fields: %#v", ro.Fields)
	}
	if ro.Fields["stream"] != true {
		t.Fatalf("the other layers must be preserved: %#v", ro.Fields)
	}
}

// 空档位名 = 退到模型的 reasoning_default（此例是 low），而不是不注入。
// ★ 与 TestExtraMergeChainKeepsAllLayers 的 "high" 对照，证明显式档位能压过默认。
func TestEmptyLevelFallsBackToReasoningDefault(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if ro.Fields["reasoning_effort"] != "low" {
		t.Fatalf("an empty gear must fall back to reasoning_default=low, got %#v", ro.Fields)
	}
	// 方言与模型名仍要带上。
	if ro.Fields["stream"] != true || ro.Fields["context"] != "long" {
		t.Fatalf("downgrading the gear must not drop the other layers: %#v", ro.Fields)
	}
}

// 模型没声明 reasoning_default ⇒ 空档位才真的不注入任何思考字段。
func TestEmptyLevelWithoutDefaultInjectsNothing(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "plain")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if _, bad := ro.Fields["reasoning_effort"]; bad {
		t.Fatalf("with no default gear there must be no reasoning fields: %#v", ro.Fields)
	}
}

// 不支持思考的模型：写档位也照样只带方言。
func TestPlainModelIgnoresLevel(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "plain")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{Reasoning: "high"})
	if ro.Fields["stream"] != true {
		t.Fatalf("the provider layer must still be present: %#v", ro.Fields)
	}
	if _, bad := ro.Fields["reasoning_effort"]; bad {
		t.Fatalf("a model without reasoning support must not carry reasoning fields: %#v", ro.Fields)
	}
}

// agent 的采样参数进 typed 字段，且与档位共存。
func TestAgentSamplingParamsAreApplied(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")
	temp := float32(0.25)
	max := 512

	ro := build.RequestOptionsFor(p, m, build.CallIntent{
		Reasoning:   "high",
		Temperature: &temp,
		MaxTokens:   &max,
	})

	if ro.Temperature == nil || *ro.Temperature != temp {
		t.Fatalf("temperature was dropped: %v", ro.Temperature)
	}
	if ro.MaxTokens == nil || *ro.MaxTokens != max {
		t.Fatalf("max_tokens was dropped: %v", ro.MaxTokens)
	}
	if ro.Fields["reasoning_effort"] != "high" {
		t.Fatalf("the gear layer must survive agent params: %#v", ro.Fields)
	}
}

// ★ agent 参数赢过 extra 里的同名键。
//
//	不是「也带上」而是「删掉 extra 的那一份」：acl 在设置完 common options
//	之后才 SetExtraFields（chat_model.go:591 / :624），留下来的 extra 键
//	会把 typed 值覆盖掉 —— 两条都发出去，最终生效的是 extra。
func TestAgentSamplingParamsBeatExtra(t *testing.T) {
	p := testProvider()
	p.Extra["temperature"] = float64(0.9)
	p.Extra["max_tokens"] = float64(999)
	m := modelOf(t, p, "reasoner")
	temp := float32(0.1)
	max := 64

	ro := build.RequestOptionsFor(p, m, build.CallIntent{Temperature: &temp, MaxTokens: &max})

	if _, bad := ro.Fields["temperature"]; bad {
		t.Fatalf("extra.temperature must be dropped once the agent sets it: %#v", ro.Fields)
	}
	if _, bad := ro.Fields["max_tokens"]; bad {
		t.Fatalf("extra.max_tokens must be dropped once the agent sets it: %#v", ro.Fields)
	}
	if ro.Temperature == nil || *ro.Temperature != temp {
		t.Fatalf("temperature is wrong: %v", ro.Temperature)
	}
}

// 没写 agent 参数时，extra 里的同名键必须原样保留 —— 不能顺手清掉别人的配置。
func TestExtraSamplingKeysSurviveWithoutAgentParams(t *testing.T) {
	p := testProvider()
	p.Extra["temperature"] = float64(0.9)
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if ro.Fields["temperature"] != float64(0.9) {
		t.Fatalf("extra.temperature must be preserved when the agent says nothing: %#v", ro.Fields)
	}
	if ro.Temperature != nil {
		t.Fatalf("no typed temperature should be injected: %v", ro.Temperature)
	}
}

func TestReasoningFieldsDefaultToNative(t *testing.T) {
	// 没配 reasoning_field ⇒ 空串，协议层用它自己的原生字段名。
	field, from := build.ReasoningFields(&decl.Model{ID: "m"})
	if field != "" || from != "" {
		t.Fatalf("with no config it must return an empty string, got %q / %q", field, from)
	}
	// 配了目标但没配源 ⇒ 源取目标。
	field, from = build.ReasoningFields(&decl.Model{ReasoningField: "reasoning"})
	if field != "reasoning" || from != "reasoning" {
		t.Fatalf("the source must default to the target, got %q / %q", field, from)
	}
	// 两个都配了 ⇒ 各取各的。
	field, from = build.ReasoningFields(&decl.Model{ReasoningField: "reasoning", ReasoningFieldFrom: "reasoning_content"})
	if field != "reasoning" || from != "reasoning_content" {
		t.Fatalf("got %q / %q", field, from)
	}
}

func TestResolveReportsMissingProviderAndModel(t *testing.T) {
	cfg := &spec.Config{
		Providers: map[string]*decl.Provider{"ex": {ID: "ex"}},
		Models:    map[string]*decl.Model{"ex/fast": {ID: "fast"}},
	}
	if _, _, err := build.Resolve(cfg, "ex/fast"); err != nil {
		t.Fatalf("a declared ref must resolve: %v", err)
	}
	if _, _, err := build.Resolve(cfg, "other/fast"); err == nil {
		t.Fatal("未声明的 provider 应报错")
	}
	if _, _, err := build.Resolve(cfg, "ex/nope"); err == nil {
		t.Fatal("未声明的模型应报错（不静默退化）")
	}
	if _, _, err := build.Resolve(cfg, "no-slash"); err == nil {
		t.Fatal("缺 provider 前缀应报错")
	}
}

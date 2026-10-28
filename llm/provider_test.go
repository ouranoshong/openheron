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

func TestReasoningLevelResolvesToEndpointShape(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")

	got, ok := m.LevelFields("high")
	if !ok {
		t.Fatal("high must be in the vocabulary")
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("the gear field shape is wrong: %#v", got)
	}
	if _, ok := m.LevelFields("nonexistent"); ok {
		t.Fatal("an unknown tier must return false")
	}
}

func TestModelWithoutReasoningDeclaresNone(t *testing.T) {
	m := modelOf(t, testProvider(), "plain")
	if _, ok := m.LevelFields("high"); ok {
		t.Fatal("a model without a vocabulary must not resolve a tier")
	}
}

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

func TestExtraLaterLayerWins(t *testing.T) {
	p := testProvider()
	p.Extra["context"] = "short"
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if ro.Fields["context"] != "long" {
		t.Fatalf("the model layer must override the provider layer, got %#v", ro.Fields["context"])
	}
}

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

func TestEmptyLevelFallsBackToReasoningDefault(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "reasoner")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if ro.Fields["reasoning_effort"] != "low" {
		t.Fatalf("an empty gear must fall back to reasoning_default=low, got %#v", ro.Fields)
	}
	if ro.Fields["stream"] != true || ro.Fields["context"] != "long" {
		t.Fatalf("downgrading the gear must not drop the other layers: %#v", ro.Fields)
	}
}

func TestEmptyLevelWithoutDefaultInjectsNothing(t *testing.T) {
	p := testProvider()
	m := modelOf(t, p, "plain")

	ro := build.RequestOptionsFor(p, m, build.CallIntent{})
	if _, bad := ro.Fields["reasoning_effort"]; bad {
		t.Fatalf("with no default gear there must be no reasoning fields: %#v", ro.Fields)
	}
}

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
	field, from := build.ReasoningFields(&decl.Model{ID: "m"})
	if field != "" || from != "" {
		t.Fatalf("with no config it must return an empty string, got %q / %q", field, from)
	}
	field, from = build.ReasoningFields(&decl.Model{ReasoningField: "reasoning"})
	if field != "reasoning" || from != "reasoning" {
		t.Fatalf("the source must default to the target, got %q / %q", field, from)
	}
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
		t.Fatal("an undeclared provider must error")
	}
	if _, _, err := build.Resolve(cfg, "ex/nope"); err == nil {
		t.Fatal("an undeclared model must error (no silent degradation)")
	}
	if _, _, err := build.Resolve(cfg, "no-slash"); err == nil {
		t.Fatal("a missing provider prefix must error")
	}
}

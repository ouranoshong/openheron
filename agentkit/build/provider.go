package build

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/llm/common"
)

func FromConfig(ctx context.Context, root string, p *decl.Provider, m *decl.Model) (
	model.ToolCallingChatModel, error) {

	if p == nil {
		return nil, fmt.Errorf("llm: provider is nil")
	}

	if m != nil && m.IsEmbedding() {
		return nil, fmt.Errorf("llm: model %q is kind embedding and cannot be built as a chat model; "+
			"use llm.NewEmbedder", m.ID)
	}

	timeout := p.Timeout.Duration()
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	cfg := &common.Config{
		ProviderID:  p.ID,
		APIKey:      p.APIKey,
		BaseURL:     p.BaseURL,
		HTTPClient:  &http.Client{Timeout: timeout},
		HintKeys:    p.HintKeys,
		ExtraFields: p.Extra,
	}
	if m != nil {
		cfg.Model = m.ID
		if m.Timeout.Duration() > 0 {
			cfg.HTTPClient = &http.Client{Timeout: m.Timeout.Duration()}
		}
	}

	cm, err := common.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("llm: build provider %q: %w", p.ID, err)
	}
	return cm, nil
}

func Resolve(c *spec.Config, ref string) (*decl.Provider, *decl.Model, error) {
	r, err := spec.ParseRef(ref)
	if err != nil {
		return nil, nil, err
	}
	p, ok := c.Providers[r.ProviderID]
	if !ok {
		return nil, nil, fmt.Errorf("llm: provider %q is not declared under .openheron/llm/ (declared: %s)",
			r.ProviderID, joinProviderIDs(c))
	}
	m, ok := c.Models[ref]
	if !ok {
		return nil, nil, fmt.Errorf("llm: provider %q does not declare model %q", r.ProviderID, r.ModelID)
	}
	return p, m, nil
}

type CallIntent struct {
	Reasoning   string
	Temperature *float32
	MaxTokens   *int
}

func BuildModel(ctx context.Context, c *spec.Config, ref string, in CallIntent) (
	model.ToolCallingChatModel, error) {

	p, m, err := Resolve(c, ref)
	if err != nil {
		return nil, err
	}
	cm, err := FromConfig(ctx, c.ConfigRoot, p, m)
	if err != nil {
		return nil, err
	}
	field, from := ReasoningFields(m)
	cm = common.AttachRequestOptions(cm, RequestOptionsFor(p, m, in), field, from)
	return stampModel(cm, ref, effectiveTier(m, in)), nil
}

func effectiveTier(m *decl.Model, in CallIntent) string {
	level := in.Reasoning
	if level == "" && m != nil {
		level = m.ReasoningDefault
	}
	return level
}

func RequestOptionsFor(p *decl.Provider, m *decl.Model, in CallIntent) common.RequestOptions {
	level := effectiveTier(m, in)
	fields := shallowMerge(p.Extra, nil)
	if m != nil {
		fields = shallowMerge(fields, m.Extra)
		if lvl, ok := m.LevelFields(level); ok {
			fields = shallowMerge(fields, lvl)
		}
	}
	if in.Temperature != nil {
		delete(fields, "temperature")
	}
	if in.MaxTokens != nil {
		delete(fields, "max_tokens")
	}

	ro := common.RequestOptions{
		Fields:      fields,
		Temperature: in.Temperature,
		MaxTokens:   in.MaxTokens,
	}
	if m != nil {
		ro.Model = m.ID
	}
	return ro
}

func shallowMerge(a, b map[string]any) map[string]any {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func ReasoningFields(m *decl.Model) (field, from string) {
	if m == nil {
		return "", ""
	}
	from = m.ReasoningFieldFrom
	if from == "" {
		from = m.ReasoningField
	}
	return m.ReasoningField, from
}

func joinProviderIDs(c *spec.Config) string {
	if len(c.Providers) == 0 {
		return "none"
	}
	names := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		names = append(names, id)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func SkillsDir(root string) string { return filepath.Join(root, spec.DirSkills) }

package build

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

type fakeProvider struct {
	id       string
	optional bool
	tools    []tool.BaseTool
	err      error
	calls    int
}

func (p *fakeProvider) ID() string     { return p.id }
func (p *fakeProvider) Optional() bool { return p.optional }
func (p *fakeProvider) Provide(context.Context, toolEnv) ([]tool.BaseTool, error) {
	p.calls++
	return p.tools, p.err
}

type metadataOnlyTool struct{ name string }

func (t *metadataOnlyTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func registryOf(ps ...toolProvider) *toolRegistry { return &toolRegistry{providers: ps} }

func buildRegistry(t *testing.T, r *toolRegistry) ([]tool.BaseTool, error) {
	t.Helper()
	return r.Build(context.Background(), toolEnv{})
}

func TestToolRegistryKeepsOrder(t *testing.T) {
	r := registryOf(
		&fakeProvider{id: "a", tools: []tool.BaseTool{
			&stubTool{name: "a1"}, &stubTool{name: "a2"},
		}},
		&fakeProvider{id: "b", tools: []tool.BaseTool{&stubTool{name: "b1"}}},
	)
	tools, err := buildRegistry(t, r)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(namesOf(tools), ",")
	if got != "a1,a2,b1" {
		t.Fatalf("order = %q, want a1,a2,b1", got)
	}
}

func TestToolRegistryRejectsDuplicateNames(t *testing.T) {
	t.Run("across providers", func(t *testing.T) {
		_, err := buildRegistry(t, registryOf(
			&fakeProvider{id: "a", tools: []tool.BaseTool{&stubTool{name: "dup"}}},
			&fakeProvider{id: "b", tools: []tool.BaseTool{&stubTool{name: "dup"}}},
		))
		if err == nil || !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
			t.Fatalf("want a duplicate error naming both providers, got %v", err)
		}
	})
	t.Run("within one provider", func(t *testing.T) {
		_, err := buildRegistry(t, registryOf(
			&fakeProvider{id: "a", tools: []tool.BaseTool{
				&stubTool{name: "dup"}, &stubTool{name: "dup"},
			}},
		))
		if err == nil || !strings.Contains(err.Error(), "within") {
			t.Fatalf("want a within-provider duplicate error, got %v", err)
		}
	})
}

func TestToolRegistryRejectsReservedNames(t *testing.T) {
	_, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "a", tools: []tool.BaseTool{&stubTool{name: "read_file"}}},
	))
	if err == nil || !strings.Contains(err.Error(), "filesystem middleware") {
		t.Fatalf("want the owner named in the error, got %v", err)
	}
}

func TestToolRegistryRejectsMetadataOnlyTool(t *testing.T) {
	_, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "a", tools: []tool.BaseTool{&metadataOnlyTool{name: "fake"}}},
	))
	if err == nil || !strings.Contains(err.Error(), "metadata-only") {
		t.Fatalf("want a metadata-only error, got %v", err)
	}
}

func TestToolRegistryOptionalProviderDegrades(t *testing.T) {
	boom := errors.New("boom")
	tools, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "opt", optional: true, err: boom},
		&fakeProvider{id: "ok", tools: []tool.BaseTool{&stubTool{name: "ok"}}},
	))
	if err != nil {
		t.Fatalf("an optional provider must degrade, got %v", err)
	}
	if got := strings.Join(namesOf(tools), ","); got != "ok" {
		t.Fatalf("tools = %q, want just ok", got)
	}
}

func TestToolRegistryRequiredProviderFails(t *testing.T) {
	_, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "required", err: errors.New("boom")},
	))
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("want a required-provider error, got %v", err)
	}
}

func TestToolRegistryInvalidToolsAreFatal(t *testing.T) {
	t.Run("empty name even when optional", func(t *testing.T) {
		_, err := buildRegistry(t, registryOf(
			&fakeProvider{id: "opt", optional: true,
				tools: []tool.BaseTool{&stubTool{name: ""}}},
		))
		if err == nil {
			t.Fatal("an empty tool name must not be downgraded by Optional")
		}
	})
	t.Run("duplicate even when optional", func(t *testing.T) {
		_, err := buildRegistry(t, registryOf(
			&fakeProvider{id: "opt", optional: true,
				tools: []tool.BaseTool{&stubTool{name: "x"}, &stubTool{name: "x"}}},
		))
		if err == nil {
			t.Fatal("a duplicate must not be downgraded by Optional")
		}
	})
}

func TestToolRegistryIsAtomicPerProvider(t *testing.T) {
	tools, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "a", tools: []tool.BaseTool{
			&stubTool{name: "good"}, &stubTool{name: ""},
		}},
	))
	if err == nil {
		t.Fatal("the invalid batch must fail")
	}
	if len(tools) != 0 {
		t.Fatalf("a failed provider left %d tools behind", len(tools))
	}
}

func TestRegistryToolDefaultsToAllow(t *testing.T) {
	if got := NewPolicy(nil, nil).TierOf("brand_new_tool"); got != TierAllow {
		t.Fatalf("an unlisted registry tool = %q, want the allow fallback", got)
	}
}

func TestRegistryToolObeysDisabledTools(t *testing.T) {
	tools, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "a", tools: []tool.BaseTool{&stubTool{name: "new_tool"}}},
	))
	if err != nil {
		t.Fatal(err)
	}

	root := specTable(NewPolicy(nil, nil), decl.SubAgentSpec{ID: "main"}, nil)
	if kept, _ := root.Filter(tools); len(kept) != 1 {
		t.Fatalf("the root must see the tool, kept %v", namesOf(kept))
	}
	sub := specTable(NewPolicy(nil, nil),
		decl.SubAgentSpec{ID: "worker", DisabledTools: []string{"new_tool"}}, nil)
	if kept, removed := sub.Filter(tools); len(kept) != 0 || removed[LayerDisable] != 1 {
		t.Fatalf("the sub-agent must not see it: kept %v removed %v", namesOf(kept), removed)
	}
}

func TestRegistryToolObeysPolicyTiers(t *testing.T) {
	tools, err := buildRegistry(t, registryOf(
		&fakeProvider{id: "a", tools: []tool.BaseTool{&stubTool{name: "new_tool"}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPolicy(nil, nil)
	if got := p.TierOf("new_tool"); got != TierAllow {
		t.Fatalf("tier = %q, want allow", got)
	}
	deny := specTable(NewPolicy(nil, []string{"new_tool"}), decl.SubAgentSpec{ID: "main"}, nil)
	if kept, removed := deny.Filter(tools); len(kept) != 0 || removed[LayerDeny] != 1 {
		t.Fatalf("deny must remove it at the filter stage: kept %v removed %v", namesOf(kept), removed)
	}
}

func TestAddingAProviderNeedsNoAssemblyChange(t *testing.T) {
	cfg := minimalTestConfig()
	b := newTestBuilderWithConfig(t, cfg)
	b.tools = registryOf(&fakeProvider{id: "fake",
		tools: []tool.BaseTool{&stubTool{name: "fake_tool"}}})

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	if len(bc.inputs) == 0 {
		t.Fatal("no agent was built")
	}
	root := bc.inputs[0]
	if !strings.Contains(strings.Join(namesOf(root.ToolsConfig.Tools), ","), "fake_tool") {
		t.Fatalf("the provider's tool never reached ToolsConfig: %v",
			namesOf(root.ToolsConfig.Tools))
	}
}

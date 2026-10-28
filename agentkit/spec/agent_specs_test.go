package spec

import (
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"strings"
	"testing"
)

func TestEachAgentExpandsItsOwnTree(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")
	f.write(t, "agents/lead.md",
		"---\ndescription: Lead\nmodel: openai/fast\nsubagents: [architect]\n---\nLead\n")
	f.write(t, "agents/architect.md", "---\ndescription: A\nmodel: openai/fast\n---\nA\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [lead]\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	if got := specs["main"].Sub; len(got) != 1 || got[0].ID != "lead" {
		t.Fatalf("main's tree = %+v, want [lead]", got)
	}
	if got := specs["lead"].Sub; len(got) != 1 || got[0].ID != "architect" {
		t.Fatalf("lead's own tree = %+v, want [architect]", got)
	}
	if cfg.DefaultAgent() != "main" {
		t.Fatalf("the default agent = %q, want main", cfg.DefaultAgent())
	}
}

func TestEveryDeclaredAgentIsSelectable(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/lead.md", "---\ndescription: L\nmodel: openai/fast\n---\nL\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [lead]\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range cfg.AgentNames() {
		if _, ok := specs[id]; !ok {
			t.Fatalf("agent %q must have a spec of its own, got %v", id, cfg.AgentNames())
		}
	}
}

func TestDefaultAgentIsRequired(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/lead.md", "---\ndescription: L\nmodel: openai/fast\n---\nL\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("a config without main.md must be rejected")
	}
}

func TestAgentNobodyReferencesIsStillUsable(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/orphan.md", "---\ndescription: 孤儿\nmodel: openai/fast\n---\nO\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("an agent nobody references must still load: %v", err)
	}
	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := specs["orphan"]; !ok {
		t.Fatal("the orphan agent must have a spec of its own")
	}
}

func TestDepthIsValidatedPerAgent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 1\n")
	f.write(t, "agents/lead.md",
		"---\ndescription: Lead\nmodel: openai/fast\nsubagents: [coder]\n---\nLead\n")
	f.write(t, "agents/coder.md",
		"---\ndescription: C\nmodel: openai/fast\nsubagents: [deep]\n---\nC\n")
	f.write(t, "agents/deep.md", "---\ndescription: D\nmodel: openai/fast\n---\nD\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [lead]\n---\n")

	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "team nested to depth") {
		t.Fatalf("a team deeper than the cap must be rejected: %v", err)
	}
}

func TestToolsAreEnabledByThePresenceOfTheirFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.HTTP == nil {
		t.Fatal("tools/http.yaml was written, so it must be on")
	}
	if cfg.Tools.WebSearch != nil {
		t.Fatal("tools/web_search.yaml was not written, so it must be off")
	}
	if cfg.Tools.Wikipedia != nil {
		t.Fatal("tools/wikipedia.yaml was not written, so it must be off")
	}
	if got := cfg.EnabledToolNames(); len(got) != 1 || got[0] != decl.ToolNameHTTPGet {
		t.Fatalf("enabled tool names = %v, want [%s]", got, decl.ToolNameHTTPGet)
	}
}

func TestToolsAreOffWhenTheirFileIsAbsent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/web_search.yaml", "provider: duckduckgo\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.HTTP != nil {
		t.Fatal("no tools/http.yaml was written, so it must be off")
	}
	if cfg.Tools.WebSearch == nil {
		t.Fatal("tools/web_search.yaml was written, so it must be on")
	}
	if got := cfg.EnabledToolNames(); len(got) != 1 || got[0] != decl.ToolNameWebSearch {
		t.Fatalf("enabled tool names = %v, want [%s]", got, decl.ToolNameWebSearch)
	}
}

func TestNoToolsSectionMeansNoTools(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EnabledToolNames(); len(got) != 0 {
		t.Fatalf("enabled tool names = %v, want none", got)
	}
}

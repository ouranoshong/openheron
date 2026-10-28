package spec

import (
	"openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"testing"
)

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

func loadExample(t *testing.T) *Config {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", ".openheron-example"), filepath.Join(root, DefaultBaseDirName))

	svc, err := NewService(filepath.Join(root, DefaultBaseDirName), root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("the shipped example must load: %v", err)
	}
	return cfg
}

func TestExampleConfigLoads(t *testing.T) {
	cfg := loadExample(t)

	main := cfg.DefaultAgent()
	if main == "" {
		t.Fatal("the example must declare a main agent")
	}
	root, ok := cfg.Agents[main]
	if !ok {
		t.Fatalf("the main agent %q is missing from %v", main, cfg.AgentNames())
	}
	if got := root.SubAgents; len(got) != 3 {
		t.Fatalf("the example declares %d subagents, want 3 (%v)", len(got), got)
	}
}

func TestExampleConfigExposesEveryRole(t *testing.T) {
	cfg := loadExample(t)

	all, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatalf("AgentSpecs: %v", err)
	}
	specs := all[cfg.DefaultAgent()].Sub
	if len(specs) != 3 {
		t.Fatalf("the example's main agent has %d sub-agents, want 3", len(specs))
	}
	byID := map[string]decl.SubAgentSpec{}
	for _, s := range specs {
		byID[s.ID] = s
	}
	for _, want := range []string{"researcher", "coder", "reviewer"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("the example must expose the %q role, got %v", want, specs)
		}
	}
	if got := byID["researcher"].DisabledTools; len(got) != 3 {
		t.Fatalf("researcher must disable the writing tools, got %v", got)
	}
	if got := byID["coder"].DisabledTools; len(got) != 0 {
		t.Fatalf("coder must keep the writing tools, got %v", got)
	}
}

func TestExampleRolesCanBeTheSessionAgent(t *testing.T) {
	cfg := loadExample(t)

	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatalf("AgentSpecs: %v", err)
	}
	for _, want := range []string{"main", "researcher", "coder", "reviewer"} {
		found := false
		for id := range specs {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q must have a spec of its own, got %v", want, cfg.AgentNames())
		}
	}
}

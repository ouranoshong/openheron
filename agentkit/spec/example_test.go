package spec

import (
	"openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"testing"
)

// copyTree 复制一棵树（样例里有子目录与 .gitignore）。
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

// loadExample 把 .openheron-example 复制成一份配置根并加载。
//
// ★ 配置根指向临时目录里复制出来的那份：否则会命中开发者真实的 ~/.openheron，
//
//	测试就会跑在别人的机器配置上 —— 那种失败没法复现，也没法信。
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

// 出厂样例必须能加载 —— 用户是直接 `cp -r .openheron-example/...` 用的。
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

// ★ 多角色样例要真的翻出**三个**规格，而不是一个带三个名字的壳。
func TestExampleConfigExposesEveryRole(t *testing.T) {
	cfg := loadExample(t)

	// ★ 规格按 agent 展开：main 的下挂是 specs[main].Sub。
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
	// 只读角色不该拿到写文件的工具 —— 分层是这套样例要演示的东西。
	if got := byID["researcher"].DisabledTools; len(got) != 3 {
		t.Fatalf("researcher must disable the writing tools, got %v", got)
	}
	if got := byID["coder"].DisabledTools; len(got) != 0 {
		t.Fatalf("coder must keep the writing tools, got %v", got)
	}
}

// 三个角色都能作为会话的 agent —— 「切换到某个专家」是多 agent 模式的
// 另一半（另一半是委派）。
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

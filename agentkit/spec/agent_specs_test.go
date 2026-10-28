package spec

import (
	"openheron/agentkit/spec/decl"
	"strings"
	"testing"
)

// 没有「主 agent」：每个 agent 都是同一格式的同一个东西，声明了
// `subagents:` 的那个能带队伍，没声明的就是独一个。
//
// ★ 会话选哪个 agent，就按哪个的声明装配 —— 所以**每个** agent 都得
//
//	有一棵自己的树（它自己 + 它的下属）。

// ① 每个 agent 一棵树：main 的树是它自己的下属，lead 的树也是。
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
	// main 的树：main + lead。
	if got := specs["main"].Sub; len(got) != 1 || got[0].ID != "lead" {
		t.Fatalf("main's tree = %+v, want [lead]", got)
	}
	// ★ lead 作为入口时带的是**它自己的**下属（architect）。
	if got := specs["lead"].Sub; len(got) != 1 || got[0].ID != "architect" {
		t.Fatalf("lead's own tree = %+v, want [architect]", got)
	}
	// 默认 agent 恒为 main。
	if cfg.DefaultAgent() != "main" {
		t.Fatalf("the default agent = %q, want main", cfg.DefaultAgent())
	}
}

// ② 每个 agent 都能当会话入口 —— 声明了就有一棵树。
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

// ③ main.md 是必需的 —— 新会话没指定 agent 时用它。
func TestDefaultAgentIsRequired(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/lead.md", "---\ndescription: L\nmodel: openai/fast\n---\nL\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("a config without main.md must be rejected")
	}
}

// ④ 没人引用的 agent **不是**错误：它照样能当会话入口。
//
// ★ 以前这里报「unreachable」—— 那条规则的前提是「只有候选根能当入口」。
//
//	现在每个声明了的 agent 都能当入口，于是「没人引用」不意味着「用不上」。
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

// ⑤ 深度按**每个 agent 的队伍**校验：某支队伍超了上限就在启动期报错。
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

// ── 「那一段配置在不在」就是开关 ───────────────────────────────────────────

// 没有 enabled 字段：tools/http.yaml 在 = 开，不在 = 关。
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
	// 写了的那一个 → 默认 allow 档。
	if got := cfg.EnabledToolNames(); len(got) != 1 || got[0] != decl.ToolNameHTTPGet {
		t.Fatalf("enabled tool names = %v, want [%s]", got, decl.ToolNameHTTPGet)
	}
}

// 文件不在 = 关。
//
// ★ 不再有「写个 null 关掉」这种形态 —— 一个工具一个文件，关它就是删文件。
//
//	这正是把 tools/ 从 server/config.yaml 里搬出来的附带好处：以前三个工具
//	挤在一份 server 配置里，关掉一个要写 null，而 null 与「整段没写」
//	在 yaml 里长得一模一样。
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

// 一个都没写 → 全部关（默认）。
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

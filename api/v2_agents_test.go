package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"openheron/agentkit/spec"
)

// 没有「主 agent」：每个 agent 都能当会话的 agent，装配按它自己的配置来。
//
// ★ 所以接口侧只有一件事要保证：**选中的 agent 必须存在**。
//   选谁都行 —— 嵌套多深都行（它自己那棵树照样展开）。

// nestedConfig 造一份带嵌套的配置：main → lead → coder。
func nestedConfig(t *testing.T) *spec.Config {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, ".openheron")
	mkdirAll(t, base)
	write := func(rel, content string) {
		p := filepath.Join(base, rel)
		mkdirAll(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("server/spec.yaml", "start_timeout: 30s\nagent:\n  max_depth: 2\n")
	write("llm/test.yaml", `type: openai
base_url: https://example.invalid
models:
  - id: small
`)
	write("agents/main.md", "---\ndescription: Root\nmodel: test/small\nsubagents: [lead]\n---\nbody\n")
	write("agents/lead.md", "---\ndescription: Lead\nmodel: test/small\nsubagents: [coder]\n---\nbody\n")
	write("agents/coder.md", "---\ndescription: Coder\nmodel: test/small\n---\nbody\n")
	write("policy/permissions.yaml", "ask:\n  - execute\ndeny: []\n")

	svc, err := spec.NewService(base, root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// ★ `/v2/agents` 列出**全部** agent —— 前端不必自己算「哪些能选」。
//
// ★ 每个 agent 报自己的下属（队伍结构），不报「归属根」：一个 agent 可以
//
//	同时是别人的下属、又是自己的入口，「它在谁的队伍里第几层」没有唯一
//	答案。
func TestV2AgentsListsEveryAgent(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return nestedConfig(t) })

	got := callJSON(t, srv, "GET", "/v2/agents", nil)
	if got.code != 200 {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view agentsView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}

	byID := map[string]agentView{}
	for _, a := range view.Agents {
		byID[a.ID] = a
	}
	for _, id := range []string{"main", "lead", "coder"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("%s is missing from /v2/agents: %s", id, got.body)
		}
	}
	// 队伍结构由「自己的下属」表达。
	if got := byID["main"].Subagents; len(got) != 1 || got[0] != "lead" {
		t.Fatalf("main's sub-agents = %v, want [lead]", got)
	}
	if got := byID["lead"].Subagents; len(got) != 1 || got[0] != "coder" {
		t.Fatalf("lead's sub-agents = %v, want [coder]", got)
	}
	if got := byID["coder"].Subagents; len(got) != 0 {
		t.Fatalf("coder's sub-agents = %v, want none", got)
	}
	// 默认 agent 是 main —— 新会话没指定时用它。
	if view.DefaultAgent != "main" {
		t.Fatalf("defaultAgent = %q, want main", view.DefaultAgent)
	}
}

// ★ `/v2/agents` 带**配置指纹** —— 前端唯一的「配置变了」信号。
//
// 用户可以在**编辑器里**改 `agents/*.md`（新增一个文件也算）：服务端会热更新，
// 但浏览器此前拿不到任何通知，而这份列表在前端是**模块级缓存** —— 于是
// 选择器一直显示过时的 agent 名单。
//
// 指纹取自 spec.Version（按「文件名 + 大小 + mtime」算），所以**新增一个
// agent 文件必然改指纹**。
func TestV2AgentsCarriesConfigFingerprint(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".openheron")
	mkdirAll(t, base)
	write := func(rel, content string) {
		p := filepath.Join(base, rel)
		mkdirAll(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("server/spec.yaml", "start_timeout: 30s\n")
	write("llm/test.yaml", `type: openai
base_url: https://example.invalid
models:
  - id: small
`)
	write("agents/main.md", "---\ndescription: Root\nmodel: test/small\n---\nbody\n")
	write("policy/permissions.yaml", "ask:\n  - execute\ndeny: []\n")

	svc, err := spec.NewService(base, root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	load := func() *spec.Config {
		t.Helper()
		cfg, err := svc.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return cfg
	}

	current := load()
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return current })

	fingerprint := func() string {
		t.Helper()
		got := callJSON(t, srv, "GET", "/v2/agents", nil)
		if got.code != 200 {
			t.Fatalf("= %d (%s)", got.code, got.body)
		}
		var view agentsView
		if err := json.Unmarshal([]byte(got.body), &view); err != nil {
			t.Fatalf("decode: %v (%s)", err, got.body)
		}
		return view.Version
	}

	before := fingerprint()
	if before == "" {
		t.Fatal("version must not be empty —— 它是前端发现热更新的唯一信号")
	}

	// ★ 用户在编辑器里新建了一个 agent 文件 —— 服务端重新加载了配置。
	write("agents/latecomer.md", "---\ndescription: Late\nmodel: test/small\n---\nbody\n")
	current = load()

	if after := fingerprint(); after == before {
		t.Fatalf("version = %q, want it to change after a config change", after)
	}
}

// ★ 切换到**任何**声明了的 agent 都行 —— 嵌套的也算。
func TestSwitchSessionToAnyDeclaredAgent(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return nestedConfig(t) })

	created := callJSON(t, srv, "POST", "/v2/sessions", "{}")
	if created.code != 200 && created.code != 201 {
		t.Fatalf("create session: = %d (%s)", created.code, created.body)
	}
	var meta struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal([]byte(created.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, created.body)
	}

	// 嵌套的 lead 与更深的 coder 都能选。
	for _, want := range []string{"lead", "coder"} {
		got := callJSON(t, srv, "PATCH", "/v2/sessions/"+meta.ThreadID+"/agent",
			map[string]any{"agentId": want})
		if got.code != 200 {
			t.Fatalf("switch to %q: = %d (%s)", want, got.code, got.body)
		}
		var after struct {
			AgentID string `json:"agentId"`
		}
		if err := json.Unmarshal([]byte(got.body), &after); err != nil {
			t.Fatalf("decode: %v (%s)", err, got.body)
		}
		if after.AgentID != want {
			t.Fatalf("agentId = %q, want %q", after.AgentID, want)
		}
	}

	// ★ 没声明的仍然拒绝 —— 这是唯一一种拒绝。
	bad := callJSON(t, srv, "PATCH", "/v2/sessions/"+meta.ThreadID+"/agent",
		map[string]any{"agentId": "ghost"})
	if bad.code == 200 {
		t.Fatalf("an undeclared agent must be rejected: %s", bad.body)
	}
}

// 切换之后，**下一轮**以它为根运行。
//
// ★ 切换只影响之后的轮次：历史不改写，正在跑的 turn 不换归属。
func TestSessionRunsAsTheSwitchedAgent(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return nestedConfig(t) })

	created := callJSON(t, srv, "POST", "/v2/sessions", "{}")
	if created.code != 200 && created.code != 201 {
		t.Fatalf("create session: = %d (%s)", created.code, created.body)
	}
	var meta struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal([]byte(created.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, created.body)
	}

	if got := callJSON(t, srv, "PATCH", "/v2/sessions/"+meta.ThreadID+"/agent",
		map[string]any{"agentId": "lead"}); got.code != 200 {
		t.Fatalf("switch: = %d (%s)", got.code, got.body)
	}

	// 会话详情里的 agentId 就是切换后的那个。
	got := callJSON(t, srv, "GET", "/v2/sessions/"+meta.ThreadID, nil)
	if got.code != 200 {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var detail struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal([]byte(got.body), &detail); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if detail.AgentID != "lead" {
		t.Fatalf("agentId = %q, want lead", detail.AgentID)
	}
}

package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

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
	if got := byID["main"].Subagents; len(got) != 1 || got[0] != "lead" {
		t.Fatalf("main's sub-agents = %v, want [lead]", got)
	}
	if got := byID["lead"].Subagents; len(got) != 1 || got[0] != "coder" {
		t.Fatalf("lead's sub-agents = %v, want [coder]", got)
	}
	if got := byID["coder"].Subagents; len(got) != 0 {
		t.Fatalf("coder's sub-agents = %v, want none", got)
	}
	if view.DefaultAgent != "main" {
		t.Fatalf("defaultAgent = %q, want main", view.DefaultAgent)
	}
}

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
		t.Fatal("version must not be empty - it is the only signal the frontend has for hot reload")
	}

	write("agents/latecomer.md", "---\ndescription: Late\nmodel: test/small\n---\nbody\n")
	current = load()

	if after := fingerprint(); after == before {
		t.Fatalf("version = %q, want it to change after a config change", after)
	}
}

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

	bad := callJSON(t, srv, "PATCH", "/v2/sessions/"+meta.ThreadID+"/agent",
		map[string]any{"agentId": "ghost"})
	if bad.code == 200 {
		t.Fatalf("an undeclared agent must be rejected: %s", bad.body)
	}
}

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

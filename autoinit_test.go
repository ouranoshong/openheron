package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/templates"
)

// ★ The first start writes the configuration, so a fresh clone starts without
//
//	a command in front of it. The tree that comes out is the one
//	`openheron init` writes: the same templates, the same rules.
func TestTheFirstStartWritesTheConfiguration(t *testing.T) {
	project := t.TempDir()
	t.Setenv(spec.ConfigDirEnv, "")
	svc, err := spec.NewService("", project)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := obs.New(&buf, new(slog.LevelVar))

	if err := initConfigIfEmpty(svc, log); err != nil {
		t.Fatalf("initConfigIfEmpty: %v", err)
	}

	agent := filepath.Join(svc.Root(), spec.DirAgent, spec.DefaultAgentID+".md")
	body, err := os.ReadFile(agent)
	if err != nil {
		t.Fatalf("the agent file was not written: %v", err)
	}
	// ★ The model is the provider template's own default and the key is read
	//   from the environment: the same as `openheron init` without flags.
	if !strings.Contains(string(body), "model: deepseek/deepseek-flash") {
		t.Fatalf("the agent must carry the default model:\n%s", body)
	}
	provider, err := os.ReadFile(filepath.Join(svc.Root(), spec.DirLLM, "deepseek.yaml"))
	if err != nil {
		t.Fatalf("the provider file was not written: %v", err)
	}
	// ★ The api key is **not** in the provider file: the provider's name
	//   says where it comes from — LLM_DEEPSEEK_API_KEY, or the keyring
	//   entry llm:deepseek.
	if strings.Contains(string(provider), "api_key:") {
		t.Fatalf("the api key must not be in the configuration:\n%s", provider)
	}
	// ★ The tree has to load: a configuration that does not load is not a
	//   configuration.
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("the written configuration must load: %v", err)
	}
	if cfg.Agents[spec.DefaultAgentID] == nil {
		t.Fatalf("the default agent is missing: %+v", cfg.Agents)
	}
	if !strings.Contains(buf.String(), "configuration initialized") {
		t.Fatalf("the initialization was not reported:\n%s", buf.String())
	}
}

// ★ A named root is the user's: an empty one is their decision, not a gap to
//
//	fill. ★ The test instance depends on this — it names its own root, and a
//	configuration written under it would be a configuration nobody asked for.
func TestANamedRootIsNotWrittenInto(t *testing.T) {
	root := filepath.Join(t.TempDir(), "named")
	svc, err := spec.NewServiceAt(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := obs.New(&buf, new(slog.LevelVar))

	if err := initConfigIfEmpty(svc, log); err != nil {
		t.Fatalf("initConfigIfEmpty: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, spec.DirAgent, spec.DefaultAgentID+".md")); !os.IsNotExist(err) {
		t.Fatalf("a named root must not be filled: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("nothing happened, so nothing was reported:\n%s", buf.String())
	}
}

// ★ "Empty" means **no agent file**. A tree that has one is a configuration,
//
//	whether or not it loads, and the initialization must not touch it.
func TestAnExistingConfigurationIsLeftAlone(t *testing.T) {
	project := t.TempDir()
	t.Setenv(spec.ConfigDirEnv, "")
	svc, err := spec.NewService("", project)
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(svc.Root(), spec.DirAgent, spec.DefaultAgentID+".md")
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "---\nmodel: deepseek/deepseek-flash\n---\n\nmy own agent\n"
	if err := os.WriteFile(agent, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := obs.New(&buf, new(slog.LevelVar))

	if err := initConfigIfEmpty(svc, log); err != nil {
		t.Fatalf("initConfigIfEmpty: %v", err)
	}

	got, err := os.ReadFile(agent)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Fatalf("the existing agent file was rewritten:\n%s", got)
	}
}

// ★ A half-written tree is topped up, not replaced: the files that are there
//
//	are kept, the ones that are missing are written.
func TestAHalfWrittenTreeIsToppedUp(t *testing.T) {
	project := t.TempDir()
	t.Setenv(spec.ConfigDirEnv, "")
	svc, err := spec.NewService("", project)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := templates.File("tools/http.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(svc.Root(), spec.DirTools, "http.yaml")
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := obs.New(&buf, new(slog.LevelVar))

	if err := initConfigIfEmpty(svc, log); err != nil {
		t.Fatalf("initConfigIfEmpty: %v", err)
	}

	got, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("the existing tool file was rewritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(svc.Root(), spec.DirAgent, spec.DefaultAgentID+".md")); err != nil {
		t.Fatalf("the missing files were not written: %v", err)
	}
}

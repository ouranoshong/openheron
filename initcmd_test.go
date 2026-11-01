package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

func initEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv(spec.ConfigDirEnv, "")
	return filepath.Join(root, "project")
}

func TestInitJSONOutputIsValidJSON(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "--json"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	var out struct {
		Dir     string   `json:"dir"`
		Model   string   `json:"model"`
		Created []string `json:"created"`
		Notes   []string `json:"notes"`
		Env     string   `json:"env"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("the output must be valid JSON: %v\n%s", err, buf.String())
	}
	if out.Dir == "" || out.Model == "" {
		t.Fatalf("dir and model must be reported: %+v", out)
	}
	if len(out.Created) != 6 {
		t.Fatalf("created = %v, want six files", out.Created)
	}
	for _, want := range []string{
		".openheron/agents/main.md",
		".openheron/agents/task.md",
		".openheron/llm/deepseek.yaml",
		".openheron/policy/permissions.yaml",
		".openheron/policy/limits.yaml",
		".openheron/server/config.yaml",
	} {
		found := false
		for _, c := range out.Created {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("created = %v, want it to contain %s", out.Created, want)
		}
	}
	if out.Env != "DEEPSEEK_API_KEY" {
		t.Fatalf("env = %q, want DEEPSEEK_API_KEY", out.Env)
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "--dry-run"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	if !strings.Contains(buf.String(), "dry run") {
		t.Fatalf("the output must say it was a dry run:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".openheron")); err == nil {
		t.Fatal("a dry run must not create anything")
	}
}

func TestInitRejectsAnUnknownFlag(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--dir", dir, "--nope"}, &buf)
	if err == nil {
		t.Fatal("an unknown flag must be an error")
	}
}

func TestInitRejectsAMalformedModel(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--dir", dir, "--model", "gpt-4o"}, &buf)
	if err == nil {
		t.Fatal("a model reference without a provider must be an error")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("the error must say what is wrong: %v", err)
	}
}

func TestInitRejectsStrayArguments(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "extra"}, &buf); err == nil {
		t.Fatal("a stray argument must be an error")
	}
}

// ── --help ──────────────────────────────────────────────────────────────────

func TestInitHelpPrintsTheUsage(t *testing.T) {
	var buf bytes.Buffer
	if err := initCmd([]string{"--help"}, &buf); err != nil {
		t.Fatalf("--help must not be an error: %v", err)
	}
	if !strings.Contains(buf.String(), "openheron init") {
		t.Fatalf("the usage must name the command:\n%s", buf.String())
	}
}

func TestInitWritesIntoTheGivenDirectory(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	for _, rel := range []string{
		".openheron/agents/main.md",
		".openheron/agents/task.md",
		".openheron/llm/deepseek.yaml",
		".openheron/policy/permissions.yaml",
		".openheron/policy/limits.yaml",
		".openheron/server/config.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
	}
	// ★ The file's presence is the switch that turns a tool on, so init does
	//   not open an outbound channel on its own.
	if _, err := os.Stat(filepath.Join(dir, ".openheron", "tools")); err == nil {
		t.Fatal("tools/ must not be written unless --tools is given")
	}
	svc, err := spec.NewServiceAt(filepath.Join(dir, ".openheron"), dir)
	if err != nil {
		t.Fatalf("NewServiceAt: %v", err)
	}
	if _, err := svc.Load(); err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
}

func TestInitToolsWritesTheToolConfigurations(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "--tools"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	for _, rel := range []string{
		".openheron/tools/http.yaml",
		".openheron/tools/web_search.yaml",
		".openheron/tools/wikipedia.yaml",
		".openheron/tools/kb.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
	}
	svc, err := spec.NewServiceAt(filepath.Join(dir, ".openheron"), dir)
	if err != nil {
		t.Fatalf("NewServiceAt: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
	if cfg.Tools.HTTP == nil || cfg.Tools.WebSearch == nil ||
		cfg.Tools.Wikipedia == nil {
		t.Fatalf("the written tool files must enable the tools: %+v", cfg.Tools)
	}
}

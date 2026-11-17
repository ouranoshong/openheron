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

// initEnv prepares a hermetic environment and returns the config root the
// tests write into.
//
// ★ The openheron root is named, so the command has one answer for where the
//
//	base directory is — and the answer is a temporary directory, not the
//	developer's home.
func initEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv(spec.RootEnv, filepath.Join(root, "base"))
	t.Setenv(spec.ConfigRootEnv, "")
	t.Setenv(spec.ConfigDirEnv, "")
	// ★ The project root names are cleared too: the machine the tests run on
	//   may have one of them set, and the command would then work somewhere
	//   else than the temporary directory.
	t.Setenv(spec.DefaultProjectRootEnv, "")
	t.Setenv(spec.ProjectRootEnv, "")
	return filepath.Join(root, "configs")
}

func TestInitJSONOutputIsValidJSON(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--config-root", dir, "--json"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	var out struct {
		Dir     string   `json:"dir"`
		Model   string   `json:"model"`
		Created []string `json:"created"`
		Notes   []string `json:"notes"`
		Env     string   `json:"env"`
		Secret  string   `json:"secret"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("the output must be valid JSON: %v\n%s", err, buf.String())
	}
	if out.Dir == "" || out.Model == "" {
		t.Fatalf("dir and model must be reported: %+v", out)
	}
	// ★ The tree is written whole: four agents, five skills, three MCP
	//   servers, six tool configurations, the provider and the server
	//   section — 20 files.
	if len(out.Created) != 20 {
		t.Fatalf("created = %v, want 20 files", out.Created)
	}
	for _, want := range []string{
		"agents/main.md",
		"agents/task.md",
		"agents/agent-expert.md",
		"agents/skill-expert.md",
		"skills/openheron-config-map/SKILL.md",
		"skills/openheron-config-edit/SKILL.md",
		"skills/openheron-config-templates/SKILL.md",
		"skills/openheron-config-validate/SKILL.md",
		"skills/openheron-agent-authoring/SKILL.md",
		"llm/deepseek.yaml",
		"tools/http.yaml",
		"tools/web_search.yaml",
		"tools/wikipedia.yaml",
		"tools/kb.yaml",
		"tools/filesystem.yaml",
		"tools/config.yaml",
		"mcps/fetch.yaml",
		"mcps/time.yaml",
		"mcps/deepwiki.yaml",
		"server/config.yaml",
	} {
		found := false
		for _, c := range out.Created {
			// ★ The payload joins with the platform separator; the expectation
			//   is written with slashes so it reads the same everywhere.
			if filepath.ToSlash(c) == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("created = %v, want it to contain %s", out.Created, want)
		}
	}
	if out.Env != "LLM_DEEPSEEK_API_KEY" {
		t.Fatalf("env = %q, want LLM_DEEPSEEK_API_KEY", out.Env)
	}
	// ★ The keyring entry is named too: the key has two places, and the
	//   user has to know both before they wonder which one is in force.
	if out.Secret != "llm:deepseek" {
		t.Fatalf("secret = %q, want llm:deepseek", out.Secret)
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--config-root", dir, "--dry-run"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	if !strings.Contains(buf.String(), "dry run") {
		t.Fatalf("the output must say it was a dry run:\n%s", buf.String())
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("a dry run must not create anything")
	}
	// ★ The default project root is a directory, and a dry run writes
	//   nothing — a directory included.
	if _, err := os.Stat(defaultProjectRootOf(t)); err == nil {
		t.Fatal("a dry run must not create the default project root")
	}
}

// defaultProjectRootOf returns the default project root the command would
// create: the openheron root the test named, plus the two path segments.
func defaultProjectRootOf(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv(spec.RootEnv), spec.DefaultProjectsDirName, "default")
}

// ★ The default project root is part of the layout the command lays out: it
//
//	is where a session that belongs to no project does its work, and a
//	command that cannot chdir into it never starts.
func TestInitCreatesTheDefaultProjectRoot(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--config-root", dir}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	root := defaultProjectRootOf(t)
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the default project root must exist: %v", err)
	}
	if !strings.Contains(buf.String(), root) {
		t.Fatalf("the output must name the directory it created:\n%s", buf.String())
	}
}

// ★ A **named** project root is the user's directory: making it for them
//
//	would hide a typo, so the command leaves it alone.
func TestInitDoesNotCreateANamedProjectRoot(t *testing.T) {
	dir := initEnv(t)
	named := filepath.Join(t.TempDir(), "named")
	t.Setenv(spec.DefaultProjectRootEnv, named)

	var buf bytes.Buffer
	if err := initCmd([]string{"--config-root", dir}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	if _, err := os.Stat(named); err == nil {
		t.Fatal("a named project root must not be created")
	}
}

func TestInitRejectsAnUnknownFlag(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--config-root", dir, "--nope"}, &buf)
	if err == nil {
		t.Fatal("an unknown flag must be an error")
	}
}

// ★ The old flag named a **project** directory and the new one names the
//
//	**config root** — two different directories. Accepting the old name for
//	the new one would write the configuration somewhere nobody asked for.
func TestInitRejectsTheRetiredDirFlag(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--dir", dir}, &buf)
	if err == nil {
		t.Fatal("--dir must be an error")
	}
	if !strings.Contains(err.Error(), "--config-root") {
		t.Fatalf("the error must name the new flag: %v", err)
	}
}

func TestInitRejectsAMalformedModel(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--config-root", dir, "--model", "gpt-4o"}, &buf)
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
	if err := initCmd([]string{"--config-root", dir, "extra"}, &buf); err == nil {
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
	if err := initCmd([]string{"--config-root", dir}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	for _, rel := range []string{
		"agents/main.md",
		"agents/task.md",
		"agents/agent-expert.md",
		"agents/skill-expert.md",
		"skills/openheron-config-map/SKILL.md",
		"llm/deepseek.yaml",
		"mcps/fetch.yaml",
		"tools/http.yaml",
		"server/config.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
	}
	svc, err := spec.NewServiceAt(dir, dir)
	if err != nil {
		t.Fatalf("NewServiceAt: %v", err)
	}
	if _, err := svc.Load(); err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
}

// ★ The tool configurations are part of the tree: the agents name
//
//	`request_get` / `duckduckgo_search` / `wikipedia_search`, and a name that
//	no provider offers is reported as a typo.
func TestInitToolsWritesTheToolConfigurations(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--config-root", dir}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	for _, rel := range []string{
		"tools/http.yaml",
		"tools/web_search.yaml",
		"tools/wikipedia.yaml",
		"tools/kb.yaml",
		"tools/filesystem.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
	}
	svc, err := spec.NewServiceAt(dir, dir)
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

// ★ The switch is negative on purpose: the full tree is the default, and
//
//	`--no-tools` is the way to leave the tool configurations out.
func TestInitNoToolsLeavesTheToolConfigurationsOut(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--config-root", dir, "--no-tools"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tools")); err == nil {
		t.Fatal("tools/ must not be written when --no-tools is given")
	}
	// ★ The rest of the tree is written all the same.
	if _, err := os.Stat(filepath.Join(dir, "agents", "main.md")); err != nil {
		t.Fatalf("the agents must still be written: %v", err)
	}
	if !strings.Contains(buf.String(), "tools/ was skipped") {
		t.Fatalf("the output must say what was skipped:\n%s", buf.String())
	}
}

// ★ The default target is the config root the server reads — the one inside
//
//	the openheron root. It is not <project>/.openheron any more.
func TestInitDefaultsToTheOpenheronRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv(spec.RootEnv, filepath.Join(root, "base"))
	t.Setenv(spec.ConfigRootEnv, "")
	t.Setenv(spec.ConfigDirEnv, "")

	var buf bytes.Buffer
	if err := initCmd(nil, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	want := filepath.Join(root, "base", spec.DefaultConfigDirName)
	if _, err := os.Stat(filepath.Join(want, "agents", "main.md")); err != nil {
		t.Fatalf("the factory tree must land in %q: %v", want, err)
	}
}

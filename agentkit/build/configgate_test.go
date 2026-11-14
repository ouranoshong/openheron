package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"

	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/spec"
)

// gateEnv writes a small but real configuration root and returns the project
// root, the config root, the service, and a backend wired with the gate.
func gateEnv(t *testing.T) (projectRoot, cfgRoot string, svc *spec.Service, b *fsx.Local) {
	t.Helper()

	projectRoot = t.TempDir()
	cfgRoot = filepath.Join(projectRoot, spec.DefaultBaseDirName)
	for _, sub := range []string{spec.DirLLM, spec.DirAgent, spec.DirTools, spec.DirServer} {
		if err := os.MkdirAll(filepath.Join(cfgRoot, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LLM_OPENAI_API_KEY", "sk-test")

	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfgRoot, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("llm/openai.yaml", "type: openai\nbase_url: https://a.example\n"+
		"models:\n  - id: fast\n    reasoning:\n      off: { reasoning_effort: none }\n")
	write("agents/main.md", "---\nmodel: openai/fast\ntools: [ls]\n---\nbody\n")
	write("server/config.yaml", "listen_addr: \":18099\"\n")

	var err error
	svc, err = spec.NewService(cfgRoot, projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	b, err = fsx.New(fsx.Config{Root: projectRoot, ConfigGate: newConfigGate(svc)})
	if err != nil {
		t.Fatal(err)
	}
	return projectRoot, cfgRoot, svc, b
}

// ★ The point of the gate: a file tool that edits a configuration file goes
// through the service, so a change that breaks the tree never lands.
func TestAnEditInsideTheConfigRootGoesThroughTheService(t *testing.T) {
	_, cfgRoot, _, b := gateEnv(t)
	path := filepath.Join(cfgRoot, "agents", "main.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The model points the agent at a model that does not exist.
	err = b.Edit(context.Background(), &filesystem.EditRequest{
		FilePath:  path,
		OldString: "model: openai/fast",
		NewString: "model: openai/nope",
	})
	if err == nil {
		t.Fatal("a dangling model reference must be refused")
	}
	if !strings.Contains(err.Error(), "openai/nope") {
		t.Fatalf("the refusal must name the model: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the file changed despite the refusal: %q", after)
	}
}

// ★ An edit that keeps the tree working lands — and lands through the
// service, which is what makes the write atomic.
func TestAnEditInsideTheConfigRootLandsWhenTheTreeStillHolds(t *testing.T) {
	_, cfgRoot, _, b := gateEnv(t)
	path := filepath.Join(cfgRoot, "agents", "main.md")

	if err := b.Edit(context.Background(), &filesystem.EditRequest{
		FilePath:  path,
		OldString: "body",
		NewString: "body two",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "body two") {
		t.Fatalf("the edit did not land: %q", got)
	}
}

// ★ `server/` is not hot-reloaded, so it is not editable: the gate is where
// that rule finally reaches the file tools.
func TestAWriteCannotTouchANonEditablePartition(t *testing.T) {
	_, cfgRoot, _, b := gateEnv(t)
	path := filepath.Join(cfgRoot, "server", "config.yaml")

	err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "listen_addr: \":1\"\n",
	})
	if err == nil {
		t.Fatal("server/ must not be writable")
	}
	if !strings.Contains(err.Error(), "not editable") {
		t.Fatalf("the refusal must say why: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), ":18099") {
		t.Fatalf("the file changed despite the refusal: %q", got)
	}
}

// ★ The configuration root holds the session store next to the configuration.
// A write that lands there would corrupt it, so it is refused like any other
// file that is not a configuration file.
func TestTheConfigurationRootRefusesAFileThatIsNotConfiguration(t *testing.T) {
	_, cfgRoot, _, b := gateEnv(t)
	path := filepath.Join(cfgRoot, "state", "sessions", "x.json")

	err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "{}",
	})
	if err == nil {
		t.Fatal("a file that is not configuration must be refused")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("the file was written despite the refusal")
	}
}

// ★ Everything outside the configuration root keeps the behaviour it had.
func TestAWriteOutsideTheConfigRootIsUntouched(t *testing.T) {
	projectRoot, _, _, b := gateEnv(t)
	path := filepath.Join(projectRoot, "notes.md")

	if err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "plain",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "plain" {
		t.Fatalf("the file was not written: %q", got)
	}
}

// ★ An old string that is not there is an error, not an empty patch.
func TestAnEditInsideTheConfigRootWithTheWrongOldStringIsRefused(t *testing.T) {
	_, cfgRoot, _, b := gateEnv(t)
	path := filepath.Join(cfgRoot, "agents", "main.md")

	err := b.Edit(context.Background(), &filesystem.EditRequest{
		FilePath: path, OldString: "nowhere", NewString: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want a not-found error, got %v", err)
	}
}

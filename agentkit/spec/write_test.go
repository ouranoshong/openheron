package spec

import (
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *fixture) service(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (f *fixture) exists(t *testing.T, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(f.root, rel))
	return err == nil
}

func TestLoadWithNilEqualsLoad(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")
	f.write(t, "policy/permissions.yaml", "ask: [read_file, write_file]\n")

	s := f.service(t)

	plain, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	withNil, err := s.loadWith(nil)
	if err != nil {
		t.Fatal(err)
	}

	if withNil.Version != plain.Version {
		t.Fatalf("version = %q, want %q", withNil.Version, plain.Version)
	}
	if withNil.Server.ListenAddr != plain.Server.ListenAddr {
		t.Fatalf("listen_addr = %q, want %q", withNil.Server.ListenAddr, plain.Server.ListenAddr)
	}
	if strings.Join(withNil.Policy.Ask, ",") != strings.Join(plain.Policy.Ask, ",") {
		t.Fatalf("ask = %v, want %v", withNil.Policy.Ask, plain.Policy.Ask)
	}
	if len(withNil.Agents) != len(plain.Agents) {
		t.Fatalf("agents = %d, want %d", len(withNil.Agents), len(plain.Agents))
	}
}

func TestLoadWithReplacesExistingContent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "ask: [read_file]\n")

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("deny: [execute]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policy.Ask) != 0 {
		t.Fatalf("ask = %v, want empty (wholesale replace)", cfg.Policy.Ask)
	}
	if got := cfg.Policy.Deny; len(got) != 1 || got[0] != "execute" {
		t.Fatalf("deny = %v, want [execute]", got)
	}
	if got := f.read(t, "policy/permissions.yaml"); !strings.Contains(got, "read_file") {
		t.Fatalf("disk was modified: %q", got)
	}
}

func TestLoadWithAddsANewFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "llm/second.yaml",
		Content: []byte("type: openai\nbase_url: https://b.example.com\nmodels:\n  - id: x\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Models["second/x"]; !ok {
		t.Fatalf("second/x not declared; models = %v", keys(cfg.Models))
	}
	if f.exists(t, "llm/second.yaml") {
		t.Fatal("trial merge created a file on disk")
	}
}

func TestLoadWithDeleteCatchesDanglingModelRef(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	_, err := s.loadWith(&mutation{Rel: "llm/openai.yaml", Delete: true})

	if err == nil {
		t.Fatal("deleting the only provider that main references must fail the trial merge")
	}
	if !strings.Contains(err.Error(), "openai/fast") {
		t.Fatalf("the error must name the dangling reference, got %q", err)
	}
	if f.exists(t, "llm/openai.yaml") != true {
		t.Fatal("a failed trial merge must not delete anything on disk")
	}
}

func TestLoadWithDeleteSucceedsWhenNothingDangles(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/unused.yaml", minimalLLM)

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{Rel: "llm/unused.yaml", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["unused"]; ok {
		t.Fatal("unused should be gone after the delete mutation")
	}
	if f.exists(t, "llm/unused.yaml") != true {
		t.Fatal("trial merge must not delete the file")
	}
}

func TestLoadWithRejectsUnresolvableModelRef(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	_, err := s.loadWith(&mutation{
		Rel:     "agents/main.md",
		Content: []byte("---\nmodel: openai/nope\n---\nbody\n"),
	})
	if err == nil {
		t.Fatal("an unresolvable model ref must fail the trial merge")
	}
	if !strings.Contains(err.Error(), "agents/main.md") {
		t.Fatalf("the error must name the file to edit, got %q", err)
	}
	if !strings.Contains(err.Error(), "openai/nope") {
		t.Fatalf("the error must name the ref, got %q", err)
	}
}

func TestLoadWithRejectsUnknownYAMLFieldWithLineNumber(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	_, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("allow: [read_file]\nmax_retry: 3\n"),
	})
	if err == nil {
		t.Fatal("an unknown field must be rejected")
	}
	if !strings.Contains(err.Error(), "max_retry") {
		t.Fatalf("the error must name the field, got %q", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("the error must carry a line number, got %q", err)
	}
	if !strings.Contains(err.Error(), "policy/permissions.yaml") {
		t.Fatalf("the error must name the file by its relative path, got %q", err)
	}
	if strings.Contains(err.Error(), f.root) || strings.Contains(err.Error(), f.root) {
		t.Fatalf("errors must not leak absolute paths: %q", err)
	}
}

func TestTheCandidateOnlyAffectsItsOwnFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "ask: [on_disk]\n")
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("ask: [candidate]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policy.Ask) != 1 || cfg.Policy.Ask[0] != "candidate" {
		t.Fatalf("ask = %v, want [candidate]", cfg.Policy.Ask)
	}
	if cfg.Server.ListenAddr != ":9999" {
		t.Fatalf("an unrelated file must not be affected: listen_addr = %q", cfg.Server.ListenAddr)
	}
}

func TestMutationOnServerCategoryIsSeenByTheMerge(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "server/config.yaml",
		Content: []byte("agent:\n  max_depth: 2\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxDepth() != 2 {
		t.Fatalf("max_depth = %d, want 2", cfg.MaxDepth())
	}
}

func TestLoadWithCreatesIntoAMissingDirectory(t *testing.T) {
	root := t.TempDir()
	cfgRoot := filepath.Join(root, ".openheron")
	if err := os.MkdirAll(filepath.Join(cfgRoot, DirLLM), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfgRoot, DirAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: cfgRoot}
	f.seedMinimal(t)
	t.Setenv("TEST_LLM_KEY", "sk-test")

	if _, err := os.Stat(filepath.Join(cfgRoot, DirPolicy)); err == nil {
		t.Fatal("precondition: policy/ must not exist yet")
	}

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("ask: [execute]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policy.Ask) != 1 || cfg.Policy.Ask[0] != "execute" {
		t.Fatalf("ask = %v, want [execute]", cfg.Policy.Ask)
	}
	if _, err := os.Stat(filepath.Join(cfgRoot, DirPolicy)); err == nil {
		t.Fatal("trial merge created a directory on disk")
	}
}

func TestCategoriesCoverExactlyWhatTheLoaderReads(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Categories {
		if seen[c.Dir] {
			t.Fatalf("duplicate category %q", c.Dir)
		}
		seen[c.Dir] = true

		cfg := &Config{
			Providers: map[string]*decl.Provider{},
			Models:    map[string]*decl.Model{},
			Agents:    map[string]*decl.Agent{},
			Server:    defaultServer(),
			Policy:    defaultPolicy(),
		}
		if cfg.loaderFor(c) == nil {
			t.Fatalf("category %q has no loader", c.Dir)
		}
	}
	if seen[DirSkills] {
		t.Fatal("skills must stay out of Categories: it is a directory tree, not flat entity files")
	}
}

func TestCategoryHotIsDerivedFromHotDirs(t *testing.T) {
	for _, c := range Categories {
		inHotDirs := false
		for _, d := range HotDirs {
			if d == c.Dir {
				inHotDirs = true
			}
		}
		if c.Hot() != inHotDirs {
			t.Fatalf("category %q: Hot() = %v, HotDirs says %v", c.Dir, c.Hot(), inHotDirs)
		}
	}
	serverCat, _ := CategoryByDir(DirServer)
	if serverCat.Hot() {
		t.Fatal("server must not be hot: changing it requires a restart")
	}
}

func TestRootIsTheOnlyConfigDirectory(t *testing.T) {
	f := newFixture(t)
	s := f.service(t)

	if got := s.Root(); got != f.root {
		t.Fatalf("root = %q, want %q", got, f.root)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

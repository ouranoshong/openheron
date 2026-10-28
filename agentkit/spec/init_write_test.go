package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── NewServiceAt ───────────────────────────────────────────────────────────

func TestNewServiceDoesNotProbeUpwards(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor", ".openheron")
	if err := os.MkdirAll(ancestor, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "ancestor", "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(ConfigDirEnv, "")
	derived, err := NewService("", child)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Root() == ancestor {
		t.Fatalf("the ancestor's config root must not be picked up, got %q", derived.Root())
	}
	if want := filepath.Join(child, ".openheron"); derived.Root() != want {
		t.Fatalf("the config root must be the project root's own directory, got %q (want %q)", derived.Root(), want)
	}

	fixed, err := NewServiceAt(filepath.Join(child, ".openheron"), child)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Root() != filepath.Join(child, ".openheron") {
		t.Fatalf("root = %q, want the given one", fixed.Root())
	}
}

func TestNewServiceAtTakesTheConfigRootAsIs(t *testing.T) {
	base := t.TempDir()
	s, err := NewServiceAt(base, base)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != base {
		t.Fatalf("root = %q, want the given one", s.Root())
	}
}

func TestAnAbsentConfigRootLoadsNothing(t *testing.T) {
	s, err := NewServiceAt("", filepath.Join(t.TempDir(), ".openheron"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.LoadUnchecked()
	if err != nil {
		t.Fatalf("LoadUnchecked: %v", err)
	}
	if len(cfg.Providers) != 0 || len(cfg.Agents) != 0 {
		t.Fatalf("nothing must be loaded from an absent config root: %+v", cfg)
	}
}

// ── WriteMany ──────────────────────────────────────────────────────────────

const batchProvider = `type: openai_compat
base_url: https://api.openai.com/v1
api_key: ${TEST_LLM_KEY}
models:
  - id: gpt-4o
`

const batchAgent = `---
description: 批量写的 agent
model: openai/gpt-4o
---

正文。
`

func TestWriteManyWritesMutuallyDependentFilesAtOnce(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	rev, _, err := s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(batchProvider)},
		{Rel: "agents/main.md", Content: []byte(batchAgent)},
	}, "")
	if err != nil {
		t.Fatalf("WriteMany: %v", err)
	}
	if rev == "" {
		t.Fatal("the revision must be reported")
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("the written config must load: %v", err)
	}
	if _, ok := cfg.Agents["main"]; !ok {
		t.Fatal("the agent must be there")
	}
	if _, ok := cfg.Models["openai/gpt-4o"]; !ok {
		t.Fatal("the model must be there")
	}
}

func TestWriteManyLeavesTheDiskAloneWhenTheMergeIsRejected(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.root)

	_, _, err = s.WriteMany([]File{
		{Rel: "agents/main.md", Content: []byte("---\nmodel: openai/nope\n---\n\n坏的。\n")},
		{Rel: "llm/openai.yaml", Content: []byte(batchProvider)},
	}, "")
	if err == nil {
		t.Fatal("the batch must be rejected")
	}
	if got := snapshot(t, f.root); got != before {
		t.Fatalf("the disk must be untouched:\nbefore %s\nafter  %s", before, got)
	}
}

func TestWriteManyRejectsALiteralSecret(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(
			"type: openai_compat\nbase_url: https://x\napi_key: sk-literal\nmodels:\n  - id: gpt-4o\n")},
	}, "")
	if err == nil {
		t.Fatal("a literal api_key must be rejected")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("the error must name the key: %v", err)
	}
}

func TestWriteManyIsAllOrNothing(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.root)

	_, _, err = s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(batchProvider)},
		{Rel: "agents/main.md", Content: []byte("---\nmodel: openai/missing\n---\n\n坏的。\n")},
	}, "")
	if err == nil {
		t.Fatal("the batch must be rejected")
	}
	if got := snapshot(t, f.root); got != before {
		t.Fatalf("the disk must be untouched:\nbefore %s\nafter  %s", before, got)
	}
}

func TestWriteManyRejectsAnEmptyBatch(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WriteMany(nil, ""); err == nil {
		t.Fatal("an empty batch must be an error")
	}
}

func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b.WriteString(rel + "\n" + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return b.String()
}

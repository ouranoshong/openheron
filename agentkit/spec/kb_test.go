package spec

import (
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"path/filepath"
	"strings"
	"testing"
)

const embeddingLLM = `type: openai_compat
base_url: http://127.0.0.1:8089/v1
models:
  - id: Qwen3-Embedding-0.6B-GGUF
    kind: embedding
    dimensions: 1024
`

func kbFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/local.yaml", embeddingLLM)
	return f
}

func load(t *testing.T, f *fixture) (*Config, error) {
	t.Helper()
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s.Load()
}

func loadErr(t *testing.T, f *fixture) error {
	t.Helper()
	_, err := load(t, f)
	return err
}

func TestKBAbsentIsNotAnError(t *testing.T) {
	f := kbFixture(t)
	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("no tools/kb.yaml must not be an error: %v", err)
	}
	if cfg.KB.CorpusDir != "" {
		t.Errorf("corpus = %q, want empty (knowledge base off)", cfg.KB.CorpusDir)
	}
	if cfg.KB.BatchSize != 10 || cfg.KB.TopK != 8 {
		t.Errorf("defaults = batch %d / topk %d, want 10 / 8", cfg.KB.BatchSize, cfg.KB.TopK)
	}
	if len(cfg.KB.Splitter.Headers) != 0 {
		t.Errorf("splitter.headers = %v, want empty (no default: the field is inert in v2)",
			cfg.KB.Splitter.Headers)
	}
}

func TestKBCorpusRejectsAbsolutePath(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: /usr/share/doc\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	err := loadErr(t, f)
	if err == nil {
		t.Fatal("an absolute corpus path was accepted")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Errorf("error should say why, got: %v", err)
	}
}

// TestKBTheOldCorpusKeyStillLoads proves that the `corpus` → `corpus_dir`
// rename does not break a working configuration.
//
// ★ A rename that breaks the people who already use the thing is a
//
//	migration, not a rename — and the project's rule is that a **silent**
//	break is the worst outcome. The old key loads, and the loader says so
//	in `deferred`, so the user learns about it at startup.
func TestKBTheOldCorpusKeyStillLoads(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("the old `corpus` key must still load: %v", err)
	}
	if cfg.KB.CorpusDir != "docs" {
		t.Errorf("CorpusDir = %q, want docs (the old key's value)", cfg.KB.CorpusDir)
	}
	if cfg.KB.CorpusLegacy != "" {
		t.Errorf("CorpusLegacy = %q, want empty (it is read once, then dropped)",
			cfg.KB.CorpusLegacy)
	}

	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "corpus_dir") && strings.Contains(d, "renamed") {
			found = true
		}
	}
	if !found {
		t.Errorf("the rename must be reported in deferred, got %v", cfg.Deferred())
	}
}

// TestKBTheNewKeyWinsOverTheOldOne proves the precedence when both spellings
// are present: the current key is the one that counts.
func TestKBTheNewKeyWinsOverTheOldOne(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml",
		"corpus: old\ncorpus_dir: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.KB.CorpusDir != "docs" {
		t.Errorf("CorpusDir = %q, want docs (the new key wins)", cfg.KB.CorpusDir)
	}
}

func TestKBModelMustBeEmbedding(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Models["local/Qwen3-Embedding-0.6B-GGUF"].Kind = string(decl.ModelKindChat)
	if err := cfg.validateKB(); err == nil {
		t.Fatal("a chat model was accepted as the embedding model")
	} else if !strings.Contains(err.Error(), "kind: embedding") {
		t.Errorf("error should say how to fix it, got: %v", err)
	}
}

func TestKBModelMustExist(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: docs\nmodel: local/nope\n")

	err := loadErr(t, f)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("err = %v, want a complaint about the missing model", err)
	}
}

func TestKBModelRequiredWhenCorpusSet(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: docs\n")

	err := loadErr(t, f)
	if err == nil || !strings.Contains(err.Error(), "kb.model") {
		t.Fatalf("err = %v, want a complaint about the missing model", err)
	}
}

func TestKBCorpusAloneIsNotAnError(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: docs\n")

	err := loadErr(t, f)
	if err == nil || !strings.Contains(err.Error(), "kb.model") {
		t.Fatalf("err = %v, want a complaint about the missing model", err)
	}
}

func TestKBEmptyHeadersIsAccepted(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml",
		"corpus_dir: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\nsplitter:\n  headers: {}\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("clearing a field that has no effect must not stop the configuration from starting (§9.1): %v", err)
	}
	if len(cfg.KB.Splitter.Headers) != 0 {
		t.Errorf("headers = %v, want empty", cfg.KB.Splitter.Headers)
	}
}

func TestKBSplitterHeadersReplaceNotMerge(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml",
		"corpus_dir: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\nsplitter:\n  headers: {\"##\": section}\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := cfg.KB.Splitter.Headers["###"]; ok {
		t.Errorf("headers = %v, want the project level to **replace** the default", cfg.KB.Splitter.Headers)
	}
	if got := cfg.KB.Splitter.Headers["##"]; got != "section" {
		t.Errorf("headers = %v, want ##: section", cfg.KB.Splitter.Headers)
	}
}

func TestModelDimensionsMustNotBeNegative(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Models["local/Qwen3-Embedding-0.6B-GGUF"].Dimensions = -1
	if err := cfg.validateKB(); err == nil {
		t.Fatal("a negative dimension was accepted")
	}
}

func TestModelKindDefaultsToChat(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for ref, m := range cfg.Models {
		if m.Kind != "" && m.Kind != string(decl.ModelKindChat) {
			t.Errorf("%s kind = %q, want chat or empty", ref, m.Kind)
		}
		if m.IsEmbedding() {
			t.Errorf("%s must not be an embedding model by default", ref)
		}
	}
}

func TestKBCategoryIsRegistered(t *testing.T) {
	var found bool
	for _, c := range Categories {
		if c.Dir == DirTools {
			found = true
			if !c.Editable {
				t.Error("kb/ should be editable")
			}
			if !c.Hot() {
				t.Error("kb/ should hot-reload")
			}
			if c.Ext != ".yaml" {
				t.Errorf("kb ext = %q", c.Ext)
			}
		}
	}
	if !found {
		t.Fatalf("kb is not in Categories: %v", dirNames(Categories))
	}
}

func dirNames(cs []Category) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Dir
	}
	return out
}

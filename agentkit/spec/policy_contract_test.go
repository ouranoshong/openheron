package spec

import (
	"testing"

	"github.com/ouranoshong/openheron/kb"
)

// ★ The tier model: the default is ask, and the exceptions are named by
//
//	the file that provides the tool.
func TestAConfigurationThatNamesNoExceptionsAllowsNothing(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AllowedTools(); len(got) != 0 {
		t.Fatalf("a configuration that names no exceptions must allow nothing, got %v", got)
	}
}

// ★ The knowledge base names its own exceptions: the search reads, the
//
//	index writes to the disk.
func TestTheKnowledgeBaseNamesItsOwnExceptions(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/kb.yaml", "corpus_dir: \"\"\nallows: ["+kb.ToolSearch+"]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, n := range cfg.AllowedTools() {
		allowed[n] = true
	}
	if !allowed[kb.ToolSearch] {
		t.Fatalf("%s must run without a nod — a read-only tool behind approval is one the "+
			"model learns not to use", kb.ToolSearch)
	}
	if allowed[kb.ToolIndex] {
		t.Fatalf("%s must ask — indexing writes to the index on disk", kb.ToolIndex)
	}
}

// ★ The exceptions are the tools that run without a nod; everything else
//
//	asks.
func TestOnlyTheNamedToolsRunWithoutANod(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\nallows: [request_get]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, n := range cfg.AllowedTools() {
		allowed[n] = true
	}
	if !allowed["request_get"] {
		t.Fatal("request_get must run without a nod — the file names it")
	}
	if allowed["request_post"] {
		t.Fatal("request_post must ask — nobody named it")
	}
}

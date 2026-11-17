package spec

import (
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// ★ The `writes:` key says which of a server's own tools write. It is the
//
//	same vocabulary as `allows:` — the server's own tool names — and it
//	lives in the server's own file, beside them.
func TestTheServerFileNamesItsOwnWriters(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nwrites: [create_issue, delete_repo]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := cfg.MCPs["github"]
	if !ok {
		t.Fatalf("the server must be loaded, got %v", cfg.MCPs)
	}
	if len(srv.Writes) != 2 {
		t.Fatalf("writes = %v, want two names", srv.Writes)
	}
	if srv.Writes[0] != "create_issue" || srv.Writes[1] != "delete_repo" {
		t.Fatalf("writes = %v", srv.Writes)
	}
}

// A server that names no writer has none — the key is an allow-list, and
// an absent key means the same thing as an empty list.
func TestAServerWithoutWritersHasNone(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPs["github"].Writes; len(got) != 0 {
		t.Fatalf("writes = %v, want none", got)
	}
}

// ★ `writes:` is separate from `allows:`. A tool that runs without a nod
//
//	(the tier) and a tool that writes (the trusted mode) are two different
//	questions, and one answer must not answer the other.
func TestWritesAndAllowsAreTwoDifferentLists(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nallows: [read_issue]\nwrites: [create_issue]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	srv := cfg.MCPs["github"]
	if len(srv.Allows) != 1 || srv.Allows[0] != "read_issue" {
		t.Fatalf("allows = %v", srv.Allows)
	}
	if len(srv.Writes) != 1 || srv.Writes[0] != "create_issue" {
		t.Fatalf("writes = %v", srv.Writes)
	}
}

// The key is optional, and a file that does not carry it still loads.
func TestTheWritesKeyIsOptional(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a server without writers must load: %v", err)
	}
	if cfg.MCPs["github"] == nil {
		t.Fatal("the server must be loaded")
	}
}

// The names are the server's own tool names — not the exposed ones. The
// namespace is added when the tool is mounted.
func TestTheWritersAreNamedInTheServersOwnVocabulary(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nwrites: [create_issue]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range cfg.MCPs["github"].Writes {
		if name == decl.MCPToolPrefix+"github__create_issue" {
			t.Fatal("the file must name the tool in the server's own vocabulary")
		}
	}
}

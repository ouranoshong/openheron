package build

import (
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// ★ The writers are assembled from the configuration, not from the server:
// the entry exists before the server is up, and a server that is down
//
//	must not change what the gate believes.
func TestTheWritersAreAssembledFromTheConfiguration(t *testing.T) {
	files := decl.MCPFiles{"github": mcpCfg("github", func(c *decl.MCPServerCfg) {
		c.Writes = []string{"create_issue", "delete_repo"}
	})}

	names := mcpTrustedWriteNames(files)
	if !names["mcp__github__create_issue"] {
		t.Fatalf("the writer must be assembled, got %v", names)
	}
	if !names["mcp__github__delete_repo"] {
		t.Fatalf("the writer must be assembled, got %v", names)
	}
	// ★ The declaration names that tool, not the whole server.
	if names["mcp__github__read_issue"] {
		t.Fatalf("a tool nobody declared must not be a writer: %v", names)
	}
}

// A server that declares no writer contributes none.
func TestAServerWithoutWritersContributesNone(t *testing.T) {
	files := decl.MCPFiles{"github": mcpCfg("github")}

	if names := mcpTrustedWriteNames(files); len(names) != 0 {
		t.Fatalf("writers = %v, want none", names)
	}
}

// ★ The writers and the allow-list are two different lists. A tool that
//
//	runs without a nod is not a writer, and a writer is not automatically
//	allowed to run — the tier table is its own question.
func TestTheWritersAreNotTheAllowList(t *testing.T) {
	files := decl.MCPFiles{"github": mcpCfg("github", func(c *decl.MCPServerCfg) {
		c.Allows = []string{"read_issue"}
		c.Writes = []string{"create_issue"}
	})}

	allowed := mcpAllowedNames(files)
	writers := mcpTrustedWriteNames(files)

	if !allowed["mcp__github__read_issue"] {
		t.Fatalf("the allow-list must be the allow-list: %v", allowed)
	}
	if allowed["mcp__github__create_issue"] {
		t.Fatalf("a writer is not automatically allowed to run: %v", allowed)
	}
	if writers["mcp__github__read_issue"] {
		t.Fatalf("a reader is not a writer: %v", writers)
	}
}

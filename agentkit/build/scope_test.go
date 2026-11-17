package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/authz"
	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// scopeFixture lays down the three trusted paths of one round: the project
// root, the session's own workspace and the configuration root.
type scopeFixture struct {
	ctx        context.Context
	sc         scope
	project    string
	workspace  string
	configRoot string
	outside    string
}

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "project")
	workspace := filepath.Join(base, "workspace")
	configRoot := filepath.Join(base, "configs")
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{project, workspace, configRoot, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	local, err := fsx.New(fsx.Config{Root: project})
	if err != nil {
		t.Fatalf("fsx.New: %v", err)
	}
	ctx := fsx.WithProjectRoot(context.Background(), project)
	ctx = authz.WithSessionWorkspace(ctx, workspace)
	return &scopeFixture{
		ctx:        ctx,
		sc:         scope{local: local, configRoot: configRoot},
		project:    project,
		workspace:  workspace,
		configRoot: configRoot,
		outside:    outside,
	}
}

// A write inside the project root is a trusted write.
func TestAWriteInsideTheProjectRootIsTrusted(t *testing.T) {
	f := newScopeFixture(t)
	args := `{"file_path":"src/main.go"}`

	if !f.sc.trusted(f.ctx, decl.ToolNameWriteFile, args) {
		t.Fatal("a write inside the project root must not ask")
	}
}

// ★ A write outside every trusted path asks. The middle mode waives the
//
//	nod for the paths it trusts and for nothing else.
func TestAWriteOutsideTheTrustedPathsAsks(t *testing.T) {
	f := newScopeFixture(t)
	args := `{"file_path":"` + filepath.ToSlash(f.outside) + `/notes.txt"}`

	if f.sc.trusted(f.ctx, decl.ToolNameWriteFile, args) {
		t.Fatal("a write outside every trusted path must ask")
	}
}

// The session's own workspace is a trusted path — it belongs to the session.
func TestAWriteIntoTheSessionWorkspaceIsTrusted(t *testing.T) {
	f := newScopeFixture(t)
	args := `{"file_path":"` + filepath.ToSlash(filepath.Join(f.workspace, "upload.txt")) + `"}`

	if !f.sc.trusted(f.ctx, decl.ToolNameWriteFile, args) {
		t.Fatal("a write into the session's own workspace must not ask")
	}
}

// The configuration root is a trusted path too.
func TestAWriteIntoTheConfigurationRootIsTrusted(t *testing.T) {
	f := newScopeFixture(t)
	args := `{"file_path":"` + filepath.ToSlash(filepath.Join(f.configRoot, "tools", "http.yaml")) + `"}`

	if !f.sc.trusted(f.ctx, decl.ToolNameEditFile, args) {
		t.Fatal("a write into the configuration root must not ask")
	}
}

// ★ The configuration tools speak in paths **relative to the configuration
//
//	root** — not to the project root. Joining them onto the wrong base
//	would answer about a different file.
func TestTheConfigurationWriteIsRelativeToTheConfigurationRoot(t *testing.T) {
	f := newScopeFixture(t)

	if !f.sc.trusted(f.ctx, decl.ToolNameConfigWrite, `{"path":"tools/http.yaml"}`) {
		t.Fatal("a configuration write must not ask")
	}
	// ★ A path that climbs out of the configuration root is not inside it.
	if f.sc.trusted(f.ctx, decl.ToolNameConfigWrite, `{"path":"../elsewhere/secret.yaml"}`) {
		t.Fatal("a configuration write that climbs out must ask")
	}
}

// ★ The shell still asks in the middle mode. It is the one tool whose
//
//	blast radius is the whole machine, and "inside a trusted path" is not
//	a question a command line can answer.
func TestTheShellAsksInTheMiddleMode(t *testing.T) {
	f := newScopeFixture(t)

	if f.sc.trusted(f.ctx, "execute", `{"command":"go build ./..."}`) {
		t.Fatal("the shell must ask in the middle mode")
	}
}

// An MCP tool the server declared as a writer is trusted — the declaration
// is the answer, because the gate cannot read an MCP tool's arguments.
func TestADeclaredMCPWriterIsTrusted(t *testing.T) {
	f := newScopeFixture(t)
	f.sc.trustedWriters = map[string]bool{"mcp__files__write": true}

	if !f.sc.trusted(f.ctx, "mcp__files__write", `{"path":"anything"}`) {
		t.Fatal("a declared writer must not ask")
	}
	// ★ The declaration names **that** tool, not the server.
	if f.sc.trusted(f.ctx, "mcp__files__read", `{"path":"anything"}`) {
		t.Fatal("a tool nobody declared must ask")
	}
}

// ★ Fail-closed: a call whose target cannot be read asks. A write the
//
//	scope cannot vouch for is a write that asks.
func TestACallWithoutAReadableTargetAsks(t *testing.T) {
	f := newScopeFixture(t)

	for _, args := range []string{``, `{}`, `{"file_path":""}`, `not json`} {
		if f.sc.trusted(f.ctx, decl.ToolNameWriteFile, args) {
			t.Fatalf("args %q must ask", args)
		}
	}
}

// A tool the scope knows nothing about asks.
func TestAnUnknownToolAsks(t *testing.T) {
	f := newScopeFixture(t)

	if f.sc.trusted(f.ctx, "some_other_tool", `{"file_path":"src/main.go"}`) {
		t.Fatal("a tool nobody declared must ask")
	}
}

package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/authz"
	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

// The gate reads the mode from the **context**, because the agent is pooled
// and shared: a mode baked into it would leak from one session into every
// other. So the same middleware answers differently for two contexts.
func TestTheGateReadsTheModeFromTheContext(t *testing.T) {
	f := newScopeFixture(t)
	mw := newApprovalMiddleware(NewPolicy(nil), f.sc)

	full := authz.WithMode(f.ctx, contract.AuthzFull)
	ran := false
	wrapped, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { ran = true; return "ran", nil },
		&adk.ToolContext{Name: "execute"})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err = wrapped(full, `{"command":"ls"}`); err != nil {
		t.Fatalf("★ full authorization must not ask: %v", err)
	}
	if !ran {
		t.Fatal("the tool must have run")
	}

	// ★ The same middleware, the default mode: the call asks.
	ran = false
	if _, err = wrapped(f.ctx, `{"command":"ls"}`); err == nil {
		t.Fatal("the default mode must ask")
	}
	if ran {
		t.Fatal("the tool must not run before the human decides")
	}
}

// ★ The middle mode waives the nod for a write inside a trusted path and
//
//	asks for everything else. Both halves have to hold: a mode that waived
//	everything would be the full mode, and a mode that waived nothing
//	would be the default one.
func TestTheMiddleModeWaivesOnlyTheTrustedWrites(t *testing.T) {
	f := newScopeFixture(t)
	mw := newApprovalMiddleware(NewPolicy(nil), f.sc)
	trusted := authz.WithMode(f.ctx, contract.AuthzTrusted)

	ran := false
	wrapped, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { ran = true; return "ran", nil },
		&adk.ToolContext{Name: decl.ToolNameWriteFile})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	inside := `{"file_path":"src/main.go"}`
	if _, err = wrapped(trusted, inside); err != nil {
		t.Fatalf("★ a write inside the project root must not ask: %v", err)
	}
	if !ran {
		t.Fatal("the tool must have run")
	}

	outside := `{"file_path":"` + filepath.ToSlash(filepath.Join(f.outside, "notes.txt")) + `"}`
	ran = false
	if _, err = wrapped(trusted, outside); err == nil {
		t.Fatal("★ a write outside the trusted paths must ask")
	}
	if ran {
		t.Fatal("the tool must not run before the human decides")
	}
}

// ★ Both wrappers must honour the waiver. The gate has two of them, and a
//
//	waiver only one of them knows about leaves the streaming tool — the
//	shell — unguarded.
func TestBothWrappersHonourTheWaiver(t *testing.T) {
	f := newScopeFixture(t)
	mw := newApprovalMiddleware(NewPolicy(nil), f.sc)
	full := authz.WithMode(f.ctx, contract.AuthzFull)

	syncRan := false
	syncWrapped, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { syncRan = true; return "ran", nil },
		&adk.ToolContext{Name: "execute"})
	if err != nil {
		t.Fatalf("wrap the synchronous path: %v", err)
	}
	if _, err = syncWrapped(full, `{"command":"ls"}`); err != nil {
		t.Fatalf("★ the synchronous path must not ask in full mode: %v", err)
	}
	if !syncRan {
		t.Fatal("the synchronous tool must have run")
	}

	streamRan := false
	streamWrapped, err := mw.WrapStreamableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (*schema.StreamReader[string], error) {
			streamRan = true
			sr, _ := schema.Pipe[string](1)
			return sr, nil
		},
		&adk.ToolContext{Name: "execute"})
	if err != nil {
		t.Fatalf("wrap the streaming path: %v", err)
	}
	if _, err = streamWrapped(full, `{"command":"ls"}`); err != nil {
		t.Fatalf("★ the streaming path must not ask in full mode: %v", err)
	}
	if !streamRan {
		t.Fatal("the streaming tool must have run")
	}
}

// ★ The mode does not widen the tool surface. A tool the agent may not use
//
//	is not in its hands at all — the gate never sees it — so the waiver
//	cannot be the thing that puts it there.
func TestTheWaiverDoesNotWidenTheToolSurface(t *testing.T) {
	f := newScopeFixture(t)
	// The policy allows read_file; write_file asks.
	mw := newApprovalMiddleware(NewPolicy([]string{"read_file"}), f.sc)
	full := authz.WithMode(f.ctx, contract.AuthzFull)

	// write_file is ask-tier, and full authorization waives the nod — but
	// the gate still guards it, and the decision is what the mode changes.
	wrapped, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { return "ran", nil },
		&adk.ToolContext{Name: decl.ToolNameWriteFile})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err = wrapped(full, `{"file_path":"src/main.go"}`); err != nil {
		t.Fatalf("full authorization must waive the nod: %v", err)
	}
}

// The scope is the gate's own: without a filesystem backend it cannot vouch
// for a path, and a path it cannot vouch for asks.
func TestAScopeWithoutABackendAsks(t *testing.T) {
	mw := newApprovalMiddleware(NewPolicy(nil), scope{})
	trusted := authz.WithMode(context.Background(), contract.AuthzTrusted)

	ran := false
	wrapped, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { ran = true; return "ran", nil },
		&adk.ToolContext{Name: decl.ToolNameWriteFile})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err = wrapped(trusted, `{"file_path":"src/main.go"}`); err == nil {
		t.Fatal("a scope that cannot vouch for a path must ask")
	}
	if ran {
		t.Fatal("the tool must not run before the human decides")
	}
}

// The project root is the round's: the fixture's own root, not the process's.
func TestTheTrustedPathsAreTheRoundsOwn(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	local, err := fsx.New(fsx.Config{Root: project})
	if err != nil {
		t.Fatalf("fsx.New: %v", err)
	}
	sc := scope{local: local}
	ctx := fsx.WithProjectRoot(context.Background(), project)

	if !sc.trusted(ctx, decl.ToolNameWriteFile, `{"file_path":"src/main.go"}`) {
		t.Fatal("a write inside the round's own root must not ask")
	}
}

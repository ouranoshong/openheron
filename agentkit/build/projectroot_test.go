package build

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// pathLike finds a directory in a piece of text.
//
// ★ It is deliberately crude: the rule below is read by a model as a law,
// and a law that carries a path is a second answer to "where is the code".
var pathLike = regexp.MustCompile(`[A-Za-z]:[\\/]|/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/`)

// ★ The rule every agent starts from names **no directory**.
//
// ★ The project root is a fact of the round, and the round carries it in
// the context. ★ A path in the rule is the process's default project root
// — a different directory from the one the session works in — and a model
// told about two roots picks the one that reads like a law.
func TestTheBuildTimeRuleNamesNoDirectory(t *testing.T) {
	rule := instruction("")
	if pathLike.MatchString(rule) {
		t.Fatalf("the rule must not carry a directory:\n%s", rule)
	}
	if strings.Contains(rule, "project root directory is") {
		t.Fatalf("the rule must not name the project root:\n%s", rule)
	}
	if !strings.Contains(rule, "round's context") {
		t.Fatalf("the rule must point at the round's context:\n%s", rule)
	}
}

// ★ The process's default project root never reaches an instruction.
//
// ★ This is the trap the rule above closes: the builder is built once and
// serves every session, so the directory it knows is the default one —
// and a session that works in a project of its own would be told about
// the wrong directory.
func TestTheDefaultProjectRootNeverReachesAnInstruction(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, []decl.SubAgentSpec{subSpec("researcher")})
	root := filepath.Join(t.TempDir(), "projects", "default")
	b := newTestBuilderWithConfig(t, cfg, WithPaths(root, root))

	snap, err := b.snapshotFor()
	if err != nil {
		t.Fatal(err)
	}
	for id, sp := range snap.agentSpec.agents {
		if strings.Contains(sp.Instruction, root) {
			t.Fatalf("agent %q carries the process's project root:\n%s", id, sp.Instruction)
		}
	}
}

// ★ The block is read from the context at run time.
//
// ★ The tree is pooled and reused across rounds, and the root belongs to
// the round: a block read at build time would be the root of the round
// that built the tree.
func TestTheRoundBlockIsReadFromTheContext(t *testing.T) {
	mw := newRoundBlockMiddleware(func(ctx context.Context) string {
		return "Project root: " + fsx.ProjectRoot(ctx)
	})

	ctx := fsx.WithProjectRoot(context.Background(), "/work/beta")
	_, runCtx, err := mw.BeforeAgent(ctx, &adk.ChatModelAgentContext{Instruction: "the rule"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(runCtx.Instruction, "Project root: /work/beta") {
		t.Fatalf("the block must come first:\n%s", runCtx.Instruction)
	}
	if !strings.Contains(runCtx.Instruction, "the rule") {
		t.Fatalf("the instruction must survive:\n%s", runCtx.Instruction)
	}
}

// ★ A round with no root leaves the instruction alone: a block that says
// nothing is better than a block that says the wrong directory.
func TestAnEmptyRoundBlockLeavesTheInstructionAlone(t *testing.T) {
	mw := newRoundBlockMiddleware(func(context.Context) string { return "" })

	_, runCtx, err := mw.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{
		Instruction: "the rule",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runCtx.Instruction != "the rule" {
		t.Fatalf("the instruction must not change:\n%s", runCtx.Instruction)
	}
}

// ★ A sub-agent is given the round's directory.
//
// ★ The framework hands a sub-agent the request and nothing else: the
// round prompt never reaches it. ★ Without the block the root agent
// works in one directory and its sub-agents are told about another — or
// about none.
func TestTheSubAgentIsGivenTheRoundsDirectory(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, []decl.SubAgentSpec{subSpec("researcher")})
	b := newTestBuilderWithConfig(t, cfg, WithRoundBlock(func(ctx context.Context) string {
		return "Project root: " + fsx.ProjectRoot(ctx)
	}))

	snap, err := b.snapshotFor()
	if err != nil {
		t.Fatal(err)
	}
	rootSp, ok := snap.agentSpec.agentSpec(snap.agentSpec.defaultAgent)
	if !ok {
		t.Fatalf("the default agent %q is not declared", snap.agentSpec.defaultAgent)
	}
	base := b.baseConfig(snap, rootSp)
	base.ChatModel = &stubModel{}
	bc := countBuilds(t)

	sp := subSpec("researcher")
	if _, _, err := b.buildNode(context.Background(), snap, node{
		base: base, agentSpec: &sp, depth: 1,
	}); err != nil {
		t.Fatal(err)
	}

	var sub *deep.Config
	for i := range bc.inputs {
		if bc.inputs[i].Name == "researcher" {
			sub = &bc.inputs[i]
		}
	}
	if sub == nil {
		t.Fatal("the sub-agent must be built")
	}
	mw := findRoundBlock(t, sub.Handlers)
	if mw == nil {
		t.Fatal("the sub-agent must carry the round block middleware")
	}

	ctx := fsx.WithProjectRoot(context.Background(), "/work/beta")
	_, runCtx, err := mw.BeforeAgent(ctx, &adk.ChatModelAgentContext{
		Instruction: sub.Instruction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runCtx.Instruction, "/work/beta") {
		t.Fatalf("the sub-agent must be told where the code lives:\n%s", runCtx.Instruction)
	}
	if strings.Contains(runCtx.Instruction, "project root directory is") {
		t.Fatalf("the sub-agent must not be told about the process's root:\n%s", runCtx.Instruction)
	}
}

// findRoundBlock returns the round block middleware among the handlers, or
// nil when the agent carries none.
func findRoundBlock(t *testing.T, h []adk.TypedChatModelAgentMiddleware[*schema.Message]) *roundBlockMiddleware {
	t.Helper()
	for _, m := range h {
		if rm, ok := m.(*roundBlockMiddleware); ok {
			return rm
		}
	}
	return nil
}

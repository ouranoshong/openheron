package build

import (
	"bytes"
	"context"
	"encoding/gob"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/approval"
	"reflect"
	"testing"
)

func TestRootAndSubAgentShareTheSameToolChain(t *testing.T) {
	b := newTestBuilder(t)
	snap := mustSnapshot(t, b)
	policy, table, maxDepth := snap.agentSpec.policy, snap.tableFor(snap.agentSpec.defaultAgent), snap.agentSpec.maxDepth

	root := toolChain(newBoundary(policy, table), maxDepth)
	sub := subHandlers(policy, decl.SubAgentSpec{ID: "x"}, maxDepth, nil)

	if len(root) != len(sub) {
		t.Fatalf("root chain has %d middlewares, sub-agent has %d — "+
			"they must be the same chain, only the tool table differs",
			len(root), len(sub))
	}
	for i := range root {
		if reflect.TypeOf(root[i]) != reflect.TypeOf(sub[i]) {
			t.Fatalf("middleware %d differs: root has %T, sub-agent has %T",
				i, root[i], sub[i])
		}
	}
}

func TestToolChainOrderPutsFilterBeforeApproval(t *testing.T) {
	chain := toolChain(newBoundary(NewPolicy(nil), newTable("main", nil)), 1)

	fi, ai, ui := -1, -1, -1
	for i, mw := range chain {
		switch mw.(type) {
		case *filterMiddleware:
			fi = i
		case *approvalMiddleware:
			ai = i
		case *unknownToolScope:
			ui = i
		}
	}
	if fi < 0 || ai < 0 || ui < 0 {
		t.Fatalf("chain is missing a middleware: filter=%d approval=%d scope=%d", fi, ai, ui)
	}
	if fi > ai {
		t.Fatalf("filterMiddleware at %d runs after ApprovalMiddleware at %d; "+
			"a denied tool would reach the gate before it was removed", fi, ai)
	}
	if ui < fi {
		t.Fatalf("UnknownToolScope at %d runs before filterMiddleware at %d; "+
			"it would annotate the unfiltered table and offer tools this turn cannot call", ui, fi)
	}
}

func TestToolChainHasTheSafeToolMiddleware(t *testing.T) {
	chain := toolChain(newBoundary(NewPolicy(nil), newTable("main", nil)), 1)

	found := false
	for _, mw := range chain {
		if _, ok := mw.(interface {
			WrapInvokableToolCall(context.Context, adk.InvokableToolCallEndpoint, *adk.ToolContext) (adk.InvokableToolCallEndpoint, error)
		}); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("★ no SafeToolMiddleware on the chain — a tool error would kill the turn " +
			"instead of reaching the model as a readable result")
	}
}

// human decision. Without this registration the failure surfaces as a model
// error at checkpoint-commit time, which reads like a bad model rather than
// a missing registration.
func TestApprovalInfoSurvivesGob(t *testing.T) {
	in := map[string]any{"interrupt": &approval.ApprovalInfo{
		ToolName:        "execute",
		ArgumentsInJSON: `{"command":"go build ./..."}`,
	}}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out map[string]any
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, ok := out["interrupt"].(*approval.ApprovalInfo)
	if !ok {
		t.Fatalf("round trip lost the concrete type: %T", out["interrupt"])
	}
	if got.ToolName != "execute" || got.ArgumentsInJSON == "" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// ★ The gate has to guard a streaming tool the way it guards a synchronous
// one. The framework reaches a tool through one of two wrappers, and the
// shell's `execute` — the tool the policy names most often — is a
// streaming one, so a gate that only wraps the synchronous path leaves the
// very tool it is there for unguarded.
func TestApprovalGateAlsoWrapsTheStreamingPath(t *testing.T) {
	mw := newApprovalMiddleware(NewPolicy([]string{"execute"}))

	_, ok := mw.(interface {
		WrapStreamableToolCall(context.Context, adk.StreamableToolCallEndpoint, *adk.ToolContext) (adk.StreamableToolCallEndpoint, error)
	})
	if !ok {
		t.Fatal("★ the approval middleware does not wrap the streaming path — " +
			"a streaming ask-tier tool runs without a human decision")
	}
}

// The two paths must reach the same verdict: a tool that the synchronous
// gate stops, the streaming gate stops too.
func TestStreamingAndSynchronousGatesAgree(t *testing.T) {
	mw := newApprovalMiddleware(NewPolicy(nil))

	syncCalled := false
	syncWrapped, err := mw.WrapInvokableToolCall(
		context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) {
			syncCalled = true
			return "ran", nil
		},
		&adk.ToolContext{Name: "execute"},
	)
	if err != nil {
		t.Fatalf("wrap the synchronous path: %v", err)
	}

	streamCalled := false
	streamWrapped, err := mw.WrapStreamableToolCall(
		context.Background(),
		func(context.Context, string, ...tool.Option) (*schema.StreamReader[string], error) {
			streamCalled = true
			sr, _ := schema.Pipe[string](1)
			return sr, nil
		},
		&adk.ToolContext{Name: "execute"},
	)
	if err != nil {
		t.Fatalf("wrap the streaming path: %v", err)
	}

	if _, err = syncWrapped(context.Background(), `{"command":"ls"}`); err == nil {
		t.Fatal("the synchronous gate let an ask-tier tool through")
	}
	if syncCalled {
		t.Fatal("the synchronous gate ran the tool before asking")
	}

	if _, err = streamWrapped(context.Background(), `{"command":"ls"}`); err == nil {
		t.Fatal("the streaming gate let an ask-tier tool through")
	}
	if streamCalled {
		t.Fatal("the streaming gate ran the tool before asking")
	}
}

// A denied tool is refused on both paths, and the refusal is a message the
// model can read rather than an error that ends the turn.
func TestDeniedToolIsRefusedOnBothPaths(t *testing.T) {
	mw := newApprovalMiddleware(NewPolicy(nil))

	syncWrapped, err := mw.WrapInvokableToolCall(
		context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { return "ran", nil },
		&adk.ToolContext{Name: "execute"},
	)
	if err != nil {
		t.Fatalf("wrap the synchronous path: %v", err)
	}
	if _, err = syncWrapped(context.Background(), "{}"); err == nil {
		t.Fatal("the synchronous gate ran a denied tool")
	}

	streamWrapped, err := mw.WrapStreamableToolCall(
		context.Background(),
		func(context.Context, string, ...tool.Option) (*schema.StreamReader[string], error) {
			sr, _ := schema.Pipe[string](1)
			return sr, nil
		},
		&adk.ToolContext{Name: "execute"},
	)
	if err != nil {
		t.Fatalf("wrap the streaming path: %v", err)
	}
	if _, err = streamWrapped(context.Background(), "{}"); err == nil {
		t.Fatal("the streaming gate ran a denied tool")
	}
}

// An allow-tier tool is left alone on both paths — the gate must not cost
// the tools nobody has to approve.
func TestAllowedToolRunsOnBothPaths(t *testing.T) {
	mw := newApprovalMiddleware(NewPolicy([]string{"read_file"}))

	syncRan := false
	syncWrapped, err := mw.WrapInvokableToolCall(
		context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) { syncRan = true; return "ran", nil },
		&adk.ToolContext{Name: "read_file"},
	)
	if err != nil {
		t.Fatalf("wrap the synchronous path: %v", err)
	}
	if _, err = syncWrapped(context.Background(), "{}"); err != nil || !syncRan {
		t.Fatalf("the synchronous gate blocked an allow-tier tool: %v", err)
	}

	streamRan := false
	streamWrapped, err := mw.WrapStreamableToolCall(
		context.Background(),
		func(context.Context, string, ...tool.Option) (*schema.StreamReader[string], error) {
			streamRan = true
			sr, _ := schema.Pipe[string](1)
			return sr, nil
		},
		&adk.ToolContext{Name: "read_file"},
	)
	if err != nil {
		t.Fatalf("wrap the streaming path: %v", err)
	}
	if _, err = streamWrapped(context.Background(), "{}"); err != nil || !streamRan {
		t.Fatalf("the streaming gate blocked an allow-tier tool: %v", err)
	}
}

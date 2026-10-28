package build

import (
	"bytes"
	"context"
	"encoding/gob"
	"github.com/cloudwego/eino/adk"
	"openheron/agentkit/spec/decl"
	"openheron/approval"
	"reflect"
	"testing"
)

func TestRootAndSubAgentShareTheSameToolChain(t *testing.T) {
	b := newTestBuilder(t)
	snap := mustSnapshot(t, b)
	policy, table, maxDepth := snap.agentSpec.policy, snap.tableFor(snap.agentSpec.defaultAgent), snap.agentSpec.maxDepth

	root := toolChain(newBoundary(policy, table), maxDepth)
	sub := subHandlers(policy, decl.SubAgentSpec{ID: "x"}, maxDepth)

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
	chain := toolChain(newBoundary(NewPolicy(nil, nil), newTable("main", nil, nil)), 1)

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
	chain := toolChain(newBoundary(NewPolicy(nil, nil), newTable("main", nil, nil)), 1)

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

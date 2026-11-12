package build

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type stubAgent struct {
	name  string
	tools []string // names this sub-agent could invoke
}

func (s *stubAgent) Name(context.Context) string        { return s.name }
func (s *stubAgent) Description(context.Context) string { return "stub" }

func (s *stubAgent) Run(context.Context, *adk.TypedAgentInput[*schema.Message], ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	gen.Close()
	return iter
}

// defaultTestPolicy is the tier table the assembly tests run against.
//
// ★ The default is ask, so the **allow list** is what the read-only tools
//
//	are on — everything else asks.
func defaultTestPolicy() Policy {
	return NewPolicy([]string{"read_file", "ls", "glob", "grep"})
}

func TestNewBuilderDefersEverythingToBuild(t *testing.T) {
	b := NewBuilder(testManager(nil))
	if b.snap != nil {
		t.Fatal("NewBuilder must not derive a snapshot before the manager is asked")
	}
	if _, _, size := b.Stats(); size != 0 {
		t.Fatalf("NewBuilder must not populate the pool, got %d entries", size)
	}
}

// The gate must wrap mutating tools and leave read-only ones untouched.
func TestApprovalMiddlewareOnlyGatesMutatingTools(t *testing.T) {
	mw := newApprovalMiddleware(defaultTestPolicy())

	for _, name := range []string{"write_file", "edit_file", "execute"} {
		tCtx := &adk.ToolContext{Name: name}
		wrapped, err := mw.WrapInvokableToolCall(context.Background(), passthroughEndpoint, tCtx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if wrapped == nil {
			t.Fatalf("%s should be wrapped by the gate", name)
		}
	}

	readOnly := &adk.ToolContext{Name: "read_file"}
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), passthroughEndpoint, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	if wrapped == nil {
		t.Fatal("read_file must pass through unchanged")
	}
}

func TestGatedToolSetMatchesBuiltinNames(t *testing.T) {
	for _, name := range []string{"write_file", "edit_file", "execute"} {
		if defaultTestPolicy().TierOf(name) != TierAsk {
			t.Errorf("%s should be gated", name)
		}
	}
	for _, name := range []string{"read_file", "ls", "glob", "grep"} {
		if defaultTestPolicy().TierOf(name) == TierAsk {
			t.Errorf("%s should not be gated", name)
		}
	}
	if defaultTestPolicy().TierOf("execute") != TierAsk {
		t.Error("execute must be covered by the gate")
	}
}

func passthroughEndpoint(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

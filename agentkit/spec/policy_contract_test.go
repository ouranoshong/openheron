package spec

import (
	"testing"

	"openheron/agentkit/spec/decl"
	"openheron/kb"
)

func TestDefaultPolicyCoversKBTools(t *testing.T) {
	p := defaultPolicy()

	if got := p.TierOf(kb.ToolSearch); got != decl.TierAllow {
		t.Errorf("effective tier of %s = %q, want %q — a read-only tool behind approval is one "+
			"the model learns not to use", kb.ToolSearch, got, decl.TierAllow)
	}
	if got := p.TierOf(kb.ToolIndex); got != decl.TierAsk {
		t.Errorf("effective tier of %s = %q, want %q", kb.ToolIndex, got, decl.TierAsk)
	}
}

func TestDefaultPolicyFallsBackToAllow(t *testing.T) {
	p := defaultPolicy()
	if got := p.TierOf("some_tool_nobody_declared"); got != decl.TierAllow {
		t.Fatalf("undeclared tool tier = %q, want %q", got, decl.TierAllow)
	}
}

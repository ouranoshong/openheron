package authz

import (
	"context"
	"testing"

	"github.com/ouranoshong/openheron/contract"
)

// ★ A context that carries no mode is a context nobody set one on, and the
//
//	safe answer is the one that asks. So the default is the answer, not
//	an absence.
func TestAContextWithoutAModeReadsAsTheDefault(t *testing.T) {
	if got := ModeOf(context.Background()); got != contract.AuthzDefault {
		t.Fatalf("mode = %q, want default", got)
	}
}

func TestTheModeTravelsInTheContext(t *testing.T) {
	ctx := WithMode(context.Background(), contract.AuthzFull)
	if got := ModeOf(ctx); got != contract.AuthzFull {
		t.Fatalf("mode = %q, want full", got)
	}
}

// ★ An empty mode becomes the default: the context always carries an answer.
func TestAnEmptyModeBecomesTheDefault(t *testing.T) {
	ctx := WithMode(context.Background(), "")
	if got := ModeOf(ctx); got != contract.AuthzDefault {
		t.Fatalf("mode = %q, want default", got)
	}
}

// The workspace travels the same way — and it is the session's own.
func TestTheSessionWorkspaceTravelsInTheContext(t *testing.T) {
	ctx := WithSessionWorkspace(context.Background(), "/tmp/session-1")
	if got := SessionWorkspace(ctx); got != "/tmp/session-1" {
		t.Fatalf("workspace = %q", got)
	}
	if got := SessionWorkspace(context.Background()); got != "" {
		t.Fatalf("a context without one must read as empty, got %q", got)
	}
}

// ★ The mode must not leak sideways: a context that carries one mode is a
//
//	child of the one that carried the other, and the child keeps its own.
func TestTheModeDoesNotLeakBetweenRounds(t *testing.T) {
	full := WithMode(context.Background(), contract.AuthzFull)
	trusted := WithMode(full, contract.AuthzTrusted)

	if got := ModeOf(trusted); got != contract.AuthzTrusted {
		t.Fatalf("the child must keep its own mode, got %q", got)
	}
	if got := ModeOf(full); got != contract.AuthzFull {
		t.Fatalf("the parent must keep its own mode, got %q", got)
	}
}

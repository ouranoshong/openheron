package obs

import (
	"context"
	"testing"
)

func TestRunIDPrefix(t *testing.T) {
	ctx := context.Background()
	if got := RunID(ctx); got != "" {
		t.Fatalf("expected no run id, got %q", got)
	}
	if got := Prefix(ctx); got != "" {
		t.Fatalf("expected no prefix, got %q", got)
	}

	ctx = WithRunID(ctx, "abc123")
	if got := RunID(ctx); got != "abc123" {
		t.Fatalf("expected abc123, got %q", got)
	}
	if got := Prefix(ctx); got != "[run=abc123] " {
		t.Fatalf("expected the run prefix, got %q", got)
	}
}

func TestEmptyRunIDIsIgnored(t *testing.T) {
	ctx := WithRunID(context.Background(), "")
	if got := RunID(ctx); got != "" {
		t.Fatalf("expected no run id, got %q", got)
	}
}

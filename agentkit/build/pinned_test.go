package build

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/contract"
)

// TestAPinnedBuildReturnsTheTreeItPinned is the builder's half of the pin.
//
// ★ A round that was interrupted asks for the tree it started with, by
//
//	fingerprint. ★ The builder answers with that tree — not with whatever
//	the disk says now.
func TestAPinnedBuildReturnsTheTreeItPinned(t *testing.T) {
	b := newTestBuilder(t)
	ctx := context.Background()

	agent, fingerprint, err := b.Build(ctx, "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint == "" {
		t.Fatal("★ a build must name its tree: the resume asks for it by name")
	}

	// ★ The configuration moved on: the tree the round started with is no
	//	longer the current one.
	managerOf(t, b).set(minimalTestConfig())

	pinned, pinnedFingerprint, err := b.Build(ctx, "s1", "", contract.TurnQuery, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if pinnedFingerprint != fingerprint {
		t.Fatalf("★ the pinned build must report the tree it pinned, got %q", pinnedFingerprint)
	}
	if pinned != agent {
		t.Fatal("★ the pinned build must return the tree the round started with")
	}
}

// TestAPinnedBuildSaysWhenTheTreeIsGone is the honest fallback.
//
// ★ The tree is gone when the process restarted: the pool lives in the
//
//	process, and a restart takes it with it. ★ The round then continues on
//	the current tree — and it says so, because a silent swap is the very
//	bug the pin exists to stop.
func TestAPinnedBuildSaysWhenTheTreeIsGone(t *testing.T) {
	var logged strings.Builder
	b := newTestBuilder(t)
	b.log = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()

	agent, _, err := b.Build(ctx, "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil {
		t.Fatal("the first build must produce an agent")
	}

	got, fingerprint, err := b.Build(ctx, "s1", "", contract.TurnQuery, "a-tree-from-another-process")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("the round must continue on the current tree")
	}
	if fingerprint == "a-tree-from-another-process" {
		t.Fatal("★ a tree that is gone cannot be reported as the tree in use")
	}
	if !strings.Contains(logged.String(), "the pinned tree is gone") {
		t.Fatalf("★ the fallback must say so, logged: %s", logged.String())
	}
}

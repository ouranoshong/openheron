package fsx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// ★ The project root of a round is **per session**, and the backend is
//
//	shared: one backend serves every round, and the root that matters is
//	the one the round in flight works in. ★ These tests pin the two
//	consequences: the boundary follows the round, and the round's root
//	replaces the backend's own — it does not widen it.

// twoProjects builds two project roots side by side.
func twoProjects(t *testing.T) (alpha, beta string) {
	t.Helper()
	base := t.TempDir()
	alpha = filepath.Join(base, "alpha")
	beta = filepath.Join(base, "beta")
	mkdir(t, alpha)
	mkdir(t, beta)
	write(t, filepath.Join(alpha, "a.md"), "alpha")
	write(t, filepath.Join(beta, "b.md"), "beta")
	return alpha, beta
}

func TestTheRoundRootIsWhereAWriteLands(t *testing.T) {
	alpha, beta := twoProjects(t)
	b := newTestBackend(t, alpha)
	ctx := WithProjectRoot(context.Background(), beta)

	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "b.md", Content: "written"}); err != nil {
		t.Fatalf("write into the round's project: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(beta, "b.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "written" {
		t.Fatalf("content = %q, want %q", got, "written")
	}
}

func TestTheRoundRootReplacesTheBackendsOwn(t *testing.T) {
	alpha, _ := twoProjects(t)
	b := newTestBackend(t, alpha)
	ctx := WithProjectRoot(context.Background(), filepath.Join(t.TempDir(), "elsewhere"))

	// ★ The backend's own root is **not** writable in this round: the
	//   session works in another project, and a session must not write into
	//   a directory that is not its own.
	err := b.Write(ctx, &filesystem.WriteRequest{FilePath: filepath.Join(alpha, "a.md"), Content: "no"})
	if err == nil {
		t.Fatal("the write into the other project must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(alpha, "a.md")); statErr != nil {
		t.Fatalf("the file must be untouched: %v", statErr)
	}
}

func TestWithoutARoundRootTheBackendsOwnHolds(t *testing.T) {
	alpha, _ := twoProjects(t)
	b := newTestBackend(t, alpha)

	if err := b.Write(context.Background(), &filesystem.WriteRequest{FilePath: "a.md", Content: "written"}); err != nil {
		t.Fatalf("write into the backend's own root: %v", err)
	}
}

func TestTheExtraRootsSurviveTheRoundRoot(t *testing.T) {
	alpha, beta := twoProjects(t)
	shared := t.TempDir()
	b := newTestBackend(t, alpha, shared)
	ctx := WithProjectRoot(context.Background(), beta)

	// ★ The shared directories — the uploads and the skills — belong to no
	//   project, and they stay writable in every round.
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: filepath.Join(shared, "note.md"), Content: "note"}); err != nil {
		t.Fatalf("write into the shared root: %v", err)
	}
}

func TestARelativePathResolvesAgainstTheRoundRoot(t *testing.T) {
	alpha, beta := twoProjects(t)
	b := newTestBackend(t, alpha)
	ctx := WithProjectRoot(context.Background(), beta)

	content, err := b.Read(ctx, &filesystem.ReadRequest{FilePath: "b.md"})
	if err != nil {
		t.Fatalf("read the relative path: %v", err)
	}
	if !strings.Contains(string(content.Content), "beta") {
		t.Fatalf("content = %q, want the round's file", content.Content)
	}
}

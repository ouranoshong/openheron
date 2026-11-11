package fsx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// ★ The built-in engine is the fallback: the tool has to work on a machine
//   with nothing installed, and ripgrep is not one of the things Windows
//   ships. These tests are the machine without ripgrep.

// newFallbackBackend builds a backend that uses the built-in engine.
func newFallbackBackend(t *testing.T, root string, extra ...string) *Local {
	t.Helper()
	b, err := New(Config{Root: root, ExtraRoots: extra})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// ★ The engine is picked when the backend is built; the test takes it
	//   away the way a machine without ripgrep does.
	b.rgPath = ""
	return b
}

// fallbackGrep runs a search with the built-in engine.
func fallbackGrep(t *testing.T, b *Local, req *filesystem.GrepRequest) []filesystem.GrepMatch {
	t.Helper()
	matches, err := b.GrepRaw(context.Background(), req)
	if err != nil {
		t.Fatalf("GrepRaw: %v", err)
	}
	return matches
}

// contentOf collects the contents of the matches.
func contentOf(matches []filesystem.GrepMatch) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Content)
	}
	return out
}

// pathsOf collects the paths of the matches.
func pathsOf(matches []filesystem.GrepMatch) map[string]bool {
	out := map[string]bool{}
	for _, m := range matches {
		out[m.Path] = true
	}
	return out
}

// noteOf returns the note the last match carries, if any.
//
// ★ The note rides on the content of the last match: the renderer counts
//
//	the paths and the entries it is given, so a note that was an entry of
//	its own would turn "two files" into "three files".
func noteOf(matches []filesystem.GrepMatch) string {
	if len(matches) == 0 {
		return ""
	}
	content := matches[len(matches)-1].Content
	idx := strings.Index(content, "\n…")
	if idx < 0 {
		return ""
	}
	return content[idx+1:]
}

// hitOf returns the content of a match without the note that rides on it.
func hitOf(m filesystem.GrepMatch) string {
	if idx := strings.Index(m.Content, "\n…"); idx >= 0 {
		return m.Content[:idx]
	}
	return m.Content
}

// ★ The search finds the line, and the result says which engine ran — a
//
//	search that quietly used a different engine reads as complete when it
//	is not.
func TestTheBuiltinEngineFindsMatches(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package main\n\nfunc main() {}\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "func main"})

	if len(matches) != 1 {
		t.Fatalf("got %d matches, want the hit: %+v", len(matches), matches)
	}
	if matches[0].Path != "a.go" || matches[0].Line != 3 || hitOf(matches[0]) != "func main() {}" {
		t.Fatalf("match = %+v, want a.go:3", matches[0])
	}
	if !strings.Contains(noteOf(matches), "built-in engine") {
		t.Fatalf("note = %q, want it to name the engine", noteOf(matches))
	}
}

// A search with no results stays "no results": the renderer counts the
// paths it is given, so a note on its own would be one file found.
func TestTheBuiltinEngineFindsNothing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package main\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "nothing-here"})

	if len(matches) != 0 {
		t.Fatalf("got %d matches, want none: %+v", len(matches), matches)
	}
}

func TestTheBuiltinEngineIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "Error\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "error", CaseInsensitive: true})
	if len(matches) != 1 || hitOf(matches[0]) != "Error" {
		t.Fatalf("got %+v, want the line", matches)
	}

	matches = fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "error"})
	if len(matches) != 0 {
		t.Fatalf("got %+v, want nothing without -i", matches)
	}
}

// ★ A glob without a separator matches the file name, the way ripgrep's
//
//	--glob does: "*.go" is every Go file at any depth.
func TestTheBuiltinEngineFiltersByGlob(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "docs"))
	write(t, filepath.Join(root, "a.go"), "hit\n")
	write(t, filepath.Join(root, "docs", "b.md"), "hit\n")
	write(t, filepath.Join(root, "docs", "c.go"), "hit\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit", Glob: "*.go"})
	paths := pathsOf(matches)
	if !paths["a.go"] || !paths["docs/c.go"] || paths["docs/b.md"] {
		t.Fatalf("paths = %v, want the two Go files", paths)
	}

	matches = fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit", Glob: "docs/*.md"})
	paths = pathsOf(matches)
	if !paths["docs/b.md"] || paths["a.go"] {
		t.Fatalf("paths = %v, want the one markdown file", paths)
	}
}

func TestTheBuiltinEngineFiltersByFileType(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "hit\n")
	write(t, filepath.Join(root, "b.txt"), "hit\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit", FileType: "go"})
	paths := pathsOf(matches)
	if !paths["a.go"] || paths["b.txt"] {
		t.Fatalf("paths = %v, want the Go file only", paths)
	}
}

// ★ A type name the table does not know is an error, not a silent
//
//	mismatch: a search that quietly looked at the wrong files is worse
//	than one that says it cannot.
func TestTheBuiltinEngineRejectsAnUnknownFileType(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "hit\n")
	b := newFallbackBackend(t, root)

	_, err := b.GrepRaw(context.Background(), &filesystem.GrepRequest{Pattern: "hit", FileType: "wat"})
	if err == nil {
		t.Fatal("an unknown file type must be refused")
	}
	if !strings.Contains(err.Error(), "wat") {
		t.Fatalf("error = %v, want it to name the type", err)
	}
}

// Hidden files are skipped, the way ripgrep skips them.
func TestTheBuiltinEngineSkipsHiddenFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".hidden"), "hit\n")
	write(t, filepath.Join(root, "visible"), "hit\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit"})
	for _, m := range matches {
		if m.Path == ".hidden" {
			t.Fatal("a hidden file must not be searched")
		}
	}
	if len(matches) != 1 {
		t.Fatalf("got %+v, want the visible file", matches)
	}
}

// A binary file is skipped, the way ripgrep skips it.
func TestTheBuiltinEngineSkipsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "blob.bin"), "hit\x00binary\n")
	write(t, filepath.Join(root, "a.txt"), "hit\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit"})
	for _, m := range matches {
		if m.Path == "blob.bin" {
			t.Fatal("a binary file must not be searched")
		}
	}
}

// The cap is the same one the ripgrep path uses, and the notice is the
// same words.
func TestTheBuiltinEngineCapsMatches(t *testing.T) {
	root := t.TempDir()
	var sb strings.Builder
	for i := 0; i < maxGrepMatches*2; i++ {
		fmt.Fprintf(&sb, "hit %d\n", i)
	}
	write(t, filepath.Join(root, "hits.txt"), sb.String())
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit"})
	if len(matches) != maxGrepMatches {
		t.Fatalf("got %d matches, want %d", len(matches), maxGrepMatches)
	}
	if last := matches[len(matches)-1]; !strings.Contains(last.Content, "cap") {
		t.Fatalf("last match = %+v, want the cap hint", last)
	}
}

// The context lines come back as their own entries, the way ripgrep's
// --json reports them.
func TestTheBuiltinEngineReturnsContextLines(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "one\ntwo\nthree\nfour\nfive\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{
		Pattern:     "three",
		BeforeLines: 1,
		AfterLines:  1,
	})

	got := contentOf(matches)
	want := []string{"two", "three", "four"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if hitOf(matches[i]) != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if matches[1].Line != 3 {
		t.Fatalf("the match is on line %d, want 3", matches[1].Line)
	}
}

// A multiline pattern matches across the lines.
func TestTheBuiltinEngineMatchesAcrossLines(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "start\nmiddle\nend\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{
		Pattern:         "start.*end",
		EnableMultiline: true,
	})

	if len(matches) != 1 {
		t.Fatalf("got %+v, want the hit", matches)
	}
	if !strings.Contains(hitOf(matches[0]), "middle") {
		t.Fatalf("content = %q, want the matched block", hitOf(matches[0]))
	}
	if matches[0].Line != 1 {
		t.Fatalf("the match starts on line %d, want 1", matches[0].Line)
	}
}

// ★ A pattern that is not a regular expression is searched for literally,
//
//	and the result says so. The model asked for a string, and a string is
//	what it gets.
func TestTheBuiltinEngineSearchesLiterallyWhenThePatternIsNotARegex(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "the c++ compiler\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "c++"})

	if len(matches) != 1 {
		t.Fatalf("got %+v, want the hit", matches)
	}
	if hitOf(matches[0]) != "the c++ compiler" {
		t.Fatalf("content = %q, want the literal hit", hitOf(matches[0]))
	}
	note := noteOf(matches)
	if !strings.Contains(note, "literally") {
		t.Fatalf("note = %q, want it to say the search was literal", note)
	}
	if !strings.Contains(note, "built-in engine") {
		t.Fatalf("note = %q, want it to name the engine", note)
	}
}

// ★ "No matches" for a pattern that was never interpreted the way it was
//
//	written is the answer that reads as complete when it is not. The call
//	fails with the reason instead.
func TestTheBuiltinEngineRefusesToReportNoMatchesForAnInvalidPattern(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "nothing here\n")
	b := newFallbackBackend(t, root)

	_, err := b.GrepRaw(context.Background(), &filesystem.GrepRequest{Pattern: "a(b"})
	if err == nil {
		t.Fatal("an invalid pattern with no literal hit must not read as 'no matches'")
	}
	if !strings.Contains(err.Error(), "not a valid regular expression") {
		t.Fatalf("error = %v, want it to say the pattern was not a regular expression", err)
	}
}

// The walk has the same budget as every other walk, and says so.
func TestTheBuiltinEngineStopsAtItsBudget(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 200; i++ {
		dir := filepath.Join(root, fmt.Sprintf("d%03d", i))
		mkdir(t, dir)
		write(t, filepath.Join(dir, "a.txt"), "hit\n")
	}
	b := newFallbackBackend(t, root)
	b.walkBudget = 20 * time.Millisecond

	_, err := b.GrepRaw(context.Background(), &filesystem.GrepRequest{Pattern: "hit"})
	if err == nil {
		t.Fatal("the search must not run past its budget")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error = %v, want it to name the budget", err)
	}
}

// ★ A machine without ripgrep gets the built-in engine, and the result
//
//	says which one ran.
func TestGrepFallsBackWhenRipgrepIsMissing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "hit\n")
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit"})

	if len(matches) == 0 {
		t.Fatal("the search must still find the line")
	}
	found := false
	for _, m := range matches {
		if strings.Contains(m.Content, "built-in engine") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the result must name the engine: %+v", matches)
	}
}

// The search stays inside the project root: a file outside it is not read
// through a link.
func TestTheBuiltinEngineDoesNotFollowLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "hit\n")
	write(t, filepath.Join(root, "a.txt"), "hit\n")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	b := newFallbackBackend(t, root)

	matches := fallbackGrep(t, b, &filesystem.GrepRequest{Pattern: "hit"})
	for _, m := range matches {
		if strings.Contains(m.Path, "secret") || strings.Contains(m.Path, "link") {
			t.Fatalf("path = %q, want no link followed", m.Path)
		}
	}
}

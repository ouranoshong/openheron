package fsx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// tree builds a project root plus a sibling directory: readable, but writable from no
// backend rooted at the project.
func tree(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "proj")
	outside = filepath.Join(base, "sibling")

	mkdir(t, filepath.Join(root, "docs"))
	mkdir(t, filepath.Join(root, "node_modules", "pkg"))
	mkdir(t, filepath.Join(root, ".git", "objects"))
	mkdir(t, filepath.Join(root, "$RECYCLE.BIN", "S-1-5"))
	mkdir(t, outside)

	write(t, filepath.Join(root, "README.md"), "root")
	write(t, filepath.Join(root, "docs", "guide.md"), "guide")
	write(t, filepath.Join(root, "docs", "main.go"), "package docs")
	write(t, filepath.Join(root, "node_modules", "pkg", "noise.md"), "noise")
	write(t, filepath.Join(root, ".git", "objects", "obj.md"), "noise")
	write(t, filepath.Join(root, "$RECYCLE.BIN", "S-1-5", "junk.md"), "junk")
	write(t, filepath.Join(outside, "outside.md"), "outside")
	return root, outside
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestBackend(t *testing.T, root string, extra ...string) *Local {
	t.Helper()
	b, err := New(Config{Root: root, ExtraRoots: extra})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func paths(infos []filesystem.FileInfo) []string {
	out := make([]string, 0, len(infos))
	for _, fi := range infos {
		out = append(out, fi.Path)
	}
	return out
}

// A glob with no path walks the project root, so a sibling directory is never entered —
// that is what the original /mnt/d accident needed, not a read ban.
func TestGlobStaysInsideRoot(t *testing.T) {
	root, _ := tree(t)
	b := newTestBackend(t, root)

	infos, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{Pattern: "**/*.md"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths(infos), "\n")
	for _, want := range []string{"README.md", "docs/guide.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"outside", "node_modules", ".git", "$RECYCLE.BIN"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("walked into %s:\n%s", unwanted, got)
		}
	}
}

// The run prompt tells the model to pass "<root>/**/*.go"; that used to match nothing.
func TestGlobAbsolutePattern(t *testing.T) {
	root, _ := tree(t)
	b := newTestBackend(t, root)

	infos, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{
		Pattern: filepath.Join(root, "**", "*.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths(infos), "\n"); got != "docs/main.go" {
		t.Fatalf("got %q, want docs/main.go", got)
	}
}

// An absolute pattern is matched relative to the walk root, so one that points elsewhere is
// a contradiction between the pattern and the path, not a confinement rule.
func TestGlobAbsolutePatternOutsideRoot(t *testing.T) {
	root, outside := tree(t)
	b := newTestBackend(t, root)

	_, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{
		Pattern: filepath.Join(outside, "**", "*.md"),
	})
	if err == nil {
		t.Fatal("want an error for a pattern outside the walk root")
	}
}

// Reads are not confined (execute can read anything anyway); a read of a sibling
// directory is the case that used to fail with "outside the project root".
func TestReadAllowsOutsideRoot(t *testing.T) {
	root, outside := tree(t)
	b := newTestBackend(t, root)

	got, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: filepath.Join(outside, "outside.md")})
	if err != nil {
		t.Fatalf("read outside the root was refused: %v", err)
	}
	if got.Content != "outside" {
		t.Fatalf("got %q", got.Content)
	}
}

func TestGlobCapsResults(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxGlobResults+50; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("f%04d.txt", i)), "x")
	}
	b := newTestBackend(t, root)

	infos, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{Pattern: "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != maxGlobResults+1 {
		t.Fatalf("got %d entries, want %d + the cap line", len(infos), maxGlobResults)
	}
	if last := infos[len(infos)-1].Path; !strings.Contains(last, "cap") {
		t.Fatalf("last entry is %q, want the cap hint", last)
	}
}

func TestGlobCancelled(t *testing.T) {
	root, _ := tree(t)
	b := newTestBackend(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "**/*.md"}); err == nil {
		t.Fatal("want ctx.Err()")
	}
}

// Writes are the boundary: an escape is refused whether it is lexical, absolute or a
// symlink, and reads of the same paths are allowed by design.
func TestResolveRejectsEscapes(t *testing.T) {
	root, outside := tree(t)
	b := newTestBackend(t, root)

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, p := range []string{"../sibling/outside.md", outside, "/etc/passwd", filepath.Join(link, "outside.md")} {
		if err := b.Write(context.Background(), &filesystem.WriteRequest{FilePath: p, Content: "x"}); err == nil {
			t.Errorf("write %q was allowed", p)
		}
		if err := b.Edit(context.Background(), &filesystem.EditRequest{FilePath: p, OldString: "outside", NewString: "x"}); err == nil {
			t.Errorf("edit %q was allowed", p)
		}
	}
}

// ★ The ancestor walk must fail closed: a directory whose permissions cannot be read is a
// barrier, not "no symlink here". The first version returned the unchecked path.
func TestResolveWriteFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions do not block the walk")
	}
	if runtime.GOOS == "windows" {
		// ★ Windows has no POSIX permission bits on directories: Chmod below
		//   succeeds but does not stop the walk, so the fixture cannot exist.
		t.Skip("directory permissions are a POSIX concept")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mkdir(t, locked)
	write(t, filepath.Join(root, "ok.txt"), "x")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	b := newTestBackend(t, root)
	if err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: filepath.Join(locked, "sub", "new.txt"),
		Content:  "x",
	}); err == nil {
		t.Fatal("write behind an unreadable directory was allowed")
	}
	// The sibling write is unaffected.
	if err := b.Write(context.Background(), &filesystem.WriteRequest{FilePath: filepath.Join(root, "ok.txt"), Content: "y"}); err != nil {
		t.Fatalf("write next to the locked directory was refused: %v", err)
	}
}

// ★ Upstream read the whole match list into memory before looking at it, so one-letter
// pattern over a build tree could exhaust the process.
func TestGrepCapsMatches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxGrepMatches*2; i++ {
		fmt.Fprintf(&b, "hit %d\n", i)
	}
	write(t, filepath.Join(root, "hits.txt"), b.String())
	backend := newTestBackend(t, root)

	matches, err := backend.GrepRaw(context.Background(), &filesystem.GrepRequest{Pattern: "hit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != maxGrepMatches {
		t.Fatalf("got %d matches, want %d", len(matches), maxGrepMatches)
	}
	if last := matches[len(matches)-1]; !strings.Contains(last.Content, "cap") {
		t.Fatalf("last match is %+v, want the cap hint", last)
	}
}

func TestEmptyPathMeansRoot(t *testing.T) {
	root, _ := tree(t)
	b := newTestBackend(t, root)

	infos, err := b.LsInfo(context.Background(), &filesystem.LsInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths(infos), "\n")
	if !strings.Contains(got, "docs/") {
		t.Fatalf("empty path did not land on the root:\n%s", got)
	}
}

func TestLsMarksDirectories(t *testing.T) {
	root, _ := tree(t)
	b := newTestBackend(t, root)

	infos, err := b.LsInfo(context.Background(), &filesystem.LsInfoRequest{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	dirs, files := 0, 0
	for _, fi := range infos {
		if fi.IsDir {
			dirs++
			if !strings.HasSuffix(fi.Path, "/") {
				t.Errorf("directory %q is not marked with a trailing slash", fi.Path)
			}
			continue
		}
		files++
		if strings.HasSuffix(fi.Path, "/") {
			t.Errorf("file %q is marked as a directory", fi.Path)
		}
	}
	if dirs == 0 || files == 0 {
		t.Fatalf("expected both kinds, got %d dirs and %d files", dirs, files)
	}
}

func TestLsCapsEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxLsEntries+10; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("f%d", i)), "x")
	}
	b := newTestBackend(t, root)

	infos, err := b.LsInfo(context.Background(), &filesystem.LsInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != maxLsEntries+1 {
		t.Fatalf("got %d entries, want %d + the cap line", len(infos), maxLsEntries)
	}
}

func TestExtraRootIsReachable(t *testing.T) {
	root, _ := tree(t)
	extra := t.TempDir()
	write(t, filepath.Join(extra, "up.md"), "x")
	b := newTestBackend(t, root, extra)

	if _, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: filepath.Join(extra, "up.md")}); err != nil {
		t.Fatalf("extra root not reachable: %v", err)
	}
	// ... but it is not the default for an empty path.
	infos, err := b.LsInfo(context.Background(), &filesystem.LsInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(paths(infos), "\n"), "up.md") {
		t.Fatal("empty path landed in an extra root")
	}
}

// The walk is pruned by the pattern: a directory whose own name cannot be
// the pattern's next segment cannot hold a match, so it is not walked.
func TestGlobPrunesByPatternPrefix(t *testing.T) {
	// "docs/**" may only live under docs/.
	for _, c := range []struct {
		segment string
		want    bool
	}{
		{"docs", true},
		{"doc", false},
		{"node_modules", false},
		{"*.go", false},
	} {
		got := mayHold([]string{c.segment}, "depth/"+c.segment)
		if c.segment == "docs" {
			got = mayHold([]string{"docs", "**"}, "docs")
		}
		if got != c.want {
			t.Fatalf("mayHold(%q) = %v, want %v", c.segment, got, c.want)
		}
	}
}

// A pattern with no directory part does not walk a single directory.
func TestGlobPrunesEverythingBelowTheBase(t *testing.T) {
	if mayHold([]string{"*.go"}, "docs") {
		t.Fatal("a file-only pattern must not descend into docs")
	}
	if !mayHold([]string{"**", "*.go"}, "docs") {
		t.Fatal("a ** pattern must descend")
	}
	// "docs/**" descends under docs/ …
	if !mayHold([]string{"docs", "**", "*.md"}, "docs/sub") {
		t.Fatal("a ** pattern must descend under its literal prefix")
	}
	// … and nowhere else. (The walk only reaches a directory whose ancestors
	// matched, so the check at one depth is enough.)
	if mayHold([]string{"docs", "**", "*.md"}, "other") {
		t.Fatal("a directory outside the pattern's first segment must not descend")
	}
	// A depth past the pattern's segments holds nothing.
	if mayHold([]string{"a.md"}, "a.md/x") {
		t.Fatal("a depth past the pattern's segments must not descend")
	}
}

// The walk has a time budget.
//
// ★ The depth and result caps bound what comes back; only the budget bounds
//
//	how long the turn is blocked.
func TestGlobStopsAtItsTimeBudget(t *testing.T) {
	root, _ := tree(t)
	b, err := New(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	// ★ A **negative** budget, not a tiny positive one. The clock's resolution
	//   is coarser than a nanosecond on some platforms — Windows reads the
	//   monotonic clock in ~0.5ms steps — and `Add` truncates to that
	//   resolution, so `time.Now().Add(time.Nanosecond)` is a no-op there and
	//   the deadline never looks expired: the walk finishes, nothing says it
	//   was cut short, and the assertion below fails at random. A budget that
	//   is already an hour in the past says the same thing and cannot be
	//   quantized away.
	b.walkBudget = -time.Hour

	files, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{Pattern: "**/*"})
	if err != nil {
		t.Fatalf("GlobInfo: %v", err)
	}
	var noted bool
	for _, f := range files {
		if strings.Contains(f.Path, "budget") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("the truncated search must say so, got %+v", files)
	}
}

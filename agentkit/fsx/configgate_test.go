package fsx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// fakeGate records what the backend handed to it.
type fakeGate struct {
	owned    map[string]bool
	content  string
	writes   []string
	baseRevs []string
	reads    int
}

func (g *fakeGate) OwnsPath(p string) bool { return g.owned[p] }

func (g *fakeGate) ReadConfig(string) ([]byte, string, error) {
	g.reads++
	return []byte(g.content), "rev1", nil
}

func (g *fakeGate) WriteConfig(_ string, content []byte, baseRev string) error {
	g.writes = append(g.writes, string(content))
	g.baseRevs = append(g.baseRevs, baseRev)
	return nil
}

// ★ The gate is the whole point of the interface: a file it owns does not
// reach the disk through the filesystem, because the configuration service
// is the one that has to say whether it may.
func TestAWriteTheGateOwnsGoesThroughTheGate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agents", "main.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("on disk"), 0o644); err != nil {
		t.Fatal(err)
	}

	gate := &fakeGate{owned: map[string]bool{path: true}}
	b, err := New(Config{Root: root, ConfigGate: gate})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "new content",
	}); err != nil {
		t.Fatal(err)
	}

	if len(gate.writes) != 1 || gate.writes[0] != "new content" {
		t.Fatalf("the gate must have received the write: %+v", gate.writes)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "on disk" {
		t.Fatalf("the filesystem wrote the file itself: %q", onDisk)
	}
}

// ★ A whole-file write has no revision to check against: the framework's
// request carries none, and the model has no baseline to give.
func TestAWholeFileWriteCarriesNoRevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agents", "main.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{owned: map[string]bool{path: true}}
	b, err := New(Config{Root: root, ConfigGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if gate.baseRevs[0] != "" {
		t.Fatalf("a whole-file write must carry no revision, got %q", gate.baseRevs[0])
	}
}

// ★ The surgical edit is read through the gate too, and the revision it was
// based on travels with it.
func TestAnEditTheGateOwnsIsReadThroughTheGate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agents", "main.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// The file on disk says something else entirely: the gate is the truth.
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{owned: map[string]bool{path: true}, content: "hello world"}
	b, err := New(Config{Root: root, ConfigGate: gate})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Edit(context.Background(), &filesystem.EditRequest{
		FilePath: path, OldString: "hello", NewString: "goodbye",
	}); err != nil {
		t.Fatal(err)
	}
	if gate.reads != 1 {
		t.Fatalf("the gate must have been asked for the content, reads=%d", gate.reads)
	}
	if len(gate.writes) != 1 || gate.writes[0] != "goodbye world" {
		t.Fatalf("the edit must have been applied to the gate's content: %+v", gate.writes)
	}
	if gate.baseRevs[0] != "rev1" {
		t.Fatalf("the edit must carry the revision it was based on, got %q", gate.baseRevs[0])
	}
}

// ★ An old string that is not there is an error, not an empty patch: a
// silent no-op reads to the model as "it worked".
func TestAnEditWithTheWrongOldStringIsRefused(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agents", "main.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{owned: map[string]bool{path: true}, content: "hello world"}
	b, err := New(Config{Root: root, ConfigGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	err = b.Edit(context.Background(), &filesystem.EditRequest{
		FilePath: path, OldString: "nowhere", NewString: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want a not-found error, got %v", err)
	}
	if len(gate.writes) != 0 {
		t.Fatalf("a refused edit must not write: %+v", gate.writes)
	}
}

// ★ Everything outside the configuration root keeps the behaviour it had.
func TestAWriteTheGateDoesNotOwnIsTheFilesystemsOwn(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.md")
	gate := &fakeGate{owned: map[string]bool{}}
	b, err := New(Config{Root: root, ConfigGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "plain",
	}); err != nil {
		t.Fatal(err)
	}
	if len(gate.writes) != 0 {
		t.Fatalf("the gate must not have been consulted: %+v", gate.writes)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "plain" {
		t.Fatalf("the file was not written: %q", onDisk)
	}
}

// ★ No gate, no change: a backend that is not openheron's writes everything
// itself.
func TestWithoutAGateTheBackendWritesEverything(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.md")
	b, err := New(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: path, Content: "plain",
	}); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "plain" {
		t.Fatalf("the file was not written: %q", onDisk)
	}
}

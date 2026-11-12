package spec

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// TestTheToolsPartitionTableAgreesWithTheLoader keeps the two lists from
// drifting.
//
// ★ The loader has its own whitelist — the files `tools/` accepts — and
//
//	the dependency table has its own. ★ A file the loader reads and the
//	table does not know about would be a change that never reaches an
//	agent, and the symptom would be a configuration that "did not take
//	effect" with nothing wrong anywhere.
func TestTheToolsPartitionTableAgreesWithTheLoader(t *testing.T) {
	want := ToolFileNames()
	for i := range want {
		want[i] = DirTools + "/" + want[i]
	}
	got := ToolConfigFiles()
	if len(want) != len(got) {
		t.Fatalf("the loader accepts %v, the table knows %v", want, got)
	}
	sort.Strings(want)
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("the loader accepts %v, the table knows %v", want, got)
		}
	}
}

// TestEveryToolHasItsFileOrNone is the other half of the table.
//
// ★ A tool that configures a file has to say which one, and a tool that
//
//	has no file of its own has to say none: the answer decides what an
//	agent depends on.
func TestEveryToolHasItsFileOrNone(t *testing.T) {
	if got := ToolConfigFile(decl.ToolNameHTTPGet); got != "tools/http.yaml" {
		t.Fatalf("the HTTP tool lives in %q", got)
	}
	if got := ToolConfigFile(decl.ToolNameWebSearch); got != "tools/web_search.yaml" {
		t.Fatalf("the search tool lives in %q", got)
	}
	if got := ToolConfigFile(decl.ToolNameWikipedia); got != "tools/wikipedia.yaml" {
		t.Fatalf("the wikipedia tool lives in %q", got)
	}
	if got := ToolConfigFile(decl.ToolNameKBSearch); got != "tools/kb.yaml" {
		t.Fatalf("the knowledge-base tool lives in %q", got)
	}
	if got := ToolConfigFile(decl.ToolNameLs); got != "tools/filesystem.yaml" {
		t.Fatalf("the filesystem tools live in %q", got)
	}
	// ★ The delegation tools, the skill reader and the config tools are
	//	provided by the code: nothing on disk can change them.
	for _, name := range []string{"task", "write_todos", "skill", "config_get", "config_write"} {
		if got := ToolConfigFile(name); got != "" {
			t.Fatalf("%q has no file of its own, but the table says %q", name, got)
		}
	}
}

// TestAWriteMarksTheFile is the promise the mark makes.
//
// ★ The write path knows what it wrote, so the next turn can read the
//
//	configuration again before it builds — and "the next turn uses it"
//	stops being a race with the watcher's poll.
func TestAWriteMarksTheFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	svc := f.service(t)

	rel := DirAgent + "/main.md"
	if _, _, err := svc.Write(rel, []byte("---\nmodel: openai/fast\n---\nbody\n"), ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	marks := svc.Marks()
	if len(marks) != 1 || !strings.HasSuffix(marks[0], "main.md") {
		t.Fatalf("the write must be marked, got %v", marks)
	}
	if drained := svc.DrainMarks(); len(drained) != 1 {
		t.Fatalf("the drain must return the mark, got %v", drained)
	}
	if len(svc.Marks()) != 0 {
		t.Fatal("the drain must forget the mark")
	}
}

// TestTheMarkIsTheWrittenPathAndNothingElse keeps the mark narrow.
//
// ★ A mark names a file; it does not say the whole partition moved.
func TestTheMarkIsTheWrittenPathAndNothingElse(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	svc := f.service(t)

	rel := DirAgent + "/main.md"
	if _, _, err := svc.Write(rel, []byte("---\nmodel: openai/fast\n---\nbody\n"), ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, mark := range svc.Marks() {
		if filepath.Base(mark) != "main.md" {
			t.Fatalf("the mark names another file: %q", mark)
		}
	}
}

package spec

import (
	"strings"
	"testing"
)

// ★ The editor used to be told that the file in front of it was broken
//
//	because **another** file was. A user editing agents/main.md saw the
//	model of agents/agent-expert.md on their screen, and there was nothing
//	they could do about it from there.
//
//	The rule now: an issue carries the file it is about, and a file the
//	user is not editing is not their file.
func TestDiagnosticsAttributeAProblemToTheFileItIsIn(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// A second agent whose model is not declared: the tree is broken, and
	// it is broken in this file, not in the one being edited.
	f.write(t, "agents/agent-expert.md", "---\nmodel: openai/gpt-4o-mini\n---\nbody\n")
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/fast\n---\nbody\n")
	if d.OK {
		t.Fatal("a tree with an undeclared model must not validate")
	}
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Path != "agents/agent-expert.md" {
		t.Fatalf("path = %q, want the file the problem is in, not the one being edited", iss.Path)
	}
	if !strings.Contains(iss.Message, "openai/gpt-4o-mini") {
		t.Fatalf("the message must name the model: %q", iss.Message)
	}
}

// ★ The draft's own problem is still its own: the file being edited is the
//
//	one the user can fix, so its issues keep pointing at it.
func TestDiagnosticsKeepTheDraftsOwnProblemOnTheDraft(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/nope\n---\nbody\n")
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Path != "agents/main.md" {
		t.Fatalf("path = %q, want the file being edited", iss.Path)
	}
}

// ★ A notice belongs to the file it names. The editor shows the notices of
//
//	the file being edited, and the notices of the other files under their
//	own names — otherwise a missing environment variable in one MCP server
//	reads as a remark about the agent file in front of the user.
func TestANoticeCarriesTheFileItIsAbout(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/memory.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  MEMORY_FILE_PATH: ${OPENHERON_MCP_MEMORY_FILE}\n")
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/fast\n---\nbody\n")
	hint := findIssue(d, SeverityHint)
	if hint == nil {
		t.Fatalf("the unset variable must be reported as a hint: %+v", d.Issues)
	}
	if hint.Path != "mcps/memory.yaml" {
		t.Fatalf("path = %q, want the MCP server's file", hint.Path)
	}
	if strings.Contains(hint.Message, ".yaml.yaml") {
		t.Fatalf("the extension must not be doubled: %q", hint.Message)
	}
}

// ★ The startup log is a sentence a user pastes into a bug report, so the
//
//	path goes back onto the message — once.
func TestTheDeferredNoticeNamesTheFileOnce(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/memory.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  MEMORY_FILE_PATH: ${OPENHERON_MCP_MEMORY_FILE}\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("an unset variable must not be fatal: %v", err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "MEMORY_FILE_PATH") {
			found = true
			if !strings.Contains(d, "mcps/memory.yaml:") {
				t.Fatalf("the notice must name the file once: %q", d)
			}
			if strings.Contains(d, ".yaml.yaml") {
				t.Fatalf("the extension must not be doubled: %q", d)
			}
		}
	}
	if !found {
		t.Fatalf("the notice must be recorded, got %v", cfg.Deferred())
	}
}

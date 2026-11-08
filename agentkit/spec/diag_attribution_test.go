package spec

import (
	"strings"
	"testing"
)

// ★ The editor used to be told that the file in front of it was broken
//
//	because **another** file was. A user editing agents/main.md saw the
//	knowledge base's complaint on their screen, and there was nothing they
//	could do about it from there.
//
//	The rule now: an issue carries the file it is about, and a file the
//	user is not editing is not their file.
func TestDiagnosticsAttributeAProblemToTheFileItIsIn(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// The knowledge base names an absolute corpus: that is tools/kb.yaml's
	// problem, not the agent file the user is editing.
	f.write(t, "tools/kb.yaml", "corpus_dir: /tmp/corpus\n")
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/fast\n---\nbody\n")
	if d.OK {
		t.Fatal("a tree with an unusable knowledge base must not validate")
	}
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Path != "tools/kb.yaml" {
		t.Fatalf("path = %q, want the file the problem is in, not the one being edited", iss.Path)
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

// ★ **One agent's model is not the whole tree's business to die for.** The
//
//	rest of the configuration is good, and a process that refuses to start
//	is a process that cannot be told what is wrong with it — the editor
//	that would fix it lives in that process.
func TestAnAgentWithAnUndeclaredModelIsLeftOutAndTheTreeStillLoads(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/agent-expert.md", "---\nmodel: openai/gpt-4o-mini\n---\nbody\n")
	f.write(t, "agents/main.md",
		"---\nmodel: openai/fast\nsubagents: [agent-expert]\n---\nbody\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("one agent's model must not take the tree down: %v", err)
	}
	if _, ok := cfg.Agents["agent-expert"]; ok {
		t.Fatal("the agent must be left out of the tree")
	}
	if got := cfg.Agents["main"].SubAgents; len(got) != 0 {
		t.Fatalf("the parent's subagents list must not name a worker that cannot run: %v", got)
	}

	// ★ And the reason is said out loud: a degradation that is silent is
	//   the worst kind.
	var said bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "agent-expert") && strings.Contains(d, "left out") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the notice must be recorded, got %v", cfg.Deferred())
	}
}

// ★ The default agent is left out too. There is no session without it, but
//
//	a session is not the process: the process is also the config editor, and
//	an editor that cannot start cannot be used to fix the very thing that
//	keeps it from starting.
func TestTheDefaultAgentsUndeclaredModelLeavesItOutAndTheTreeStillLoads(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/gpt-4o\n---\nbody\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("the default agent's undeclared model must not stop the process: %v", err)
	}
	if _, ok := cfg.Agents["main"]; ok {
		t.Fatal("the default agent must be left out of the tree")
	}
	var said bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "agents/main.md") && strings.Contains(d, "left out") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the notice must name the file and say what happened: %v", cfg.Deferred())
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

// ★ The left-out agent's notice lands on **its** file, so the editor shows
//
//	it where the user can go and fix it.
func TestTheLeftOutAgentsNoticeLandsOnItsFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/agent-expert.md", "---\nmodel: openai/gpt-4o-mini\n---\nbody\n")
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/fast\n---\nbody\n")
	if !d.OK {
		t.Fatalf("the tree must load: %+v", d.Issues)
	}
	var left *Issue
	for i := range d.Issues {
		if d.Issues[i].Path == "agents/agent-expert.md" {
			left = &d.Issues[i]
		}
	}
	if left == nil {
		t.Fatalf("the left-out agent must be reported on its own file: %+v", d.Issues)
	}
	if left.Severity != SeverityHint {
		t.Fatalf("severity = %q, want a hint — the tree loads", left.Severity)
	}
	if !strings.Contains(left.Message, "left out") {
		t.Fatalf("the notice must say what happened: %q", left.Message)
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

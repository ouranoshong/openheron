package spec

import (
	"strings"
	"testing"
)

// ★ **The gate the tree needs.** A change may not take a **working** agent
//
//	out of the tree. An agent that cannot run is left out — the tree still
//	loads, the reason is said out loud, and the session that asks for it
//	is told why it is not there.
//
// ★ The gate is on **every** way a change reaches the disk: a write, a
//
//	batch, and a delete. A gate on one road is a gate on that road.
func TestAWriteThatWouldTakeAWorkingAgentOutIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// ★ The agent file names a model no provider declares. The agent is
	//   left out of the tree — and that is a **loss**: main worked before.
	_, _, err := s.Write("agents/main.md",
		[]byte("---\nmodel: openai/nope\n---\nbody\n"), "")
	if err == nil {
		t.Fatal("a write that takes a working agent out of the tree must be refused")
	}
	if writeErrKind(err) != ErrMergeRejected {
		t.Fatalf("kind = %v, want ErrMergeRejected", err)
	}
	if !strings.Contains(err.Error(), "main") {
		t.Fatalf("the error must name the agent that would be lost: %v", err)
	}
	if got := f.read(t, "agents/main.md"); !strings.Contains(got, "openai/fast") {
		t.Fatalf("the disk must be untouched: %q", got)
	}
}

// ★ The same gate on the batch road.
func TestABatchThatWouldTakeAWorkingAgentOutIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// ★ The provider is replaced by one that no longer declares the model
	//   **main** names. The agent file is untouched — the loss comes from
	//   another file, which is the whole point: the gate is about the tree,
	//   not about the file being edited.
	_, _, err := s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(
			"type: openai_compat\nbase_url: https://api.example.com\napi_key: ${TEST_LLM_KEY}\nmodels:\n  - id: other\n")},
	}, "")
	if err == nil {
		t.Fatal("a batch that takes a working agent out of the tree must be refused")
	}
	if writeErrKind(err) != ErrMergeRejected {
		t.Fatalf("kind = %v, want ErrMergeRejected", err)
	}
	if !strings.Contains(err.Error(), "main") {
		t.Fatalf("the error must name the agent that would be lost: %v", err)
	}
	if got := f.read(t, "llm/openai.yaml"); !strings.Contains(got, "fast") {
		t.Fatalf("the disk must be untouched: %q", got)
	}
}

// ★ And on the delete road: removing the provider an agent names takes
//
//	that agent out of the tree just as surely as pointing it at a model
//	that does not exist.
func TestADeleteThatWouldTakeAWorkingAgentOutIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	_, err := s.Delete("llm/openai.yaml")
	if err == nil {
		t.Fatal("a delete that takes a working agent out of the tree must be refused")
	}
	if writeErrKind(err) != ErrMergeRejected {
		t.Fatalf("kind = %v, want ErrMergeRejected", err)
	}
	if !strings.Contains(err.Error(), "main") {
		t.Fatalf("the error must name the agent that would be lost: %v", err)
	}
	if !f.exists(t, "llm/openai.yaml") {
		t.Fatal("the file must still be on disk")
	}
}

// ★ **A change may add to the tree.** An agent that did not work before and
//
//	works now is the fix, and a gate that refuses it would refuse the fix.
func TestAWriteThatBringsAnAgentIntoTheTreeIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// ★ The agent exists but cannot run: the provider that would declare
	//   its model is not there yet.
	f.write(t, "agents/worker.md", "---\nmodel: second/nope\n---\nbody\n")
	s := f.service(t)

	// ★ The provider that declares the model arrives: the agent comes into
	//   the tree, and nothing that worked stops working.
	_, _, err := s.Write("llm/second.yaml", []byte(
		"type: openai_compat\nbase_url: https://api.example.com\napi_key: ${TEST_LLM_KEY}\nmodels:\n  - id: nope\n"), "")
	if err != nil {
		t.Fatalf("a change that brings an agent into the tree must be accepted: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Agents["worker"]; !ok {
		t.Fatal("the agent must be in the tree")
	}
}

// ★ And the gate does not touch an agent that was **already** out: a change
//
//	to an unrelated file must not be refused because some other agent cannot
//	run. That is the case the whole design exists for.
func TestAChangeUnrelatedToABrokenAgentIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/worker.md", "---\nmodel: openai/nope\n---\nbody\n")
	s := f.service(t)

	if _, _, err := s.Write("tools/http.yaml",
		[]byte("methods: [get]\nallows: [read_file]\n"), ""); err != nil {
		t.Fatalf("an unrelated change must be accepted: %v", err)
	}
}

// ★ The diagnostics answer says the same thing the write path says — and
//
//	says it **on the file being edited**, because that is the change the
//	user is making.
func TestDiagnosticsRefuseAChangeThatWouldTakeAWorkingAgentOut(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/nope\n---\nbody\n")
	if d.OK {
		t.Fatal("the change must not be reported as acceptable")
	}
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Path != "agents/main.md" {
		t.Fatalf("path = %q, want the file being edited", iss.Path)
	}
	if !strings.Contains(iss.Message, "main") {
		t.Fatalf("the message must name the agent that would be lost: %q", iss.Message)
	}
}

// ★ And the file's **own** problem comes first: the consequence is what the
//
//	user cannot fix from where they are, and the cause is.
func TestTheFilesOwnProblemComesBeforeTheGate(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: sk-literal\nmodels:\n  - id: fast\n")
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Line != 3 {
		t.Fatalf("line = %d, want 3 — the file's own problem comes first (%+v)", iss.Line, d.Issues)
	}
}

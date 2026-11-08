package spec

import (
	"strings"
	"testing"
)

// ★ **A delete has to name who still points at the file.** Deleting
//   `mcps/fetch.yaml` while an agent mounts "fetch" makes the tree fail to
//   load — and the load error is a symptom: it names the deleted file and
//   says the tree is broken, and the agent whose `mcps:` list is the actual
//   reason is nowhere in it.
//
//   What the user can act on is the reference: "agents/task.md still lists
//   this server — remove it there first".

func seedMCPReferrers(t *testing.T, f *fixture) {
	t.Helper()
	f.write(t, "mcps/fetch.yaml", "transport: stdio\ncommand: fetch\n")
	f.write(t, "mcps/deepwiki.yaml", "transport: stdio\ncommand: deepwiki\n")
	f.write(t, "agents/task.md",
		"---\nmodel: openai/fast\ndescription: t\nmcps: [fetch]\n---\nbody\n")
	f.write(t, "agents/other.md",
		"---\nmodel: openai/fast\ndescription: o\nmcps: [fetch, deepwiki]\n---\nbody\n")
}

// seedOneMCPReferrer seeds a tree where exactly **one** agent mounts fetch.
func seedOneMCPReferrer(t *testing.T, f *fixture) {
	t.Helper()
	f.write(t, "mcps/fetch.yaml", "transport: stdio\ncommand: fetch\n")
	f.write(t, "agents/task.md",
		"---\nmodel: openai/fast\ndescription: t\nmcps: [fetch]\n---\nbody\n")
}

func TestDeletingAMountedServerNamesTheAgentThatMountsIt(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	seedOneMCPReferrer(t, f)
	s := f.service(t)

	_, err := s.Delete("mcps/fetch.yaml")
	if err == nil {
		t.Fatal("a delete that leaves a mounted server dangling must be refused")
	}
	if writeErrKind(err) != ErrMergeRejected {
		t.Fatalf("kind = %v, want ErrMergeRejected", err)
	}
	// ★ The referrer's **file**: that is where the user has to go.
	if !strings.Contains(err.Error(), "agents/task.md") {
		t.Fatalf("the refusal must name the file that references it: %v", err)
	}
	if !strings.Contains(err.Error(), "mcps: [fetch]") {
		t.Fatalf("the refusal must say what the reference is: %v", err)
	}
	if !strings.Contains(err.Error(), "remove that reference first") {
		t.Fatalf("the refusal must say what to do: %v", err)
	}
	if !f.exists(t, "mcps/fetch.yaml") {
		t.Fatal("the file must still be on disk")
	}
}

func TestDeletingAMountedServerNamesEveryAgentThatMountsIt(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	seedMCPReferrers(t, f)
	s := f.service(t)

	_, err := s.Delete("mcps/fetch.yaml")
	if err == nil {
		t.Fatal("the delete must be refused")
	}
	for _, who := range []string{"agents/task.md", "agents/other.md"} {
		if !strings.Contains(err.Error(), who) {
			t.Fatalf("★ every referrer must be named, %q is missing: %v", who, err)
		}
	}
}

func TestDeletingAProviderNamesTheAgentsThatUseIt(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	_, err := s.Delete("llm/openai.yaml")
	if err == nil {
		t.Fatal("a delete that takes every agent's model away must be refused")
	}
	if !strings.Contains(err.Error(), "agents/main.md") {
		t.Fatalf("the refusal must name the file that uses it: %v", err)
	}
	if !strings.Contains(err.Error(), "model: openai/fast") {
		t.Fatalf("the refusal must say what the reference is: %v", err)
	}
}

func TestDeletingADelegatedAgentNamesItsParent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/helper.md", "---\nmodel: openai/fast\ndescription: h\n---\nbody\n")
	f.write(t, "agents/parent.md",
		"---\nmodel: openai/fast\ndescription: p\nsubagents: [helper]\n---\nbody\n")
	s := f.service(t)

	_, err := s.Delete("agents/helper.md")
	if err == nil {
		t.Fatal("a delete that leaves a dangling sub-agent must be refused")
	}
	if !strings.Contains(err.Error(), "agents/parent.md") {
		t.Fatalf("the refusal must name the parent: %v", err)
	}
	if !strings.Contains(err.Error(), "subagents: [helper]") {
		t.Fatalf("the refusal must say what the reference is: %v", err)
	}
}

// ★ The default agent is not "referenced" by anybody, but a tree needs one —
//
//	and the load error ("agents/main.md does not exist") reads like a broken
//	file rather than a refused deletion.
func TestDeletingTheDefaultAgentIsRefusedWithItsOwnReason(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	_, err := s.Delete("agents/main.md")
	if err == nil {
		t.Fatal("the default agent cannot be deleted")
	}
	if !strings.Contains(err.Error(), "default agent") {
		t.Fatalf("the refusal must say why: %v", err)
	}
	if strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("★ the load error must not stand in for the reason: %v", err)
	}
}

// ★ **Deleting an agent on purpose is not losing it.** The gate exists for
//
//	the agent that stops working by accident; a user who deletes
//	agents/task.md meant to delete it, and a gate that refuses that would
//	make the file undeletable.
func TestDeletingAnAgentNobodyNamesIsAllowed(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/task.md", "---\nmodel: openai/fast\ndescription: t\n---\nbody\n")
	s := f.service(t)

	if _, err := s.Delete("agents/task.md"); err != nil {
		t.Fatalf("an agent nobody names may be deleted: %v", err)
	}
	if f.exists(t, "agents/task.md") {
		t.Fatal("the file must be gone")
	}
}

// ★ The same error, seen from the editor: it belongs to the **agent's** file,
//
//	because that is where the reference is and the only place the user can
//	remove it. A path-less error lands on the tree, and a reader there
//	cannot click their way to the fix.
func TestTheUndeclaredServerErrorLandsOnTheAgentsFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/fetch.yaml", "transport: stdio\ncommand: fetch\n")
	f.write(t, "agents/task.md",
		"---\nmodel: openai/fast\ndescription: t\nmcps: [gone]\n---\nbody\n")
	s := f.service(t)

	d, err := s.Diagnostics("agents/task.md", []byte(
		"---\nmodel: openai/fast\ndescription: t\nmcps: [gone]\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, i := range d.Issues {
		if !strings.Contains(i.Message, "not declared under mcps/") {
			continue
		}
		found = true
		if i.Path != "agents/task.md" {
			t.Fatalf("★ the issue must be on the agent's own file, got %q", i.Path)
		}
	}
	if !found {
		t.Fatalf("the undeclared server must be reported: %+v", d.Issues)
	}
}

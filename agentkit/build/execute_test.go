package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/prebuilt/deep"

	"github.com/ouranoshong/openheron/agentkit/shell"
	"github.com/ouranoshong/openheron/contract"
)

// ★ The description is the only place the dialect can be stated: the model
//
//	writes the command, and it reads the description when it writes it.
//	The framework's text is kept — it carries the quoting rules — and the
//	dialect is appended to it.
func TestExecuteDescriptionNamesTheShell(t *testing.T) {
	posix := executeToolDesc(shell.Shell{Program: "/bin/sh", Args: []string{"-c"}})
	if !strings.Contains(posix, "sandbox environment") {
		t.Fatalf("the description must keep the framework's text: %q", posix)
	}
	if !strings.Contains(posix, "POSIX") {
		t.Fatalf("the description must say the shell is POSIX: %q", posix)
	}

	win := executeToolDesc(shell.Shell{Program: "cmd.exe", Args: []string{"/c"}, Windows: true})
	if !strings.Contains(win, "sandbox environment") {
		t.Fatalf("the description must keep the framework's text: %q", win)
	}
	if !strings.Contains(win, "cmd.exe") || !strings.Contains(win, "Windows") {
		t.Fatalf("the description must name cmd.exe: %q", win)
	}
}

// ★ The framework mounts an execute tool of its own when the config carries
//
//	a shell. Two tools with one name is a model that calls the wrong one —
//	and the wrong one is the one that does not say which shell it is.
func TestTheFrameworkExecuteIsNotMounted(t *testing.T) {
	cfg := deep.Config{}
	applyToolConfig(&cfg, heavyDeps{})
	if cfg.StreamingShell != nil {
		t.Fatal("the framework's execute tool must not be mounted")
	}
	if cfg.Shell != nil {
		t.Fatal("the framework's shell must not be mounted either")
	}
}

// ★ Exactly one execute tool reaches the model.
//
// ★ The two halves are separate facts: the framework mounts its execute
//
//	only from the config's shell fields (eino's filesystem middleware), and
//	those are nil — and the tool list carries ours once. A second one would
//	be a tool the model cannot tell apart from the first.
func TestOnlyOneExecuteToolIsMounted(t *testing.T) {
	cfg := configFromSpecs("test/model", []string{"execute"}, nil, nil)
	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	if len(bc.inputs) == 0 {
		t.Fatal("no agent was built")
	}

	root := bc.inputs[0]
	if root.StreamingShell != nil {
		t.Fatal("the framework's execute tool must not be mounted")
	}

	count := 0
	for _, name := range namesOf(root.ToolsConfig.Tools) {
		if name == "execute" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the agent must see exactly one execute tool, got %d in %v",
			count, namesOf(root.ToolsConfig.Tools))
	}
}

// The mounted execute tool carries the dialect in its description.
func TestTheMountedExecuteToolCarriesTheDialect(t *testing.T) {
	cfg := configFromSpecs("test/model", []string{"execute"}, nil, nil)
	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, tl := range bc.inputs[0].ToolsConfig.Tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "execute" {
			continue
		}
		found = true
		if !strings.Contains(info.Desc, "shell on this machine") {
			t.Fatalf("the execute tool must name the shell: %q", info.Desc)
		}
	}
	if !found {
		t.Fatal("the execute tool was not mounted")
	}
}

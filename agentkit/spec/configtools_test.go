package spec

import (
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// ★ The configuration tools have a file of their own, and it is what says
//
//	that the read-only ones run without a nod — the same rule every other
//
//	provider's tools follow.
func TestTheConfigToolsNameTheirOwnExceptions(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/config.yaml", "allows: ["+decl.ToolNameConfigGet+"]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, n := range cfg.AllowedTools() {
		allowed[n] = true
	}
	if !allowed[decl.ToolNameConfigGet] {
		t.Fatalf("%s must run without a nod — the file names it", decl.ToolNameConfigGet)
	}
	if allowed[decl.ToolNameConfigValidate] {
		t.Fatalf("%s must ask — the file does not name it", decl.ToolNameConfigValidate)
	}
}

// ★ Without the file, every configuration tool asks: there is no code list
//
//	that names them any more, and the file is the only thing that can.
func TestAnAbsentConfigFileLeavesTheConfigToolsAsking(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range cfg.AllowedTools() {
		if strings.HasPrefix(n, "config_") {
			t.Fatalf("%s must ask — the code does not name it any more", n)
		}
	}
}

// ★ The absence is worth a notice only when it changes something: an agent
//
//	that names none of the tools never sees them.
func TestTheAbsentConfigFileIsReportedWhenAnAgentUsesIt(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\ntools: ["+decl.ToolNameConfigGet+"]\n---\nbody\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, DirTools+"/config.yaml") && strings.Contains(d, decl.ToolNameConfigGet) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the file and the tools, got %v", cfg.Deferred())
	}
}

// ★ And an agent that uses none of them hears nothing: the notice is about
//
//	a cost somebody pays, and nobody is paying it.
func TestAnAgentThatUsesNoConfigToolIsNotToldAboutIt(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, DirTools+"/config.yaml") {
			t.Fatalf("an agent with no config tool must not be told about one: %v", d)
		}
	}
}

// ★ A file that asks for the one tool that rewrites the files saying so is
//
//	a file that did not take effect — so it is said, not swallowed.
func TestAConfigFileThatGrantsConfigWriteIsReported(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/config.yaml", "allows: ["+decl.ToolNameConfigWrite+"]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, decl.ToolNameConfigWrite) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the tool that cannot be granted, got %v", cfg.Deferred())
	}
}

// ★ A limit outside the range is a hard error, not a clamp: a limit that
//
//	silently becomes another number is a limit the user did not write.
func TestAConfigReadLimitOutsideTheRangeIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/config.yaml", "max_read_bytes: 10\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a limit outside the range must be a load-time error")
	}
	if !strings.Contains(err.Error(), "max_read_bytes") {
		t.Fatalf("the message must name the key, got %q", err)
	}
}

// ★ The limit the file carries is the one the tree holds.
func TestTheConfigReadLimitComesFromTheFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/config.yaml", "max_read_bytes: 4096\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Config == nil {
		t.Fatal("the file must be read into the configuration")
	}
	if got := cfg.Tools.Config.MaxReadBytes; got != 4096 {
		t.Fatalf("max_read_bytes = %d, want 4096", got)
	}
}

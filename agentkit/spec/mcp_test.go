package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/agentkit/templates"
)

// ── the partition ───────────────────────────────────────────────────────

func TestTheMCPFileNameIsTheServerName(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := cfg.MCPs["github"]
	if !ok {
		t.Fatalf("the server must be keyed by its name, got %v", cfg.MCPs)
	}
	if srv.Name != "github" {
		t.Fatalf("name = %q, want github", srv.Name)
	}
	if srv.Transport != decl.MCPTransportStdio {
		t.Fatalf("transport = %q, want stdio", srv.Transport)
	}
}

func TestTheServerNameMustBeReadableBack(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github__x.yaml", "transport: stdio\ncommand: npx\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a server name with a run of underscores must be refused")
	}
	if !strings.Contains(err.Error(), decl.MCPServerNamePattern) {
		t.Fatalf("the error must name the pattern, got %q", err)
	}
}

func TestAnUnknownTransportIsALoadTimeError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: sse\ncommand: npx\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("an unknown transport must be refused")
	}
	if !strings.Contains(err.Error(), "unknown transport") {
		t.Fatalf("the error must name the transport, got %q", err)
	}
}

func TestAStdioServerNeedsACommand(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a stdio server without a command must be refused")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("the error must name the missing field, got %q", err)
	}
}

func TestAnHTTPServerNeedsAURL(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/files.yaml", "transport: http\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("an http server without a url must be refused")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("the error must name the missing field, got %q", err)
	}
}

func TestAnHTTPServerMayNotDeclareAnEnvironment(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/files.yaml", "transport: http\nurl: https://mcp.example.com/mcp\nenv:\n  A: b\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("env on an http server must be refused")
	}
	if !strings.Contains(err.Error(), "env is not allowed") {
		t.Fatalf("the error must name the key, got %q", err)
	}
}

func TestAnEmptyEnvironmentOnAnHTTPServerIsStillRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/files.yaml", "transport: http\nurl: https://mcp.example.com/mcp\nenv: {}\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("env on an http server must be refused, even when it is empty")
	}
}

func TestAnUnknownKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\nenabled: true\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("an unknown key must be refused — the file defines the server, there is no enabled key")
	}
}

// ── the timeouts ────────────────────────────────────────────────────────

func TestTheTimeoutsHaveDefaults(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	srv := cfg.MCPs["github"]
	if srv.Timeout.Value != decl.DefaultMCPTimeout {
		t.Fatalf("timeout = %s, want %s", srv.Timeout.Value, decl.DefaultMCPTimeout)
	}
	if srv.CallTimeout.Value != decl.DefaultMCPCallTimeout {
		t.Fatalf("call_timeout = %s, want %s", srv.CallTimeout.Value, decl.DefaultMCPCallTimeout)
	}
}

func TestABadTimeoutDegradesAndIsNoticed(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\ntimeout: soon\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a wrong timeout must not keep the server from starting: %v", err)
	}
	if cfg.MCPs["github"].Timeout.Value != decl.DefaultMCPTimeout {
		t.Fatalf("timeout = %s, want the default", cfg.MCPs["github"].Timeout.Value)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "mcps/github.yaml") && strings.Contains(d, "timeout") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the file and the key, got %v", cfg.Deferred())
	}
}

// ── the environment ─────────────────────────────────────────────────────

func TestAnUnsetVariableInTheEnvironmentIsDeferredAndNotFatal(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  OPENHERON_TEST_ABSENT: ${OPENHERON_TEST_ABSENT}\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("an unset variable must not be fatal: %v", err)
	}
	if _, ok := cfg.MCPs["github"].Env["OPENHERON_TEST_ABSENT"]; ok {
		t.Fatal("the entry must be left out")
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "OPENHERON_TEST_ABSENT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the variable, got %v", cfg.Deferred())
	}
}

func TestASetVariableInTheEnvironmentIsExpanded(t *testing.T) {
	t.Setenv("OPENHERON_TEST_TOKEN", "secret")
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  TOKEN: ${OPENHERON_TEST_TOKEN}\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPs["github"].Env["TOKEN"]; got != "secret" {
		t.Fatalf("TOKEN = %q, want the expanded value", got)
	}
}

// ── the agent's list ────────────────────────────────────────────────────

func TestAnAgentNamingAServerThatDoesNotExistIsALoadTimeError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nmcps: [githbu]\n---\n\n你是助手。\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a typo in the agent's list must be refused")
	}
	if !strings.Contains(err.Error(), "githbu") {
		t.Fatalf("the error must name the server, got %q", err)
	}
}

func TestAnAbsentServerListMeansEveryConfiguredServer(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	inUse := cfg.MCPServersInUse()
	if len(inUse) != 1 || inUse[0] != "github" {
		t.Fatalf("servers in use = %v, want [github]", inUse)
	}
}

func TestAnEmptyServerListMeansNoServerAtAll(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nmcps: []\n---\n\n你是助手。\n")
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if inUse := cfg.MCPServersInUse(); len(inUse) != 0 {
		t.Fatalf("servers in use = %v, want none", inUse)
	}
}

func TestASubAgentsOwnListIsReadFromTheSubAgent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [task]\n---\n\n你是助手。\n")
	f.write(t, "agents/task.md", "---\nmodel: openai/fast\nmcps: [github]\n---\n\n你是助手。\n")
	f.write(t, "mcps/github.yaml", "transport: stdio\ncommand: npx\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	root := specs["main"]
	if root.MCPServers != nil {
		t.Fatalf("the root's list must stay absent, got %v", *root.MCPServers)
	}
	sub := specs["main"].Sub[0]
	if sub.MCPServers == nil || len(*sub.MCPServers) != 1 || (*sub.MCPServers)[0] != "github" {
		t.Fatalf("the sub-agent's own list must be carried, got %v", sub.MCPServers)
	}
}

// ── the disabled_tools patterns ─────────────────────────────────────────

func TestAWildcardThatIsNotAPrefixPatternIsALoadTimeError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md",
		"---\nmodel: openai/fast\ndisabled_tools: [\"mcp*foo\"]\n---\n\n你是助手。\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a star in the middle must be refused")
	}
	if !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("the error must say what is wrong, got %q", err)
	}
}

func TestALoneWildcardIsALoadTimeError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md",
		"---\nmodel: openai/fast\ndisabled_tools: [\"*\"]\n---\n\n你是助手。\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a lone wildcard must be refused")
	}
	if !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("the error must say what is wrong, got %q", err)
	}
}

func TestATrailingStarIsAPrefixPattern(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md",
		"---\nmodel: openai/fast\ndisabled_tools: [\"mcp__github__*\"]\n---\n\n你是助手。\n")

	if _, err := f.load(t); err != nil {
		t.Fatalf("a trailing star must be accepted: %v", err)
	}
}

// ── the editor ──────────────────────────────────────────────────────────

func TestTheEditorAcceptsAFileUnderMCPS(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	svc := f.service(t)

	if _, _, err := svc.Write("mcps/github.yaml",
		[]byte("transport: stdio\ncommand: npx\n"), ""); err != nil {
		t.Fatalf("the editor must be able to write under mcps/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "mcps", "github.yaml")); err != nil {
		t.Fatalf("the file must be there: %v", err)
	}
}

func TestTheEditorRefusesAServerNameThatCouldNotBeReadBack(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	svc := f.service(t)

	_, _, err := svc.Write("mcps/github__x.yaml", []byte("transport: stdio\ncommand: npx\n"), "")
	if err == nil {
		t.Fatal("the editor must refuse a name that could not be read back")
	}
}

// ★ The MCP example has to **load**: a file under mcps/ defines a server,
//
//	so a file that cannot be parsed is not a server that is off — it is a
//	configuration that does not load.
//
// ★ The test lives here and not in the templates package: the templates
//
//	package is a leaf, and a leaf's test may not reach up into the layer
//	that depends on it.
func TestTheMCPExampleLoads(t *testing.T) {
	content, err := templates.File("mcps/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/example.yaml", content)

	svc, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("the example must load: %v", err)
	}
	srv, ok := cfg.MCPs["example"]
	if !ok {
		t.Fatalf("the file's name is the server's name: %v", cfg.MCPs)
	}
	if srv.Transport != decl.MCPTransportStdio {
		t.Fatalf("transport = %q, want stdio", srv.Transport)
	}
}

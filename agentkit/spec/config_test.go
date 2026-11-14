package spec

import (
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type fixture struct {
	root string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{root: filepath.Join(root, ".openheron")}
	for _, sub := range []string{DirLLM, DirAgent, DirServer, DirTools, DirMCP} {
		if err := os.MkdirAll(filepath.Join(f.root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_LLM_KEY", "sk-test")
	return f
}

func (f *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) load(t *testing.T) (*Config, error) {
	t.Helper()
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	return s.Load()
}

const minimalLLM = `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
  - id: strong
`

const minimalAgent = `
---
model: openai/fast
---
你是助手。
`

func (f *fixture) seedMinimal(t *testing.T) {
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/main.md", minimalAgent)
}

func TestMissingConfigDirectoryIsNotAnError(t *testing.T) {
	root := t.TempDir()
	s, err := NewService(filepath.Join(root, "nope"), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("a missing agents/main.md must be an error")
	} else if !strings.Contains(err.Error(), "main.md") {
		t.Fatalf("the error must name the missing file, got %q", err)
	}
}

// ★ server/config.yaml's log_requests is the project-level twin of
//
//	OPENHERON_LOG_LLM_REQUESTS. It has to reach the loader, and a file that
//	omits it has to stay off — dumping megabyte bodies must be opt-in.
func TestTheRequestDumpSwitchComesFromTheConfig(t *testing.T) {
	t.Run("declared true", func(t *testing.T) {
		f := newFixture(t)
		f.seedMinimal(t)
		f.write(t, "server/config.yaml", "listen_addr: \":9999\"\nlog_requests: true\n")
		cfg, err := f.load(t)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Server.LogRequests {
			t.Fatal("log_requests: true did not reach Server.LogRequests")
		}
	})
	t.Run("omitted stays off", func(t *testing.T) {
		f := newFixture(t)
		f.seedMinimal(t)
		f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")
		cfg, err := f.load(t)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server.LogRequests {
			t.Fatal("an omitted log_requests must default to false")
		}
	})
}

// ★ A switch nobody can find is a switch nobody uses: the scaffold a new
//
//	project gets has to name the key, not leave it to the docs.
func TestTheServerTemplateNamesTheRequestDumpSwitch(t *testing.T) {
	for _, text := range Templates(DirServer) {
		if strings.Contains(text, "log_requests:") {
			return
		}
	}
	t.Fatal("the server/config.yaml template must name the log_requests key")
}

// ★ The scaffold a new project gets has to **land the log on disk**: a
//
//	server started from a terminal loses its history when the terminal
//	closes, and the log is the only record of what the agent did.
//
// ★ The check is for an **active** block. The commented-out form names the
//
//	same path, so a "contains" test would pass on it — the key has to
//	start a line of its own.
//
// ★ The standard error stays quiet: the file is the whole log, so a
//
//	server that runs in the background does not scroll a terminal.
func TestTheServerTemplateEnablesTheLogFile(t *testing.T) {
	for _, text := range Templates(DirServer) {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if line != "log:" {
				continue
			}
			var hasFile, quietStderr bool
			for _, next := range lines[i+1:] {
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				if !strings.HasPrefix(next, " ") {
					break
				}
				field := strings.TrimSpace(next)
				if idx := strings.Index(field, "#"); idx >= 0 {
					field = strings.TrimSpace(field[:idx])
				}
				switch field {
				case "file: logs/openheron.log":
					hasFile = true
				case "console: false":
					quietStderr = true
				}
			}
			if hasFile && quietStderr {
				return
			}
		}
	}
	t.Fatal("the server/config.yaml template must carry an active log block that names a file and keeps stderr quiet")
}

func TestDefaultsSurviveWhenFileOmitsKeys(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ListenAddr != ":9999" {
		t.Fatalf("listen_addr was not overridden: %q", cfg.Server.ListenAddr)
	}
	if cfg.Server.Retry.MaxRetries != 5 {
		t.Fatalf("an undeclared max_retries must stay at the default 5, got %d", cfg.Server.Retry.MaxRetries)
	}
	if cfg.Server.Watch.Debounce.Duration().Milliseconds() != 200 {
		t.Fatalf("an undeclared debounce must stay at the default 200ms, got %v", cfg.Server.Watch.Debounce)
	}
	if cfg.Server.LeaseTTL.Duration().Minutes() != 10 {
		t.Fatalf("an undeclared lease_ttl must stay at the default 10m, got %v", cfg.Server.LeaseTTL)
	}
}

// The lease's budget is a configuration item: it has to be longer than the
// provider's own timeout, and the two live in different files.
func TestTheLeaseTTLComesFromTheConfig(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\nlease_ttl: 30m\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.LeaseTTL.Duration().Minutes() != 30 {
		t.Fatalf("lease_ttl = %v, want 30m", cfg.Server.LeaseTTL)
	}
}

func TestTheOnlyFileInTheRootIsTheOneThatCounts(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/only\n---\n")
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://only.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: only
`)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.Models); got != 1 {
		t.Fatalf("a same-named file must replace wholesale, got %d models: %v", got, keysOf(cfg.Models))
	}
	if _, ok := cfg.Models["openai/only"]; !ok {
		t.Fatalf("the declared models did not take effect: %v", keysOf(cfg.Models))
	}
	if cfg.Providers["openai"].BaseURL != "https://only.example.com" {
		t.Fatalf("the base_url did not take effect: %q", cfg.Providers["openai"].BaseURL)
	}
}

// ★ The exceptions come from the file that provides the tool.
func TestTheExceptionsComeFromTheToolsOwnFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\nallows: [request_get]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, n := range cfg.AllowedTools() {
		allowed[n] = true
	}
	if !allowed["request_get"] {
		t.Fatal("request_get must run without a nod — the file names it")
	}
	if allowed["request_post"] {
		t.Fatal("request_post must ask — the file does not name it")
	}
}

func TestExpandEnvUnsetIsNonFatal(t *testing.T) {
	os.Unsetenv("OPENHERON_TEST_ABSENT")
	got, resolved, err := expandValue("${OPENHERON_TEST_ABSENT}")
	if err != nil {
		t.Fatalf("an unset env var must not be a load error (it only fails at request time): %v", err)
	}
	if resolved || got != "" {
		t.Fatalf("unset must yield an empty value with resolved=false, got %q / %v", got, resolved)
	}
}

func TestExpandEnvSetResolves(t *testing.T) {
	t.Setenv("OPENHERON_TEST_PRESENT", "  sk-ok  ")
	got, resolved, err := expandValue("${OPENHERON_TEST_PRESENT}")
	if err != nil || !resolved || got != "sk-ok" {
		t.Fatalf("set must yield the trimmed value with resolved=true: %q / %v / %v", got, resolved, err)
	}
}

func TestExpandAtFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "key")
	if err := os.WriteFile(p, []byte("  sk-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, resolved, err := expandValue("@file:" + p)
	if err != nil || !resolved {
		t.Fatal(err)
	}
	if got != "sk-from-file" {
		t.Fatalf("must be read and trimmed, got %q", got)
	}
	if _, _, err := expandValue("@file:" + filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing key file must be an error (different from a missing env var)")
	}
}

func TestExpandLiteralPassesThrough(t *testing.T) {
	got, resolved, err := expandValue("  sk-literal  ")
	if err != nil || !resolved || got != "sk-literal" {
		t.Fatalf("a literal must pass through trimmed, got %q / %v / %v", got, resolved, err)
	}
}

// ── decl.Duration ─────────────────────────────────────────────────────────────

func TestDurationRequiresUnit(t *testing.T) {
	var d decl.Duration
	if err := d.UnmarshalYAML(&yamlNode{Kind: yaml.ScalarNode, Value: "60", Tag: "!!int"}); err == nil {
		t.Fatal("★ a duration without a unit must be an error (otherwise 60 would be read as 60 nanoseconds)")
	}
	if err := d.UnmarshalYAML(&yamlNode{Kind: yaml.ScalarNode, Value: "60s", Tag: "!!str"}); err != nil {
		t.Fatal(err)
	}
	if d.Duration().Seconds() != 60 {
		t.Fatalf("60s did not parse: %v", d)
	}
}

func TestTimeoutMissingUnitIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: ${TEST_LLM_KEY}\ntimeout: 300\nmodels: [{id: fast}]\n")
	if _, err := f.load(t); err == nil {
		t.Fatal("a timeout without a unit must be an error at load time")
	}
}

func TestUnknownFieldIsRejectedWithLineNumber(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: ${TEST_LLM_KEY}\nmax_retry: 3\nmodels: [{id: fast}]\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("an unknown field must be an error")
	}
	if !strings.Contains(err.Error(), "max_retry") {
		t.Fatalf("the error must name the unknown field: %v", err)
	}
}

func TestProviderMissingKeyIsReminderNotFatal(t *testing.T) {
	f := newFixture(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\n---\n")
	f.write(t, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: ${OPENHERON_TEST_NOPE}\nmodels: [{id: fast}]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a missing api_key must not block startup: %v", err)
	}
	if cfg.Providers["openai"].APIKey != "" {
		t.Fatalf("an unresolved api_key must be empty, got %q", cfg.Providers["openai"].APIKey)
	}
	var found bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "OPENHERON_TEST_NOPE") && strings.Contains(d, "401 Unauthorized") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a notice naming the variable and its consequence must be recorded, got %v", cfg.Deferred())
	}
}

func TestAgentWithoutModelIsRejected(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/main.md", "---\ndescription: 主\n---\n正文\n")
	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("* a missing model must be rejected (a sub-agent silently inheriting the root model is a cost incident): %v", err)
	}
}

func TestModelRefWithoutProviderPrefixIsRejected(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/main.md", "---\nmodel: fast\n---\n")
	if _, err := f.load(t); err == nil {
		t.Fatal("a reference without a provider prefix must be an error at startup")
	}
}

func TestUnknownModelRefDoesNotFallBack(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/nonexistent\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("★ an undeclared model must not take the tree down: %v", err)
	}
	// ★ And it must not **fall back** either: the agent is left out, not
	//   quietly pointed at some other model.
	if _, ok := cfg.Agents["main"]; ok {
		t.Fatal("★ an agent whose model is not declared must be left out, not given another model")
	}
	var said bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "nonexistent") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the notice must name the model: %v", cfg.Deferred())
	}
}

func TestSubagentGraphCycleIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/a.md", "---\ndescription: A\nmodel: openai/fast\nsubagents: [b]\n---\n")
	f.write(t, "agents/b.md", "---\ndescription: B\nmodel: openai/fast\nsubagents: [a]\n---\n")
	f.write(t, "agents/main.md", "---\ndescription: 主\nmodel: openai/fast\nsubagents: [a]\n---\n")

	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a subagents cycle must be rejected: %v", err)
	}
}

func TestUnknownSubagentIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\ndescription: 主\nmodel: openai/fast\nsubagents: [ghost]\n---\n")
	if _, err := f.load(t); err == nil {
		t.Fatal("a reference to a nonexistent subagent must be an error")
	}
}

// ── frontmatter ──────────────────────────────────────────────────────────

func TestFrontmatterAndBodySplit(t *testing.T) {
	fm, body, _, err := splitFrontmatter([]byte("---\nmodel: a/b\ndescription: x\n---\n你是助手。\n\n第二段。\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fm), "model: a/b") {
		t.Fatalf("frontmatter was split wrong: %q", fm)
	}
	if !strings.Contains(body, "你是助手。") || !strings.Contains(body, "第二段。") {
		t.Fatalf("the body must be preserved verbatim (including paragraphs after a blank line): %q", body)
	}
}

func TestFrontmatterUnterminatedIsError(t *testing.T) {
	if _, _, _, err := splitFrontmatter([]byte("---\nmodel: a/b\n")); err == nil {
		t.Fatal("a missing closing delimiter must be an error")
	}
}

func TestAgentBodyBecomesInstruction(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents["main"].Instruction; !strings.Contains(got, "你是助手") {
		t.Fatalf("the body must become Instruction: %q", got)
	}
}

// ── deferred ─────────────────────────────────────────────────────────────

func TestDeferredIsCollected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/openai.yaml",
		strings.Replace(minimalLLM, "${TEST_LLM_KEY}", "${OPENHERON_TEST_NEVER_SET_KEY}", 1))

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Deferred()) == 0 {
		t.Fatal("★ a known but unimplemented key must land in deferred (otherwise it is silently ignored)")
	}
}

// ── a retired partition ──────────────────────────────────────────────────

// ★ The `policy/` partition is gone. A configuration that still carries it
//
//	loads — the rest of it may be perfectly good — but the directory is
//	**not read**, and a user who believes their tiers still apply has to
//	be told.
func TestALeftOverPolicyDirectoryIsReported(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "ask: [write_file]\ndeny: [execute]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "policy/") {
			found = true
			// ★ The notice has to say where the tiers went, or it is only
			//   half a message.
			if !strings.Contains(d, "server/config.yaml") {
				t.Errorf("the notice must name the new home, got %q", d)
			}
		}
	}
	if !found {
		t.Fatalf("★ a dropped partition must leave a word, got %v", cfg.Deferred())
	}
}

// ★ An empty directory is not a configuration: it carries nothing that was
//
//	dropped, so it must not raise a notice the user cannot act on.
func TestAnEmptyRetiredDirectoryIsNotReported(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	if err := os.MkdirAll(filepath.Join(f.root, "policy"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "policy/") {
			t.Fatalf("an empty directory carries nothing that was dropped, got %q", d)
		}
	}
}

// ── the caps ─────────────────────────────────────────────────────────────
//
// ★ The cap lives in server/config.yaml: one place, one value. It is not
//
//	hot-reloaded — a change takes effect on the next start — and that is
//	the price of not having a second copy of the same number.
func capsGlobal(t *testing.T, f *fixture, depth, retries int) {
	t.Helper()
	f.write(t, "server/config.yaml",
		"agent:\n  max_depth: "+itoa(depth)+"\nretry:\n  max_retries: "+itoa(retries)+"\n")
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestTheCapsComeFromTheServerConfiguration(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	capsGlobal(t, f, 3, 4)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MaxDepth(); got != 3 {
		t.Fatalf("MaxDepth = %d, want 3", got)
	}
	if got := cfg.MaxRetries(); got != 4 {
		t.Fatalf("MaxRetries = %d, want 4", got)
	}
}

func TestTheCapsFallBackToTheDefaults(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 0\nretry:\n  max_retries: 0\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MaxDepth(); got != DefaultMaxDepth {
		t.Fatalf("MaxDepth = %d, want the default %d (0 = the default)", got, DefaultMaxDepth)
	}
	if got := cfg.MaxRetries(); got != DefaultMaxRetries {
		t.Fatalf("MaxRetries = %d, want the default %d (0 = the default)", got, DefaultMaxRetries)
	}
}

func TestTheCapsRejectUnknownKeys(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_tokens: 100\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("an unknown key must be an error (strict parsing), not silently ignored")
	}
}

func TestADepthAboveTheCapFailsValidation(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\nsubagents: [grandchild]\n---\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")
	capsGlobal(t, f, 1, 5)

	if _, err := f.load(t); err == nil {
		t.Fatal("a declared depth above the cap must be an error")
	}
}

func TestRelativePathsResolveAgainstProjectDir(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	a := s.resolvePath("state")
	b := filepath.Join(f.root, "state")
	if a != b {
		t.Fatalf("a relative path must resolve against project_dir: %q != %q", a, b)
	}
	// ★ The absolute path is derived from the fixture, not hard-coded: "/x"
	//   is absolute on Unix and not on Windows, where it needs a drive.
	want := filepath.Join(f.root, "var", "lib", "openheron")
	if abs := s.resolvePath(want); abs != want {
		t.Fatalf("an absolute path must be returned as-is: %q", abs)
	}
}

func TestProjectRootAndResolveDir(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(f.root)
	if s.ProjectRoot() != root {
		t.Fatalf("ProjectRoot must be the parent of .openheron, want %q, got %q", root, s.ProjectRoot())
	}
	if s.Root() == s.ProjectRoot() {
		t.Fatal("Root (config root) and ProjectRoot (project root) must differ")
	}
	if got := s.ResolveDir("static"); got != filepath.Join(root, "static") {
		t.Fatalf("a relative path must resolve against the project root: %q != %q", got, filepath.Join(root, "static"))
	}
	abs := filepath.Join(root, "abs", "static")
	if got := s.ResolveDir(abs); got != abs {
		t.Fatalf("an absolute path must be returned as-is: %q", got)
	}
	if got := s.ResolveDir("  "); got != "" {
		t.Fatalf("an empty string must be returned as-is (callers use that to disable): %q", got)
	}
}

func TestDataDirResolvesAgainstConfigDir(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	def, err := s.DataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if def != filepath.Join(f.root, DirState) {
		t.Fatalf("the default data dir must be <projectDir>/state, got %q", def)
	}
	if fi, err := os.Stat(def); err != nil || !fi.IsDir() {
		t.Fatalf("DataDir must create the directory: %v", err)
	}

	for _, rel := range []string{DirState, "var/data"} {
		got, err := s.DataDir(rel)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(f.root, rel); got != want {
			t.Fatalf("a relative data_dir must resolve against the config dir: %q != %q", got, want)
		}
	}

	abs := filepath.Join(t.TempDir(), "elsewhere")
	if got, err := s.DataDir(abs); err != nil || got != abs {
		t.Fatalf("an absolute data_dir must be returned as-is: %q (%v)", got, err)
	}
}

// ★ The log lives with the runtime state, not with the project: `data_dir`,
//
//	`logs/` and the partitions are all inside .openheron/, while static_dir is
//	the one path that points into the tree.
func TestLogFileResolvesAgainstConfigRoot(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.LogFile(""); err != nil || got != "" {
		t.Fatalf("an empty log file means no file, not an error: %q (%v)", got, err)
	}

	got, err := s.LogFile("logs/openheron.log")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.root, "logs", "openheron.log"); got != want {
		t.Fatalf("a relative log file must resolve against the config root: %q != %q", got, want)
	}
	if fi, err := os.Stat(filepath.Dir(got)); err != nil || !fi.IsDir() {
		t.Fatalf("LogFile must create the directory it names: %v", err)
	}

	abs := filepath.Join(t.TempDir(), "openheron.log")
	if got, err := s.LogFile(abs); err != nil || got != abs {
		t.Fatalf("an absolute log file must be returned as-is: %q (%v)", got, err)
	}
}

func TestRootDefaultsToTheProjectRootDir(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(ConfigDirEnv, "")
	s, err := NewService("", deep)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(deep, DefaultBaseDirName)
	if s.Root() != want {
		t.Fatalf("the config root must be the project root's own directory, got %q (want %q)", s.Root(), want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("the missing config root must be created at %q", want)
	}
}

func TestTheConfigRootIsNeverSearchedForUpwards(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor", DefaultBaseDirName)
	if err := os.MkdirAll(ancestor, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "ancestor", "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(ConfigDirEnv, "")
	s, err := NewService("", child)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() == ancestor {
		t.Fatalf("the ancestor's config root must not be picked up, got %q", s.Root())
	}
	if want := filepath.Join(child, DefaultBaseDirName); s.Root() != want {
		t.Fatalf("the config root must be the project root's own directory, got %q (want %q)", s.Root(), want)
	}
}

func TestAnExplicitRootWinsOverTheEnvironment(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv(ConfigDirEnv, filepath.Join(t.TempDir(), "from-env"))

	s, err := NewService(explicit, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != explicit {
		t.Fatalf("the explicit root must win, got %q (want %q)", s.Root(), explicit)
	}
	if s.RootIsDefaulted() {
		t.Fatal("an explicit root must not be reported as defaulted")
	}
}

func TestTheEnvironmentWinsOverTheProjectRootDir(t *testing.T) {
	fromEnv := filepath.Join(t.TempDir(), "from-env")
	t.Setenv(ConfigDirEnv, fromEnv)

	projectRoot := t.TempDir()
	s, err := NewService("", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != fromEnv {
		t.Fatalf("the environment must win, got %q (want %q)", s.Root(), fromEnv)
	}
	if s.RootIsDefaulted() {
		t.Fatal("a root from the environment must not be reported as defaulted")
	}
	if _, err := os.Stat(fromEnv); err == nil {
		t.Fatal("a root named by the environment must not be created")
	}
}

func TestAnAbsentExplicitRootIsNotCreated(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "nope")
	t.Setenv(ConfigDirEnv, "")

	s, err := NewService(explicit, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(explicit); err == nil {
		t.Fatalf("an explicit root must not be created: %q", explicit)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("a missing agents/main.md must be an error")
	}
}

func keysOf(m map[string]*decl.Model) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSubagentGraphIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: 只读检索\nmodel: openai/fast\ntools: [ls, read_file, glob, grep]\n---\n只读。\n")
	f.write(t, "agents/main.md",
		"---\nmodel: openai/strong\nsubagents: [researcher]\n---\n主。\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a valid sub-agent declaration must not be rejected: %v", err)
	}
	r := cfg.Agents["researcher"]
	if r == nil || len(r.Tools) != 4 {
		t.Fatalf("the sub-agent declaration was not loaded: %+v", r)
	}
}

func TestDeclaredDepthOverLimitIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\nsubagents: [grandchild]\n---\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("★ exceeding the depth limit must be an error at startup")
	}
	if !strings.Contains(err.Error(), "max_depth") {
		t.Fatalf("the error must point at the tunable knob: %v", err)
	}
}

func TestRaisingMaxDepthAcceptsDeeperGraph(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\nsubagents: [grandchild]\n---\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")

	if _, err := f.load(t); err != nil {
		t.Fatalf("it must load after raising max_depth: %v", err)
	}
}

func TestAgentNobodyReferencesIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/orphan.md", "---\ndescription: 孤儿\nmodel: openai/fast\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("an agent nobody references must be accepted: %v", err)
	}
	if _, ok := cfg.Agents["orphan"]; !ok {
		t.Fatal("the orphan agent must be declared")
	}
}

func TestSubAgentSpecsMirrorTopology(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\ntools: [ls, read_file]\nreasoning: low\ntemperature: 0.3\nmax_tokens: 128\nsubagents: [grandchild]\n---\nR 正文\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\nG 正文\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	all, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	specs := all[cfg.DefaultAgent()].Sub
	if len(specs) != 1 || specs[0].ID != "researcher" {
		t.Fatalf("the top level of the spec tree is wrong: %+v", specs)
	}
	if len(specs[0].Tools) != 2 || specs[0].Tools[0] != "ls" {
		t.Fatalf("tools was not carried over: %+v", specs[0].Tools)
	}
	if specs[0].Reasoning != "low" {
		t.Fatalf("the reasoning gear name was not carried over: %q", specs[0].Reasoning)
	}
	if specs[0].Temperature == nil || *specs[0].Temperature != 0.3 {
		t.Fatalf("temperature was not carried over: %v", specs[0].Temperature)
	}
	if specs[0].MaxTokens == nil || *specs[0].MaxTokens != 128 {
		t.Fatalf("max_tokens was not carried over: %v", specs[0].MaxTokens)
	}
	if len(specs[0].Sub) != 1 || specs[0].Sub[0].ID != "grandchild" {
		t.Fatalf("the nested spec is wrong: %+v", specs[0].Sub)
	}
}

func TestEveryDeclaredAgentCanBeTheSessionAgent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")
	f.write(t, "agents/worker.md",
		"---\ndescription: W\nmodel: openai/fast\nsubagents: [leaf]\n---\n")
	f.write(t, "agents/leaf.md", "---\ndescription: L\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [worker]\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "worker", "leaf"} {
		if _, ok := specs[id]; !ok {
			t.Fatalf("%q must have a spec of its own, got %v", id, cfg.AgentNames())
		}
	}
	if got := specs["leaf"].Sub; len(got) != 0 {
		t.Fatalf("leaf's tree = %+v, want none", got)
	}
}

func TestModelReasoningVocabularyAndDefault(t *testing.T) {
	f := newFixture(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\n---\n")
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
    reasoning:
      off:  {}
      low:  { reasoning_effort: low }
      high: { reasoning_effort: high }
    reasoning_default: low
`)
	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Models["openai/fast"]
	if got, ok := m.LevelFields("high"); !ok || got["reasoning_effort"] != "high" {
		t.Fatalf("the gear shape did not parse correctly: %#v ok=%v", got, ok)
	}
	if m.ReasoningDefault != "low" {
		t.Fatalf("reasoning_default did not parse: %q", m.ReasoningDefault)
	}
}

func TestLegacyThinkingAndEffortAreRejected(t *testing.T) {
	f := newFixture(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\n---\n")
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
    thinking:
      high: { reasoning_effort: high }
    effort: high
`)
	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "thinking") {
		t.Fatalf("the legacy thinking field must be rejected in strict mode: %v", err)
	}
}

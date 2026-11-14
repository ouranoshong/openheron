package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// fakeSecretStore is an in-memory store.
//
// ★ It exists so that the tests never touch the **real** keyring: the
//
//	Windows Credential Manager, the macOS Keychain and the Linux Secret
//	Service are global OS resources, and a test that wrote to them would
//	leave entries in the developer's own credential store — under a name
//	nobody would remember to delete.
type fakeSecretStore struct {
	mu     sync.Mutex
	values map[string]string

	// unavailable says the store cannot be reached at all — a headless
	// machine with no session bus, say.
	unavailable bool
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[string]string{}}
}

func (f *fakeSecretStore) Get(user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return "", ErrSecretUnavailable
	}
	v, ok := f.values[user]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (f *fakeSecretStore) Set(user, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return ErrSecretUnavailable
	}
	f.values[user] = value
	return nil
}

func (f *fakeSecretStore) Delete(user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return ErrSecretUnavailable
	}
	if _, ok := f.values[user]; !ok {
		return ErrSecretNotFound
	}
	delete(f.values, user)
	return nil
}

// ── the names ─────────────────────────────────────────────────────────────

func TestTheTwoNamespacesAreDerivedFromTheName(t *testing.T) {
	cases := []struct {
		provider string
		env      string
		user     string
	}{
		{"deepseek", "LLM_DEEPSEEK_API_KEY", "llm:deepseek"},
		{"openai", "LLM_OPENAI_API_KEY", "llm:openai"},
		{"my_provider", "LLM_MY_PROVIDER_API_KEY", "llm:my_provider"},
		// ★ An id that ends with the suffix's own letters: the name is
		//   stripped once, not twice.
		{"foo_api_key", "LLM_FOO_API_KEY_API_KEY", "llm:foo_api_key"},
	}
	for _, c := range cases {
		if got := LLMEnvName(c.provider); got != c.env {
			t.Errorf("LLMEnvName(%q) = %q, want %q", c.provider, got, c.env)
		}
		if got := LLMSecretUser(c.provider); got != c.user {
			t.Errorf("LLMSecretUser(%q) = %q, want %q", c.provider, got, c.user)
		}
		if got := MCPEnvName(c.provider); got != "MCP_"+strings.ToUpper(c.provider)+"_TOKEN" {
			t.Errorf("MCPEnvName(%q) = %q", c.provider, got)
		}
	}
}

func TestANameMapsBackToItsKeyringUser(t *testing.T) {
	cases := []struct {
		name string
		user string
		ok   bool
	}{
		{"LLM_DEEPSEEK_API_KEY", "llm:deepseek", true},
		{"MCP_GITHUB_TOKEN", "mcp:github", true},
		{"LLM_MY_PROVIDER_API_KEY", "llm:my_provider", true},
		{"LLM_FOO_API_KEY_API_KEY", "llm:foo_api_key", true},
		// ★ A name that is not one of the two shapes belongs to no
		//   namespace, so there is nothing in the keyring it could point
		//   at.
		{"GITHUB_TOKEN", "", false},
		{"OPENAI_API_KEY", "", false},
		{"LLM__API_KEY", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		user, ok := SecretUserFor(c.name)
		if ok != c.ok || user != c.user {
			t.Errorf("SecretUserFor(%q) = %q, %v; want %q, %v", c.name, user, ok, c.user, c.ok)
		}
	}
}

// ── the chain ─────────────────────────────────────────────────────────────

func TestTheEnvironmentOverridesTheKeyring(t *testing.T) {
	store := newFakeSecretStore()
	if err := store.Set("llm:deepseek", "from-keyring"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_DEEPSEEK_API_KEY", "from-environment")

	sec, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if sec.Value != "from-environment" {
		t.Fatalf("value = %q, want the environment's", sec.Value)
	}
	if sec.Source != "env:LLM_DEEPSEEK_API_KEY" {
		t.Fatalf("source = %q", sec.Source)
	}
}

func TestTheKeyringIsTheFallback(t *testing.T) {
	store := newFakeSecretStore()
	if err := store.Set("llm:deepseek", "from-keyring"); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("LLM_DEEPSEEK_API_KEY")

	sec, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !sec.Found || sec.Value != "from-keyring" {
		t.Fatalf("value = %q, found = %v", sec.Value, sec.Found)
	}
	if sec.Source != "keyring:llm:deepseek" {
		t.Fatalf("source = %q", sec.Source)
	}
}

func TestANameWithNoKeyAnywhereIsNotResolved(t *testing.T) {
	store := newFakeSecretStore()
	os.Unsetenv("LLM_DEEPSEEK_API_KEY")

	sec, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if sec.Found {
		t.Fatalf("found = %v, want false", sec.Found)
	}
	if sec.Source != "none" {
		t.Fatalf("source = %q", sec.Source)
	}
}

func TestAnEmptyVariableIsItsOwnCase(t *testing.T) {
	store := newFakeSecretStore()
	t.Setenv("LLM_DEEPSEEK_API_KEY", "")

	sec, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !sec.Empty {
		t.Fatal("a variable that is set and holds nothing must be reported as empty")
	}
	if sec.Found {
		t.Fatal("an empty variable holds no key")
	}
}

// ★ The empty case is the whole point of the change: the old loader could
//
//	not tell "set but empty" from "not set", because the field carried the
//	reference and the reference carried nothing.
func TestAnEmptyVariableAndAMissingOneSayDifferentThings(t *testing.T) {
	store := newFakeSecretStore()
	t.Setenv("LLM_DEEPSEEK_API_KEY", "")
	empty := secretNotice("LLM_DEEPSEEK_API_KEY", mustResolve(t, store, "LLM_DEEPSEEK_API_KEY"))

	os.Unsetenv("LLM_DEEPSEEK_API_KEY")
	missing := secretNotice("LLM_DEEPSEEK_API_KEY", mustResolve(t, store, "LLM_DEEPSEEK_API_KEY"))

	if empty == missing {
		t.Fatalf("the two cases say the same thing: %q", empty)
	}
	if !strings.Contains(empty, "empty") {
		t.Fatalf("the empty case must say so: %q", empty)
	}
	if !strings.Contains(missing, "not set") {
		t.Fatalf("the missing case must say so: %q", missing)
	}
}

func mustResolve(t *testing.T, store SecretStore, name string) Secret {
	t.Helper()
	sec, err := ResolveSecretFrom(store, name)
	if err != nil {
		t.Fatal(err)
	}
	return sec
}

func TestAnUnavailableKeyringIsNotAMissingKey(t *testing.T) {
	store := newFakeSecretStore()
	store.unavailable = true
	os.Unsetenv("LLM_DEEPSEEK_API_KEY")

	sec, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY")
	// ★ The failure is **soft**: a machine without a keyring is a fact
	//   about the machine, not a broken configuration. The tree still loads.
	if err != nil {
		t.Fatalf("an unreachable store must not fail the resolution: %v", err)
	}
	if !sec.Unavailable {
		t.Fatal("the resolution must say the store was unreachable")
	}
	if sec.Found {
		t.Fatal("an unreachable store holds no key")
	}
}

func TestAValueMayPointAtAFile(t *testing.T) {
	store := newFakeSecretStore()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("  sk-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_DEEPSEEK_API_KEY", "@file:"+path)

	sec, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if sec.Value != "sk-from-file" {
		t.Fatalf("value = %q, want the file's content, trimmed", sec.Value)
	}
	if !strings.HasPrefix(sec.Source, "file:") {
		t.Fatalf("source = %q", sec.Source)
	}
}

func TestAFileThatCannotBeReadIsAHardError(t *testing.T) {
	store := newFakeSecretStore()
	t.Setenv("LLM_DEEPSEEK_API_KEY", "@file:"+filepath.Join(t.TempDir(), "nope"))

	if _, err := ResolveSecretFrom(store, "LLM_DEEPSEEK_API_KEY"); err == nil {
		t.Fatal("a path that does not resolve must be an error")
	}
}

// ── the status the interface shows ────────────────────────────────────────

func TestTheStatusSaysWhichKeyIsInForce(t *testing.T) {
	store := newFakeSecretStore()
	if err := store.Set("llm:deepseek", "from-keyring"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_DEEPSEEK_API_KEY", "from-environment")

	st := StatusFor(store, "LLM_DEEPSEEK_API_KEY")
	if !st.Stored {
		t.Fatal("the keyring holds it")
	}
	if st.StoredSource != "keyring:llm:deepseek" {
		t.Fatalf("stored source = %q", st.StoredSource)
	}
	if st.Effective != "env:LLM_DEEPSEEK_API_KEY" {
		t.Fatalf("effective = %q", st.Effective)
	}
	if !st.Shadowed {
		t.Fatal("the stored key is shadowed by the environment")
	}
}

func TestTheStatusOfAnEmptyKeyring(t *testing.T) {
	store := newFakeSecretStore()
	os.Unsetenv("LLM_DEEPSEEK_API_KEY")

	st := StatusFor(store, "LLM_DEEPSEEK_API_KEY")
	if st.Stored || st.Shadowed {
		t.Fatalf("stored = %v, shadowed = %v", st.Stored, st.Shadowed)
	}
	if st.Effective != "none" {
		t.Fatalf("effective = %q", st.Effective)
	}
}

// ── the loader ────────────────────────────────────────────────────────────

func TestTheProviderKeyComesFromTheConvention(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	t.Setenv("LLM_OPENAI_API_KEY", "sk-from-the-environment")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["openai"]
	if p == nil {
		t.Fatal("the provider is missing")
	}
	if p.APIKey != "sk-from-the-environment" {
		t.Fatalf("key = %q", p.APIKey)
	}
	if p.KeySource != "env:LLM_OPENAI_API_KEY" {
		t.Fatalf("source = %q", p.KeySource)
	}
}

func TestTheProviderKeyFallsBackToTheKeyring(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	os.Unsetenv("LLM_OPENAI_API_KEY")
	if err := f.secrets.Set("llm:openai", "sk-from-the-keyring"); err != nil {
		t.Fatal(err)
	}

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["openai"]
	if p.APIKey != "sk-from-the-keyring" {
		t.Fatalf("key = %q", p.APIKey)
	}
	if p.KeySource != "keyring:llm:openai" {
		t.Fatalf("source = %q", p.KeySource)
	}
}

func TestAMissingProviderKeyIsANoticeAndNotAFailure(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	os.Unsetenv("LLM_OPENAI_API_KEY")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a missing key must not block the startup: %v", err)
	}
	if cfg.Providers["openai"].APIKey != "" {
		t.Fatal("the key must be empty")
	}
	var found bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "LLM_OPENAI_API_KEY") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the variable: %v", cfg.Deferred())
	}
}

func TestAFileThatStillCarriesTheKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/openai.yaml", strings.Replace(minimalLLM, "base_url:", "api_key: ${OLD_KEY}\nbase_url:", 1))

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a file that still carries api_key must be refused")
	}
	if !strings.Contains(err.Error(), "LLM_OPENAI_API_KEY") {
		t.Fatalf("the refusal must say where the key comes from now: %v", err)
	}
}

func TestAProviderNameMustBeUsableAsAVariableName(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/my-provider.yaml", "type: openai_compat\nbase_url: https://x\nmodels: [{id: fast}]\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a provider name with a dash must be refused")
	}
	if !strings.Contains(err.Error(), decl.ProviderNamePattern) {
		t.Fatalf("the error must name the pattern: %v", err)
	}
}

// ── the MCP side ──────────────────────────────────────────────────────────

func TestTheMCPServerTokenComesFromTheKeyring(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  GITHUB_TOKEN: ${MCP_GITHUB_TOKEN}\n")
	if err := f.secrets.Set("mcp:github", "ghp-from-the-keyring"); err != nil {
		t.Fatal(err)
	}

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	srv := cfg.MCPs["github"]
	if srv == nil {
		t.Fatal("the server is missing")
	}
	if srv.Env["GITHUB_TOKEN"] != "ghp-from-the-keyring" {
		t.Fatalf("env = %v", srv.Env)
	}
}

func TestAMCPReferenceThatIsNotDerivedStaysAPlainLookup(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  GITHUB_TOKEN: ${GITHUB_TOKEN}\n")
	t.Setenv("GITHUB_TOKEN", "plain-token")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPs["github"].Env["GITHUB_TOKEN"]; got != "plain-token" {
		t.Fatalf("env = %q", got)
	}
}

func TestAMissingMCPServerTokenLeavesTheEntryOut(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "mcps/github.yaml",
		"transport: stdio\ncommand: npx\nenv:\n  GITHUB_TOKEN: ${MCP_GITHUB_TOKEN}\n")
	os.Unsetenv("MCP_GITHUB_TOKEN")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.MCPs["github"].Env["GITHUB_TOKEN"]; ok {
		t.Fatal("the entry must be left out")
	}
	var found bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "MCP_GITHUB_TOKEN") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the variable: %v", cfg.Deferred())
	}
}

// ── the store itself ──────────────────────────────────────────────────────

func TestTheStoreIsWhatTheServiceHandsOut(t *testing.T) {
	f := newFixture(t)
	s := f.service(t)
	if s.SecretStore() == nil {
		t.Fatal("the service must hand out a store")
	}
	if errors.Is(nil, ErrSecretNotFound) {
		t.Fatal("unreachable")
	}
}

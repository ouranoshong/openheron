package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

// fakeStore is an in-memory keyring.
//
// ★ The real one is the developer's own credential store — the Windows
//
//	Credential Manager, the macOS Keychain, the Linux Secret Service — and a
//	test that wrote to it would leave an entry nobody would remember to
//	delete.
type fakeStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: map[string]string{}}
}

func (f *fakeStore) Get(user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[user]
	if !ok {
		return "", spec.ErrSecretNotFound
	}
	return v, nil
}

func (f *fakeStore) Set(user, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[user] = value
	return nil
}

func (f *fakeStore) Delete(user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.values[user]; !ok {
		return spec.ErrSecretNotFound
	}
	delete(f.values, user)
	return nil
}

// secretCmdEnv runs the command against a fake store and returns its output.
func secretCmdEnv(t *testing.T, store spec.SecretStore, stdin string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := secretCmdWith(args, strings.NewReader(stdin), &buf, &buf, store)
	return buf.String(), err
}

// ── set ─────────────────────────────────────────────────────────────────────

func TestSecretSetStoresTheValueUnderTheDerivedName(t *testing.T) {
	store := newFakeStore()

	out, err := secretCmdEnv(t, store, "sk-the-key\n", "set", "llm", "deepseek")
	if err != nil {
		t.Fatalf("secret set: %v", err)
	}
	got, err := store.Get("llm:deepseek")
	if err != nil {
		t.Fatalf("the key is not under llm:deepseek: %v", err)
	}
	if got != "sk-the-key" {
		t.Fatalf("value = %q", got)
	}
	// ★ The answer says where the key is **in force** — the user has to
	//   know which of the two places won.
	if !strings.Contains(out, "stored llm:deepseek") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "in force: keyring:llm:deepseek") {
		t.Fatalf("output = %q", out)
	}
}

func TestSecretSetSaysThatTheEnvironmentWins(t *testing.T) {
	store := newFakeStore()
	t.Setenv("LLM_DEEPSEEK_API_KEY", "from-the-environment")

	out, err := secretCmdEnv(t, store, "sk-from-the-keyring\n", "set", "llm", "deepseek")
	if err != nil {
		t.Fatalf("secret set: %v", err)
	}
	if !strings.Contains(out, "in force: env:LLM_DEEPSEEK_API_KEY") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "wins") {
		t.Fatalf("the shadowing must be said out loud: %q", out)
	}
}

func TestSecretSetRefusesAnEmptyValue(t *testing.T) {
	store := newFakeStore()
	if _, err := secretCmdEnv(t, store, "  \n", "set", "llm", "deepseek"); err == nil {
		t.Fatal("an empty value must be refused")
	}
}

func TestSecretSetRefusesAnUnknownGroup(t *testing.T) {
	store := newFakeStore()
	if _, err := secretCmdEnv(t, store, "x", "set", "keys", "deepseek"); err == nil {
		t.Fatal("an unknown group must be refused")
	}
}

func TestSecretSetRefusesANameThatCannotBeAVariable(t *testing.T) {
	store := newFakeStore()
	if _, err := secretCmdEnv(t, store, "x", "set", "llm", "my-provider"); err == nil {
		t.Fatal("a name with a dash must be refused")
	}
}

// ── get ─────────────────────────────────────────────────────────────────────

func TestSecretGetPrintsTheStoredValue(t *testing.T) {
	store := newFakeStore()
	if err := store.Set("llm:deepseek", "sk-the-key"); err != nil {
		t.Fatal(err)
	}

	out, err := secretCmdEnv(t, store, "", "get", "llm", "deepseek")
	if err != nil {
		t.Fatalf("secret get: %v", err)
	}
	if strings.TrimSpace(out) != "sk-the-key" {
		t.Fatalf("output = %q", out)
	}
}

func TestSecretGetFailsWhenThereIsNoKey(t *testing.T) {
	store := newFakeStore()
	if _, err := secretCmdEnv(t, store, "", "get", "llm", "deepseek"); err == nil {
		t.Fatal("there is no key, so the command must fail")
	}
}

// ── list ────────────────────────────────────────────────────────────────────

func TestSecretListNamesBothGroupsAndTheSourceInForce(t *testing.T) {
	root := t.TempDir()
	cfgRoot := writeSecretConfig(t, root)
	store := newFakeStore()
	if err := store.Set("llm:deepseek", "sk-the-key"); err != nil {
		t.Fatal(err)
	}

	out, err := secretCmdEnv(t, store, "", "--config-dir", cfgRoot, "--project-root", root, "list")
	if err != nil {
		t.Fatalf("secret list: %v", err)
	}
	if !strings.Contains(out, "Providers") || !strings.Contains(out, "MCP servers") {
		t.Fatalf("both groups must be named: %q", out)
	}
	if !strings.Contains(out, "deepseek") {
		t.Fatalf("the provider must be listed: %q", out)
	}
	if !strings.Contains(out, "stored") {
		t.Fatalf("the state must be said: %q", out)
	}
	if !strings.Contains(out, "keyring:llm:deepseek") {
		t.Fatalf("the source in force must be said: %q", out)
	}
}

func TestSecretListNeverPrintsTheValue(t *testing.T) {
	root := t.TempDir()
	cfgRoot := writeSecretConfig(t, root)
	store := newFakeStore()
	if err := store.Set("llm:deepseek", "sk-never-printed"); err != nil {
		t.Fatal(err)
	}

	out, err := secretCmdEnv(t, store, "", "--config-dir", cfgRoot, "--project-root", root, "list")
	if err != nil {
		t.Fatalf("secret list: %v", err)
	}
	if strings.Contains(out, "sk-never-printed") {
		t.Fatalf("the value must never be printed: %q", out)
	}
}

// ── remove ──────────────────────────────────────────────────────────────────

func TestSecretRemoveDeletesOneEntry(t *testing.T) {
	store := newFakeStore()
	if err := store.Set("llm:deepseek", "a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("llm:openai", "b"); err != nil {
		t.Fatal(err)
	}

	if _, err := secretCmdEnv(t, store, "", "remove", "llm", "deepseek"); err != nil {
		t.Fatalf("secret remove: %v", err)
	}
	if _, err := store.Get("llm:deepseek"); !errors.Is(err, spec.ErrSecretNotFound) {
		t.Fatalf("the entry is still there: %v", err)
	}
	// ★ One entry only: the other one is untouched.
	if _, err := store.Get("llm:openai"); err != nil {
		t.Fatalf("the other entry was removed: %v", err)
	}
}

func TestSecretRemoveOfAGroupTouchesOnlyThatGroup(t *testing.T) {
	root := t.TempDir()
	cfgRoot := writeSecretConfig(t, root)
	store := newFakeStore()
	if err := store.Set("llm:deepseek", "a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("mcp:github", "b"); err != nil {
		t.Fatal(err)
	}

	if _, err := secretCmdEnv(t, store, "", "--config-dir", cfgRoot, "--project-root", root, "remove", "llm"); err != nil {
		t.Fatalf("secret remove: %v", err)
	}
	if _, err := store.Get("llm:deepseek"); !errors.Is(err, spec.ErrSecretNotFound) {
		t.Fatalf("the group's entry is still there: %v", err)
	}
	// ★ The other group is a different namespace, and the path says so.
	if _, err := store.Get("mcp:github"); err != nil {
		t.Fatalf("the other group was touched: %v", err)
	}
}

func TestSecretRemoveOfAnAbsentEntryIsNotAnError(t *testing.T) {
	store := newFakeStore()
	out, err := secretCmdEnv(t, store, "", "remove", "llm", "deepseek")
	if err != nil {
		t.Fatalf("removing what is not there must not fail: %v", err)
	}
	if !strings.Contains(out, "there was no") {
		t.Fatalf("a silent nothing must be visible: %q", out)
	}
}

// ── the usage ───────────────────────────────────────────────────────────────

func TestSecretWithoutAnActionExplainsItself(t *testing.T) {
	store := newFakeStore()
	_, err := secretCmdEnv(t, store, "")
	if err == nil {
		t.Fatal("no action must be an error")
	}
}

func TestSecretRejectsAnUnknownAction(t *testing.T) {
	store := newFakeStore()
	if _, err := secretCmdEnv(t, store, "", "show"); err == nil {
		t.Fatal("an unknown action must be refused")
	}
}

// writeSecretConfig writes a small tree with one provider and one server,
// and returns the config root.
func writeSecretConfig(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, ".openheron")
	for _, sub := range []string{"llm", "agents", "mcps"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("llm/deepseek.yaml", "type: openai_compat\nbase_url: https://api.deepseek.com\nmodels:\n  - id: fast\n")
	write("agents/main.md", "---\nmodel: deepseek/fast\n---\nbody\n")
	write("mcps/github.yaml", "transport: stdio\ncommand: npx\n")
	return dir
}

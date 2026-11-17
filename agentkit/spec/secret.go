package spec

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// The secret system: two namespaces, one resolution chain.
//
// ★ A secret is **named**, not placed. The name says which namespace it
//
//	belongs to — LLM_<PROVIDER>_API_KEY for a model key, MCP_<SERVER>_TOKEN
//	for a server's token — and the name is what a configuration file refers
//	to. Where the value comes from is a separate question, and it has one
//	answer: the environment first (the override), the keyring second (the
//	store).
//
// ★ Why the environment is checked **first**: a keychain is per user and per
//
//	machine, so a container, a CI runner and a sandbox have none — there the
//	environment is the only place the key can be. The other way round, a
//	stale keychain entry would silently win over the value an operator just
//	exported, and nobody would know which one the server used.
//
// ★ The two namespaces are the point, not a detail: they are what makes
//
//	"delete every model key" a scoped operation instead of a guess, and they
//	are what lets the LLM side and the MCP side share one resolver, one
//	store and one write surface.

const (
	// SecretService is the keyring service every secret is stored under.
	//
	// ★ One service for both namespaces: the **user** is what separates
	//   them ("llm:deepseek" and "mcp:github"), so one service means one
	//   place to look and one place to forget.
	SecretService = "openheron"

	// SecretGroupLLM and SecretGroupMCP are the two namespaces.
	//
	// ★ They are the groups of the write surface and the groups of the
	//   interface, and they are what makes "delete everything" a scoped
	//   operation: the scope is in the path.
	SecretGroupLLM = "llm"
	SecretGroupMCP = "mcp"

	llmEnvPrefix = "LLM_"
	llmEnvSuffix = "_API_KEY"
	mcpEnvPrefix = "MCP_"
	mcpEnvSuffix = "_TOKEN"

	llmUserPrefix = "llm:"
	mcpUserPrefix = "mcp:"

	// filePrefix marks a value that points at a file instead of holding the
	// secret. It is the same prefix the configuration used to carry.
	filePrefix = "@file:"
)

// LLMEnvName returns the environment variable that holds the key of a provider.
//
// ★ The name is **derived**, not written down: the provider id is the file
//
//	name, so "deepseek" can only ever be DEEPSEEK_API_KEY's provider. One
//	rule, stated once, instead of a table.
func LLMEnvName(provider string) string {
	return llmEnvPrefix + strings.ToUpper(provider) + llmEnvSuffix
}

// MCPEnvName returns the environment variable that holds the token of a server.
func MCPEnvName(server string) string {
	return mcpEnvPrefix + strings.ToUpper(server) + mcpEnvSuffix
}

// LLMSecretUser returns the keyring user that holds the key of a provider.
func LLMSecretUser(provider string) string { return llmUserPrefix + provider }

// MCPSecretUser returns the keyring user that holds the token of a server.
func MCPSecretUser(server string) string { return mcpUserPrefix + server }

// SecretUserFor maps a name back to the keyring user that holds it.
//
// ★ Only the two derived shapes have a user. A name that is not one of them
//
//	("${GITHUB_TOKEN}") belongs to no namespace, so there is nothing in the
//	keyring it could point at — it stays a plain environment lookup.
func SecretUserFor(name string) (string, bool) {
	if id, ok := stripAffix(name, llmEnvPrefix, llmEnvSuffix); ok {
		return llmUserPrefix + strings.ToLower(id), true
	}
	if id, ok := stripAffix(name, mcpEnvPrefix, mcpEnvSuffix); ok {
		return mcpUserPrefix + strings.ToLower(id), true
	}
	return "", false
}

// ValidSecretGroup reports whether a group names one of the two namespaces.
func ValidSecretGroup(group string) bool {
	return group == SecretGroupLLM || group == SecretGroupMCP
}

// SecretEnvName returns the environment variable that holds one entity's key.
//
// ★ The group decides the shape, and the shape is the namespace: a model
//
//	key is `LLM_<PROVIDER>_API_KEY` and a server's token is
//	`MCP_<SERVER>_TOKEN`. The command line and the HTTP surface ask the
//	same question, so they ask it here.
func SecretEnvName(group, name string) string {
	if group == SecretGroupMCP {
		return MCPEnvName(name)
	}
	return LLMEnvName(name)
}

// SecretUser returns the keyring user that holds one entity's key.
//
// ★ It is the group and the name: "llm" + "deepseek" ⇒ "llm:deepseek".
//
//	The prefix is what makes "delete every model key" a scoped operation
//	instead of a guess.
func SecretUser(group, name string) string {
	if group == SecretGroupMCP {
		return MCPSecretUser(name)
	}
	return LLMSecretUser(name)
}

// stripAffix removes one prefix and one suffix, and refuses an empty middle.
//
// ★ One prefix and one suffix, not "as many as fit": an id may itself end
//
//	with the suffix's letters — "foo_api_key" becomes
//	"LLM_FOO_API_KEY_API_KEY" — and stripping twice would answer a
//	different question than the one that was asked.
func stripAffix(name, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return "", false
	}
	if len(name) < len(prefix)+len(suffix) {
		return "", false
	}
	id := name[len(prefix) : len(name)-len(suffix)]
	if id == "" {
		return "", false
	}
	return id, true
}

// SecretStore is where the secrets live.
//
// ★ Get answers two different questions with two different errors: the entry
//
//	is absent (ErrSecretNotFound) or the store could not be reached at all
//	(any other error). "There is no key" and "there is no keyring here" are
//	not the same sentence, and the user needs both — the first is a setting
//	they have to make, the second is a fact about the machine.
type SecretStore interface {
	Get(user string) (string, error)
	Set(user, value string) error
	Delete(user string) error
}

var (
	// ErrSecretNotFound means the store has no entry under that user.
	ErrSecretNotFound = errors.New("secret not found in the keyring")

	// ErrSecretUnavailable means the store could not be reached.
	ErrSecretUnavailable = errors.New("the keyring is not available")
)

// A Secret is one resolved secret: the value, and where it came from.
//
// ★ Source names the **place**, never the value: "env:LLM_DEEPSEEK_API_KEY",
//
//	"keyring:llm:deepseek", "file:/etc/openheron/deepseek.key" or "none".
//	The value itself never leaves the process.
type Secret struct {
	Value  string
	Source string

	// Found says a value was found.
	Found bool

	// Empty says the variable was there and held nothing.
	//
	// ★ Its own case, because "set but empty" and "not set" are different
	//   facts: the first is a mistake the user can see and fix, the second
	//   is a step they have not taken yet. The old loader could not tell
	//   them apart — the field carried the reference and the reference
	//   carried nothing.
	Empty bool

	// Unavailable says the store could not be reached, so "not found" is
	// not what happened.
	Unavailable bool
}

// KeyringStore returns the OS keyring as a store.
//
// ★ It is **explicit on purpose**. The keyring is a global OS resource — the
//
//	Windows Credential Manager, the macOS Keychain, the Linux Secret
//	Service — and a test that wrote to it would leave entries in the
//	developer's own credential store, under a name nobody would remember to
//	delete. So the keyring is reached only where somebody asked for it: the
//	server's composition root, the `init` command, the `secret` command.
//	Everything else — a library user, a test — gets no store at all.
func KeyringStore() SecretStore { return keyringStore{} }

// nullStore is the store of a process that wired no keyring.
//
// ★ It answers "not available", not "not found": the difference is the
//
//	difference between "this machine has no keyring" and "this keyring
//	holds no such key", and the first is a fact about the machine while
//	the second is a setting the user has to make.
type nullStore struct{}

func (nullStore) Get(string) (string, error) { return "", ErrSecretUnavailable }
func (nullStore) Set(string, string) error   { return ErrSecretUnavailable }
func (nullStore) Delete(string) error        { return ErrSecretUnavailable }

// secretResolver resolves names against a store.
//
// ★ The store is a field, not a package variable: the keyring is a global
//
//	OS resource, and a test must never touch the developer's own
//	credentials. One resolver per configuration tree, one fake per test.
type secretResolver struct {
	store SecretStore
}

// nullResolver is the resolver of a tree that wired no keyring.
var nullResolver = &secretResolver{store: nullStore{}}

// keyringResolver is the resolver that reaches the OS keyring.
var keyringResolver = &secretResolver{store: keyringStore{}}

// ResolveSecret resolves a secret by name against the machine's keyring.
//
// ★ It is the explicit way to reach the credential store: the loader never
//
//	calls it, because the loader goes through the tree's own resolver. A
//	caller that wants the keyring asks for it by name.
func ResolveSecret(name string) (Secret, error) {
	return keyringResolver.resolve(name)
}

// ResolveSecretFrom resolves a secret by name against a given store.
//
// ★ A nil store means **no store**: the resolver answers "not available".
//
//	The alternative — a nil that quietly means "the keyring" — would make
//	every caller that forgot to wire one reach the developer's credential
//	store without saying so.
func ResolveSecretFrom(store SecretStore, name string) (Secret, error) {
	if store == nil {
		return nullResolver.resolve(name)
	}
	return (&secretResolver{store: store}).resolve(name)
}

// resolve runs the chain: the environment first, the keyring second.
//
// ★ The environment wins because it is the **override**: it is what a
//
//	container, a CI runner or an operator's shell hands to this process for
//	exactly this run. The keyring is what the machine keeps.
func (r *secretResolver) resolve(name string) (Secret, error) {
	if v, ok := os.LookupEnv(name); ok {
		v = strings.TrimSpace(v)
		if v == "" {
			return Secret{Source: "env:" + name, Empty: true}, nil
		}
		if strings.HasPrefix(v, filePrefix) {
			path := strings.TrimSpace(strings.TrimPrefix(v, filePrefix))
			if path == "" {
				return Secret{}, fmt.Errorf("read secret file: %s is missing a path", name)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				// ★ A path that does not resolve is a **configuration**
				// mistake, not a running condition: the user wrote a
				// path, and the path is wrong. The environment being
				// unset is the running condition, and it only earns a
				// notice.
				return Secret{}, fmt.Errorf("read secret file: %w", err)
			}
			return Secret{Value: strings.TrimSpace(string(b)), Source: "file:" + path, Found: true}, nil
		}
		return Secret{Value: v, Source: "env:" + name, Found: true}, nil
	}

	user, ok := SecretUserFor(name)
	if !ok {
		return Secret{Source: "none"}, nil
	}
	value, err := r.store.Get(user)
	switch {
	case err == nil:
		return Secret{Value: value, Source: "keyring:" + user, Found: true}, nil
	case errors.Is(err, ErrSecretNotFound):
		return Secret{Source: "none"}, nil
	default:
		// ★ The store could not be reached. That is not "there is no key",
		//   and the difference is what the notice says.
		//
		//   ★ And it is **soft**: a machine without a keyring is a fact
		//   about the machine, not a broken configuration. The key is
		//   simply not there, and the tree still loads.
		return Secret{Source: "none", Unavailable: true}, nil
	}
}

// SecretStatus is what the interface shows about one entity's secret.
//
// ★ "Effective" is the question that matters first: which of the two places
//
//	is actually in force. "Stored" alone would answer a different question
//	— is there a key in the keyring — and the two disagree exactly when the
//	environment overrides it.
type SecretStatus struct {
	// EnvVar is the environment variable that overrides the stored secret.
	EnvVar string

	// EnvSet says that variable holds something.
	EnvSet bool

	// Stored says the keyring holds the secret.
	Stored bool

	// StoredSource is where the stored secret is: "keyring:llm:deepseek".
	StoredSource string

	// Effective is the source in force: "keyring:llm:deepseek",
	// "env:LLM_DEEPSEEK_API_KEY", "file:/path" or "none".
	Effective string

	// Shadowed says the environment overrides a stored secret.
	//
	// ★ It is the trap of the whole design: the key was written, the
	//   write said "stored", and the value that is actually used is a
	//   different one. A write surface that does not say this out loud
	//   leaves the user believing they changed something.
	Shadowed bool

	// Unavailable says the keyring could not be reached.
	Unavailable bool
}

// StatusFor reports the state of one entity's secret.
//
// ★ It asks the store about the entity and the environment about the
//
//	variable, and the two answers are reported separately: what is stored
//	and what is in force are different questions.
func StatusFor(store SecretStore, name string) SecretStatus {
	st := SecretStatus{EnvVar: name, Effective: "none"}
	if user, ok := SecretUserFor(name); ok {
		if store == nil {
			// ★ No store wired: "not available", not "not found".
			st.Unavailable = true
		} else {
			_, err := store.Get(user)
			switch {
			case err == nil:
				st.Stored = true
				st.StoredSource = "keyring:" + user
			case errors.Is(err, ErrSecretNotFound):
			default:
				st.Unavailable = true
			}
		}
	}
	if v, ok := os.LookupEnv(name); ok && strings.TrimSpace(v) != "" {
		st.EnvSet = true
		st.Effective = "env:" + name
		if strings.HasPrefix(strings.TrimSpace(v), filePrefix) {
			path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), filePrefix))
			st.Effective = "file:" + path
		}
		if st.Stored {
			st.Shadowed = true
		}
		return st
	}
	if st.Stored {
		if user, ok := SecretUserFor(name); ok {
			st.Effective = "keyring:" + user
		}
	}
	return st
}

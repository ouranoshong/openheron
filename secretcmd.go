package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// secretCmd is the command line's face of the secret system.
//
// ★ It says the same thing as `/v2/secrets`, in the same two namespaces:
//
//	the groups are `llm` and `mcp`, and the actions are the same four. A
//	user who learned one has learned the other — and the two are the same
//	code path under the hood, because the group and the name decide
//	everything (`spec.SecretEnvName`, `spec.SecretUser`).
//
// ★ The value never goes into the arguments: `set` reads it from the
//
//	standard input, so it lands neither in the shell history nor in the
//	process table nor in a log line. `get` writes it to the standard output
//	— that is what it is for, and it is why the documentation says not to
//	run it on a shared terminal.
func secretCmd(args []string, stdout, stderr io.Writer) error {
	// ★ The keyring is a global OS resource, so it is asked for by name —
	//   here, in the one place that needs it.
	return secretCmdWith(args, os.Stdin, stdout, stderr, spec.KeyringStore())
}

// secretCmdWith is the command, with the input and the store handed in.
//
// ★ The store is a parameter so that a test can hand the command a fake
//
//	one. The keyring is the developer's own credential store, and a test
//	that wrote to it would leave an entry nobody would remember to delete.
//	The input is a parameter for the same reason: `set` reads the secret
//	from it, and a test must not have to replace the process's stdin.
func secretCmdWith(args []string, stdin io.Reader, stdout, stderr io.Writer, store spec.SecretStore) error {
	fs := flag.NewFlagSet("secret", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configDir := fs.String("config-dir", "",
		"the config root to read (default: <project-root>/.openheron)")
	projectRoot := fs.String("project-root", "",
		"the project root (default: the current directory)")
	asJSON := fs.Bool("json", false, "machine-readable output")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSecretUsage(stdout)
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		printSecretUsage(stdout)
		return errors.New("openheron secret: an action is required")
	}

	switch rest[0] {
	case "set":
		return secretSet(rest[1:], stdin, stdout, store)
	case "get":
		return secretGet(rest[1:], stdout, store)
	case "list":
		return secretList(*configDir, *projectRoot, *asJSON, stdout, store)
	case "remove":
		return secretRemove(rest[1:], *configDir, *projectRoot, stdout, store)
	default:
		return fmt.Errorf("openheron secret: unknown action %q; it is set, get, list or remove", rest[0])
	}
}

// ── set ─────────────────────────────────────────────────────────────────────

// secretSet stores one secret, reading the value from the standard input.
//
// ★ The value comes from the input, not from the argument list: an
//
//	argument is visible in the process table and in the shell's history, and
//	a key that was on the command line for a second is a key that was on
//	the command line for a second.
func secretSet(args []string, stdin io.Reader, stdout io.Writer, store spec.SecretStore) error {
	group, name, err := secretTarget(args)
	if err != nil {
		return err
	}
	value, err := readSecretValue(stdin)
	if err != nil {
		return err
	}
	user := spec.SecretUser(group, name)
	if err := store.Set(user, value); err != nil {
		return fmt.Errorf("openheron secret set: the keyring refused the value: %w", err)
	}
	// ★ The answer names the source that is **in force**, not only the
	//   entry that was written: the environment overrides the keyring, so
	//   a write that says "stored" alone leaves the user believing they
	//   changed something.
	st := spec.StatusFor(store, spec.SecretEnvName(group, name))
	fmt.Fprintf(stdout, "stored %s\n", user)
	fmt.Fprintf(stdout, "in force: %s\n", st.Effective)
	if st.Shadowed {
		fmt.Fprintf(stdout, "the environment variable %s wins; the stored key is not in effect\n", st.EnvVar)
	}
	return nil
}

// readSecretValue reads the value from the reader.
//
// ★ The trailing newline is dropped: a key pasted with the newline that
//
//	ended the line is the same key, and a key that keeps its newline is a
//	key that does not authenticate.
func readSecretValue(r io.Reader) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("openheron secret set: read the value: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", errors.New("openheron secret set: the value is empty")
	}
	return value, nil
}

// ── get ─────────────────────────────────────────────────────────────────────

// secretGet writes the effective value to the standard output.
//
// ★ It prints the value that is **in force** — the environment's when it is
//
//	set, the keyring's otherwise — because that is the key the server
//	would use. ★ And it is the one command here that leaks by design: the
//	documentation says not to run it on a shared terminal.
func secretGet(args []string, stdout io.Writer, store spec.SecretStore) error {
	group, name, err := secretTarget(args)
	if err != nil {
		return err
	}
	envName := spec.SecretEnvName(group, name)
	sec, err := spec.ResolveSecretFrom(store, envName)
	if err != nil {
		return fmt.Errorf("openheron secret get: %w", err)
	}
	if !sec.Found {
		return fmt.Errorf("openheron secret get: there is no key for %s: neither %s nor the keyring entry %s holds one",
			name, envName, spec.SecretUser(group, name))
	}
	fmt.Fprintln(stdout, sec.Value)
	return nil
}

// ── list ────────────────────────────────────────────────────────────────────

// secretList shows both groups and what is in force.
//
// ★ The entries are the ones the **configuration** names. The keyring
//
//	cannot be enumerated, so a leftover entry whose entity is gone is
//	invisible here — the same limit the HTTP surface has, and the same
//	reason both of them drive the list from the tree.
func secretList(configDir, projectRoot string, asJSON bool, stdout io.Writer, store spec.SecretStore) error {
	cfg, err := loadSecretConfig(configDir, projectRoot)
	if err != nil {
		return err
	}
	groups := secretGroups(cfg, store)

	if asJSON {
		return writeSecretJSON(groups, stdout)
	}
	for _, g := range groups {
		fmt.Fprintf(stdout, "%s\n", g.Label)
		if len(g.Items) == 0 {
			fmt.Fprintf(stdout, "  (none)\n")
			continue
		}
		for _, item := range g.Items {
			fmt.Fprintf(stdout, "  %-20s %-8s in force: %s\n", item.Name, secretStateLabel(item), item.Effective)
			if item.Shadowed {
				fmt.Fprintf(stdout, "  %-20s the environment variable %s wins; the stored key is not in effect\n", "", item.EnvVar)
			}
		}
	}
	return nil
}

// ── remove ──────────────────────────────────────────────────────────────────

// secretRemove deletes one entry, or every entry of a group.
//
// ★ The two are different operations with different blast radii, and the
//
//	path says which one it is: `remove llm deepseek` removes that one,
//	`remove llm` removes the whole group — the model keys, and **only**
//	the model keys.
func secretRemove(args []string, configDir, projectRoot string, stdout io.Writer, store spec.SecretStore) error {
	if len(args) == 0 {
		return errors.New("openheron secret remove: a group is required (llm or mcp)")
	}
	group := strings.TrimSpace(args[0])
	if !spec.ValidSecretGroup(group) {
		return fmt.Errorf("openheron secret remove: the group must be llm or mcp, not %q", group)
	}

	// ★ One entry: the name is enough, and no configuration is needed — the
	//   keyring user is derived from the group and the name.
	if len(args) >= 2 {
		name := strings.TrimSpace(args[1])
		if !validSecretName(group, name) {
			return fmt.Errorf("openheron secret remove: %q is not a usable name for the %s group", name, group)
		}
		err := store.Delete(spec.SecretUser(group, name))
		switch {
		case err == nil:
			fmt.Fprintf(stdout, "removed %s\n", spec.SecretUser(group, name))
			return nil
		case errors.Is(err, spec.ErrSecretNotFound):
			fmt.Fprintf(stdout, "there was no %s\n", spec.SecretUser(group, name))
			return nil
		default:
			return fmt.Errorf("openheron secret remove: the keyring refused the deletion: %w", err)
		}
	}

	// ★ The whole group: the entities have to be known, and they are known
	//   from the configuration.
	cfg, err := loadSecretConfig(configDir, projectRoot)
	if err != nil {
		return err
	}
	removed := 0
	for _, name := range secretEntityNames(group, cfg) {
		err := store.Delete(spec.SecretUser(group, name))
		switch {
		case err == nil:
			removed++
		case errors.Is(err, spec.ErrSecretNotFound):
		default:
			return fmt.Errorf("openheron secret remove: the keyring refused the deletion: %w", err)
		}
	}
	fmt.Fprintf(stdout, "removed %d %s secret(s)\n", removed, group)
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// secretTarget reads the group and the name out of the arguments.
func secretTarget(args []string) (group, name string, err error) {
	if len(args) < 2 {
		return "", "", errors.New("openheron secret: a group and a name are required (llm or mcp, then the entity)")
	}
	group = strings.TrimSpace(args[0])
	name = strings.TrimSpace(args[1])
	if !spec.ValidSecretGroup(group) {
		return "", "", fmt.Errorf("openheron secret: the group must be llm or mcp, not %q", group)
	}
	if !validSecretName(group, name) {
		return "", "", fmt.Errorf("openheron secret: %q is not a usable name for the %s group", name, group)
	}
	return group, name, nil
}

// validSecretName reports whether a name may be used in a group.
//
// ★ The name becomes a keyring user and an environment variable, so it has
//
//	to read as both. The two groups have the same shape, and it is the
//	shape an MCP server's name already takes.
func validSecretName(group, name string) bool {
	if name == "" {
		return false
	}
	if group == spec.SecretGroupMCP {
		return decl.ValidMCPServerName(name)
	}
	return decl.ValidProviderName(name)
}

// secretGroup is one namespace and its entries.
type secretGroup struct {
	ID    string       `json:"id"`
	Label string       `json:"label"`
	Items []secretItem `json:"items"`
}

// secretItem is one entity's secret, without its value.
//
// ★ The value is not in the struct and never is: the command line and the
//
//	interface show **where** a key is and **whether** it is in force.
type secretItem struct {
	Name      string `json:"name"`
	Stored    bool   `json:"stored"`
	Effective string `json:"effective"`
	Shadowed  bool   `json:"shadowed"`
	EnvVar    string `json:"envVar"`
}

// secretStateLabel says whether a key is stored.
func secretStateLabel(item secretItem) string {
	if item.Stored {
		return "stored"
	}
	return "not set"
}

// secretGroups collects both groups from the configuration.
func secretGroups(cfg *spec.Config, store spec.SecretStore) []secretGroup {
	return []secretGroup{
		{ID: spec.SecretGroupLLM, Label: "Providers",
			Items: secretItems(secretEntityNames(spec.SecretGroupLLM, cfg), spec.SecretGroupLLM, store)},
		{ID: spec.SecretGroupMCP, Label: "MCP servers",
			Items: secretItems(secretEntityNames(spec.SecretGroupMCP, cfg), spec.SecretGroupMCP, store)},
	}
}

// secretItems turns the entities of one group into their entries.
//
// ★ The name is enough: the group and the name say which variable and
//
//	which keyring entry, so the same code answers for both namespaces.
func secretItems(names []string, group string, store spec.SecretStore) []secretItem {
	out := make([]secretItem, 0, len(names))
	for _, name := range names {
		envVar := spec.SecretEnvName(group, name)
		st := spec.StatusFor(store, envVar)
		out = append(out, secretItem{
			Name:      name,
			Stored:    st.Stored,
			Effective: st.Effective,
			Shadowed:  st.Shadowed,
			EnvVar:    envVar,
		})
	}
	return out
}

// secretEntityNames lists the entities of one group, sorted.
func secretEntityNames(group string, cfg *spec.Config) []string {
	var names []string
	if group == spec.SecretGroupMCP {
		for name := range cfg.MCPs {
			names = append(names, name)
		}
	} else {
		for id := range cfg.Providers {
			names = append(names, id)
		}
	}
	sort.Strings(names)
	return names
}

// loadSecretConfig reads the configuration the entries are named by.
func loadSecretConfig(configDir, projectRoot string) (*spec.Config, error) {
	root := configDir
	project := projectRoot
	if project == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("openheron secret: resolve the working directory: %w", err)
		}
		project = wd
	}
	svc, err := spec.NewService(root, project)
	if err != nil {
		return nil, err
	}
	// ★ Unchecked: the command is about the secrets, and a tree that does
	//   not load — an agent that names a model nobody declares — still has
	//   providers and servers whose keys are worth listing.
	cfg, err := svc.LoadUnchecked()
	if err != nil {
		return nil, fmt.Errorf("openheron secret: read the configuration: %w", err)
	}
	return cfg, nil
}

// writeSecretJSON writes the list in the shape `/v2/secrets` uses.
func writeSecretJSON(groups []secretGroup, stdout io.Writer) error {
	b, err := json.MarshalIndent(map[string]any{"groups": groups}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

func printSecretUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron secret set <llm|mcp> <name>     # the value is read from stdin")
	fmt.Fprintln(stdout, "       openheron secret get <llm|mcp> <name>     # the value is written to stdout")
	fmt.Fprintln(stdout, "       openheron secret list")
	fmt.Fprintln(stdout, "       openheron secret remove <llm|mcp> <name>  # one entry")
	fmt.Fprintln(stdout, "       openheron secret remove <llm|mcp>         # the whole group")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Flags: --config-dir <path> --project-root <path> --json")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "The name says where the key comes from: llm/<name> is LLM_<NAME>_API_KEY")
	fmt.Fprintln(stdout, "and the keyring entry llm:<name>; mcp/<name> is MCP_<NAME>_TOKEN and")
	fmt.Fprintln(stdout, "the keyring entry mcp:<name>. The environment wins over the keyring.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "`get` prints a secret — do not run it on a shared terminal.")
}

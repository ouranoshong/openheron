package spec

import (
	"errors"
	"fmt"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	DirAgent   = "agents"
	DirLLM     = "llm"
	DirServer  = "server"
	DirSkills  = "skills"
	DirState   = "state"
	DirTools   = "tools"
	DirKBIndex = "kb"
	DirMCP     = "mcps"

	// SkillFileName is the one file a skill folder holds.
	//
	// ★ The name is fixed by the skill backend, not chosen here: a folder
	//   under skills/ is a skill only when it holds this file, which is why
	//   the editor's path for a skill is `skills/<name>/SKILL.md` and never
	//   `skills/<name>.md`.
	SkillFileName = "SKILL.md"
)

type Category struct {
	ID       string
	Label    string
	Dir      string
	Ext      string
	Editable bool
	hot      bool
	loader   func(c *Config, name string, data []byte) error

	// Nested says the partition is one directory deeper than the rest:
	// `skills/<name>/SKILL.md` instead of `agents/<name>.md`.
	//
	// ★ It is a property of the partition, not of the path parser: a
	//   partition either holds entity files directly (one file, one entity)
	//   or holds a folder per entity. Allowing depth everywhere would let
	//   `agents/sub/main.md` through, and the loader reads one level —
	//   a file two levels down would be saved and never loaded.
	Nested bool
}

func (c Category) Hot() bool { return c.hot }

var Categories = []Category{
	{
		ID: DirLLM, Label: "Providers", Dir: DirLLM, Ext: ".yaml",
		Editable: true, hot: true,
		loader: (*Config).loadProvider,
	},
	{
		ID: DirAgent, Label: "Agents", Dir: DirAgent, Ext: ".md",
		Editable: true, hot: true,
		loader: (*Config).loadAgent,
	},
	{
		ID: DirServer, Label: "Server", Dir: DirServer, Ext: ".yaml",
		Editable: false, hot: false,
		loader: (*Config).loadServer,
	},
	{
		ID: DirTools, Label: "Tools", Dir: DirTools, Ext: ".yaml",
		Editable: true, hot: true,
		loader: (*Config).loadTool,
	},
	{
		// ★ One MCP server, one file. The partition is hot: changing a
		//   server's command or timeout takes effect on the next turn,
		//   without a restart — which means the client has to be rebuilt
		//   with it.
		ID: DirMCP, Label: "MCP servers", Dir: DirMCP, Ext: ".yaml",
		Editable: true, hot: true,
		loader: (*Config).loadMCP,
	},
	{
		// ★ A skill is a **folder**: `skills/<name>/SKILL.md`, and the folder
		//   name is what an agent writes in `skills:`. So this is the one
		//   nested partition, and the one with no loader: skills are read by
		//   the runtime's own backend, not by this package (see
		//   `agentkit/build/skill.go` `ResolveSkillsDir`). The entry exists
		//   for the **surface** — the manifest, the path rules, the editor —
		//   which is why `loader` is nil and the load loop skips it.
		//
		// ★ Hot: editing a SKILL.md has to take effect on the next turn, and
		//   the fingerprint is what makes the snapshot rebuild (the walk is
		//   recursive, so a file two levels down counts).
		ID: DirSkills, Label: "Skills", Dir: DirSkills, Ext: ".md",
		Editable: true, hot: true, Nested: true,
	},
}

func CategoryByDir(dir string) (Category, bool) {
	for _, c := range Categories {
		if c.Dir == dir {
			return c, true
		}
	}
	return Category{}, false
}

func hotDirList() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(Categories))
	for _, c := range Categories {
		if !c.Hot() || seen[c.Dir] {
			continue
		}
		seen[c.Dir] = true
		out = append(out, c.Dir)
	}
	sort.Strings(out)
	return out
}

const DefaultAgentID = "main"

// ★ The environment variables that name the roots.
//
//	Every one of them carries the OPENHERON_ prefix. ★ A bare name like
//	LISTEN_ADDR or DATA_DIR is a name any other tool in the environment may
//	also set, and a variable somebody else owns is a variable that can
//	change this process's behaviour without anybody meaning to.
//
// ★ The previous names still work, and the old one is reported: a variable
//
//	that quietly stops working is the hardest kind of mistake to find.
const (
	// RootEnv names the openheron root — the user-level base directory the
	// configuration, the state and the log file live under.
	RootEnv = "OPENHERON_ROOT"
	// ConfigRootEnv names the config root.
	ConfigRootEnv = "OPENHERON_CONFIG_ROOT"
	// ConfigDirEnv is the previous name of ConfigRootEnv.
	ConfigDirEnv = "OPENHERON_CONFIG_DIR"
	// DefaultProjectRootEnv names the default project root: where a session
	// that belongs to no project does its work.
	DefaultProjectRootEnv = "OPENHERON_DEFAULT_PROJECT_ROOT"
	// ProjectRootEnv is the previous name of DefaultProjectRootEnv.
	ProjectRootEnv = "PROJECT_ROOT"
	// DataDirEnv names the data directory.
	DataDirEnv = "OPENHERON_DATA_DIR"
)

// DefaultConfigDirName is the config root's own name inside the openheron root.
const DefaultConfigDirName = "configs"

// DefaultProjectsDirName is the directory that holds the project roots.
const DefaultProjectsDirName = "projects"

// DefaultBaseDirName is the name of the config tree that lives inside a
// project directory — the layout that predates the openheron root.
const DefaultBaseDirName = ".openheron"

type Config struct {
	Server decl.Server   `yaml:"server"`
	KB     decl.KB       `yaml:"-"`
	Tools  decl.ToolsCfg `yaml:"-"`

	// MCPs is the mcps/ partition: one entry per server, keyed by the
	// server's name.
	//
	// ★ The file's presence is the definition — there is no "enabled" key,
	//   because whether a server exists belongs in one place.
	MCPs decl.MCPFiles `yaml:"-"`

	Providers map[string]*decl.Provider `yaml:"-"`
	Models    map[string]*decl.Model    `yaml:"-"`
	Agents    map[string]*decl.Agent    `yaml:"-"`

	Version string `yaml:"-"`

	ConfigRoot string `yaml:"-"`

	notices []Notice

	// leftOut holds the agents that are not in the tree, and why.
	//
	// ★ An agent whose model is not declared cannot run, and a tree that
	//   carries it cannot be built. The agent is **left out** — dropped from
	//   the tree, with the reason kept here — so the rest of the
	//   configuration still runs. The reason is not a decoration: the
	//   builder says it when a session asks for that agent, and the write
	//   gate says it when a change would take the agent out.
	leftOut map[string]string

	// secrets resolves the keys the configuration refers to.
	//
	// ★ It is a field because the keyring is a **global OS resource**: a
	//
	//	test has to be able to hand the loader a fake one. A package
	//	variable would be shared by every tree in the process, and a test
	//	that replaced it would race every other test that loads a tree.
	secrets *secretResolver
}

// resolver returns the resolver this configuration reads its keys through.
//
// ★ A configuration built by hand (the tests do that) has no resolver of
//
//	its own, and gets the **null** one: no store wired means no keyring
//	is reached. The tree that has a store — the server's, the one a test
//	installs — carries it.
func (c *Config) resolver() *secretResolver {
	if c == nil || c.secrets == nil {
		return nullResolver
	}
	return c.secrets
}

// LeftOut returns the agents that were left out of the tree, and why.
//
// ★ The map is **not** nil when nothing was left out — a caller that asks
//
//	"is this agent usable?" must be able to tell "no" from "not asked".
func (c *Config) LeftOut() map[string]string {
	if c.leftOut == nil {
		return map[string]string{}
	}
	return c.leftOut
}

// Notice is one deferred problem, and the file it is about.
//
// ★ The path is a separate field because the message used to carry it as
//
//	text — "llm/deepseek.yaml: api_key env X is not set" — and a text is not
//	a path. The editor has to know **which file** a notice belongs to, or it
//	shows every notice of the tree under the file the user happens to be
//	editing. `Deferred()` puts the two back together for the log, so the
//	startup line reads exactly as it did.
type Notice struct {
	// Path is relative to the configuration root. Empty when the notice is
	// not about one file — a retired partition, a watch setting.
	Path string

	Message string
}

// Deferred returns the notices as sentences.
//
// ★ The path is prefixed back onto the message: the log line is what a user
//
//	pastes into a bug report, and "the api_key env is not set" without the
//	file it is about is half a sentence.
func (c *Config) Deferred() []string {
	out := make([]string, 0, len(c.notices))
	for _, n := range c.notices {
		if n.Path == "" {
			out = append(out, n.Message)
			continue
		}
		out = append(out, n.Path+": "+n.Message)
	}
	return out
}

// Notices returns the notices with the file each one is about.
func (c *Config) Notices() []Notice { return c.notices }

// DefaultAgent returns the id of the agent a new session runs as.
// Named with a call because the package already exports the DefaultAgentID
// constant, and an identifier cannot be both.
//
// ★ The default agent is **"main" by declaration**, not by survival. An agent
//
//	that was left out of the tree is still the agent the configuration names
//	as the default — falling back to another one would run a session under an
//	agent nobody asked for, and say nothing about why the one they did ask
//	for is missing.
func (c *Config) DefaultAgent() string {
	if c == nil {
		return DefaultAgentID
	}
	if _, ok := c.Agents[DefaultAgentID]; ok {
		return DefaultAgentID
	}
	if _, ok := c.leftOut[DefaultAgentID]; ok {
		return DefaultAgentID
	}
	names := c.AgentNames()
	if len(names) == 0 {
		return DefaultAgentID
	}
	return names[0]
}

// deferKey records a notice that is not about one file.
func (c *Config) deferKey(format string, args ...any) {
	c.notices = append(c.notices, Notice{Message: fmt.Sprintf(format, args...)})
}

// deferFile records a notice about one file.
//
// ★ The path is the file's own, relative to the configuration root — and it
//
//	is the **whole** path, extension included. An earlier version put the
//	path in the message text and the call sites assembled it from a name
//	that already carried its extension, so the notice said
//	"mcps/memory.yaml.yaml".
func (c *Config) deferFile(path, format string, args ...any) {
	c.notices = append(c.notices, Notice{Path: path, Message: fmt.Sprintf(format, args...)})
}

type Service struct {
	// mu guards the marks: the write path marks from one goroutine and
	// the builder drains from another.
	mu          sync.Mutex
	root        string
	projectRoot string
	defaulted   bool

	// home is the **openheron root**: the user-level base directory the
	// configuration, the state and the log file live under.
	//
	// ★ It is empty when nobody named it — no option and no environment
	//   variable. ★ That is not a gap: a service that was never told about
	//   the openheron root keeps the old layout, where the config root is
	//   also the base of the relative paths. A library caller must not find
	//   a directory appearing in the user's home because it opened a
	//   configuration.
	home string

	// notices are the things the caller should report.
	//
	// ★ The old environment variable names land here rather than in a log:
	//   the service has no logger, and a line that names the variable that
	//   still works is worth one line in the caller's log.
	notices []string

	// skillsDir is the directory the **runtime** reads skills from, as
	// resolved by `agentkit/build` — see `SetSkillsDir`.
	//
	// ★ It is pushed in from outside, and it is not derived here, because
	//   the rule that resolves it (`$EINO_EXT_SKILLS_DIR`, then an upward
	//   probe from the working directory) belongs to the skill backend, and
	//   a second copy of it in this package would be a second answer to
	//   "which skills are loaded". Empty means "the runtime found none".
	skillsDir string

	// marked are the configuration files that changed since the last drain.
	//
	// ★ The mark is a **cache, not the truth**: the truth is the content
	//   hash on disk (see reload.go). ★ It exists so that the next turn
	//   sees a change without waiting for the watcher's next poll — the
	//   write path knows what it wrote, and the poll only guesses.
	//
	// ★ It is in-process on purpose: a mark that had to survive a restart
	//   would be a fact about the past, and the past is what the content
	//   hash is for.
	marked map[string]bool

	// secrets resolves the keys. Nil means no store is wired, so the tree
	// resolves through the null resolver and never reaches a keyring.
	//
	// ★ It belongs to the **service** and not to the tree, because the
	//   service is the thing that outlives a reload: a tree is built again
	//   on every change, and a store that had to be handed to each of them
	//   would be a store that could be forgotten between two of them.
	secrets *secretResolver
}

// SetSecretStore replaces where the secrets are read from and written to.
//
// ★ It exists so that a test — or an embedding process — can hand the
//
//	configuration a store of its own. ★ And so that the **keyring** is
//	reached only where somebody asked for it: the default is no store at
//	all, because the keyring is a global OS resource, and a test that
//	wrote to it would leave entries in the developer's own credential
//	store, under a name nobody would remember to delete.
func (s *Service) SetSecretStore(store SecretStore) {
	if s == nil {
		return
	}
	if store == nil {
		s.secrets = nil
		return
	}
	s.secrets = &secretResolver{store: store}
}

// SecretStore returns the store the secrets live in.
//
// ★ Nil means **no store is wired**, and the caller turns that into a 503
//
//	rather than pretending the tree is empty. The interface, not the
//	resolver: a caller that wants to write a secret wants the store, and
//	the resolver's chain is the loader's business.
func (s *Service) SecretStore() SecretStore {
	if s == nil || s.secrets == nil {
		return nil
	}
	return s.secrets.store
}

// MarkSecret records that a secret changed, so the next turn resolves it again.
//
// ★ The key is not in the configuration tree, so the tree's fingerprint does
//
//	not change when it does — the watcher's poll would never notice, and
//	"the next turn uses it" would be a hope instead of a promise.
//
// ★ It marks the file that **names** the entity: the provider's profile or
//
//	the server's file. That file did not change, but the tree it stands for
//	did, and that is what the mark means.
func (s *Service) MarkSecret(group, name string) {
	if s == nil || name == "" {
		return
	}
	rel := DirLLM + "/" + name + ".yaml"
	if group == SecretGroupMCP {
		rel = DirMCP + "/" + name + ".yaml"
	}
	s.Mark(rel)
}

// Mark records that a configuration file changed.
//
// ★ The mark says "look again", not "this is the new content": the content
//
//	hash is what says what the file says now.
func (s *Service) Mark(rel string) {
	if s == nil || rel == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.marked == nil {
		s.marked = map[string]bool{}
	}
	s.marked[rel] = true
}

// Marks returns the files that changed since the last drain, sorted.
func (s *Service) Marks() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.marked))
	for rel := range s.marked {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// DrainMarks returns the files that changed and forgets them.
//
// ★ The drain happens **after** the caller has read the configuration
//
//	again: a mark that is dropped before the read would lose the change,
//	and the next turn would run the old configuration with no trace that
//	it ever moved.
func (s *Service) DrainMarks() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.marked))
	for rel := range s.marked {
		out = append(out, rel)
	}
	s.marked = nil
	sort.Strings(out)
	return out
}

// SetSkillsDir tells the service where the runtime actually reads skills.
//
// ★ The editor writes `<config root>/skills/`, and that is only the same
//
//	directory as the one the runtime reads when the probe happens to land
//	there. When it does not, an edit would be saved into a tree nobody reads
//	— the exact silent degradation this project refuses — so the partition
//	says where the real one is and refuses the write.
func (s *Service) SetSkillsDir(dir string) {
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	s.skillsDir = dir
}

// SkillsDir returns the directory the runtime reads skills from.
//
// ★ It is the editor's question too: a skill written into a directory
//
//	nobody loads is a silent degradation, and the service refuses that
//	write. ★ So the answer has to be readable from outside — the rule
//	itself lives in build.ResolveSkillsDir, and the service only carries
//	what it was told.
func (s *Service) SkillsDir() string {
	if s == nil {
		return ""
	}
	return s.skillsDir
}

// skillsElsewhere reports the runtime's skills directory when it is not the
// one this service writes, and "" when there is nothing to warn about.
//
// ★ "Nothing found" is not "somewhere else": a process that resolved no
//
//	skills directory at all has not looked, and the tree the editor is about
//	to write is the one the probe would find next time it starts.
func (s *Service) skillsElsewhere() string {
	if s.skillsDir == "" || s.root == "" {
		return ""
	}
	mine := filepath.Join(s.root, DirSkills)
	if samePath(s.skillsDir, mine) {
		return ""
	}
	return s.skillsDir
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aa, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	return aa == bb
}

// ServiceOption configures a service before its roots are resolved.
type ServiceOption func(*Service)

// WithHome names the openheron root.
//
// ★ It is the user-level base directory: the config root, the data directory
//
//	and the log file live under it. ★ It is an option and not an argument
//	because the home is a **base**, not a root the service owns — the config
//	root may point anywhere, and the home is what the relative paths are
//	measured from.
func WithHome(home string) ServiceOption {
	return func(s *Service) {
		if s != nil {
			s.home = strings.TrimSpace(home)
		}
	}
}

// NewService resolves the configuration root, the openheron root and the
// default project root.
//
// ★ The **openheron root** is the user-level base directory: the config root,
//
//	the data directory and the log file live under it, and it is the one
//	root that does not move when the configuration moves. It is named by
//	`WithHome`, else by $OPENHERON_ROOT; when neither names it the service
//	keeps the old layout — the config root is also the base of the relative
//	paths — so a caller that never heard of the openheron root is not
//	surprised by a directory appearing in the user's home.
//
// ★ The **config root** is resolved in exactly three steps, in this order:
//
//  1. root, when it is not empty
//  2. $OPENHERON_CONFIG_ROOT, else its previous name $OPENHERON_CONFIG_DIR
//  3. <openheron root>/configs, created when it does not exist yet
//
// There is deliberately no upward search for an ancestor's .openheron/.
// Probing made the effective configuration root depend on the working
// directory, so the same command read a different configuration depending on
// where it happened to be started — the classic way a test run ends up
// writing into the developer's own session history.
//
// ★ The **default project root** is where a session that belongs to no
//
//	project does its work. It is resolved in the same shape: the argument,
//	then $OPENHERON_DEFAULT_PROJECT_ROOT, else its previous name
//	$PROJECT_ROOT, then <openheron root>/projects/default.
func NewService(root, projectRoot string, opts ...ServiceOption) (*Service, error) {
	s := &Service{}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	notices := s.notices

	// ★ The openheron root. Empty stays empty: an unnamed home keeps the
	//   old layout, and the layout is decided by the bases below.
	home := strings.TrimSpace(s.home)
	if home == "" {
		home = strings.TrimSpace(os.Getenv(RootEnv))
	}
	if home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return nil, fmt.Errorf("config: resolve openheron root %q: %w", home, err)
		}
		home = abs
	}

	// ★ The default project root.
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(os.Getenv(DefaultProjectRootEnv))
	}
	if projectRoot == "" {
		if v := strings.TrimSpace(os.Getenv(ProjectRootEnv)); v != "" {
			projectRoot = v
			notices = append(notices, fmt.Sprintf(
				"%s was renamed to %s; the old name still works, but it will go",
				ProjectRootEnv, DefaultProjectRootEnv))
		}
	}
	if projectRoot == "" {
		if home != "" {
			projectRoot = filepath.Join(home, DefaultProjectsDirName, "default")
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("config: resolve working directory: %w", err)
			}
			projectRoot = wd
		}
	}
	projectAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("config: resolve project root %q: %w", projectRoot, err)
	}

	// ★ The config root.
	defaulted := false
	root = strings.TrimSpace(root)
	if root == "" {
		root = strings.TrimSpace(os.Getenv(ConfigRootEnv))
	}
	if root == "" {
		if v := strings.TrimSpace(os.Getenv(ConfigDirEnv)); v != "" {
			root = v
			notices = append(notices, fmt.Sprintf(
				"%s was renamed to %s; the old name still works, but it will go",
				ConfigDirEnv, ConfigRootEnv))
		}
	}
	if root == "" {
		if home != "" {
			root = filepath.Join(home, DefaultConfigDirName)
		} else {
			// ★ The old layout: no openheron root means the config tree
			//   lives inside the project, the way it always did.
			root = filepath.Join(projectAbs, DefaultBaseDirName)
		}
		defaulted = true
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("config: resolve config root %q: %w", root, err)
	}
	if defaulted {
		// Only the defaulted root is created: an explicit --config-root or
		// $OPENHERON_CONFIG_ROOT pointing nowhere is a user typo, and it must
		// not turn into a silently empty configuration tree.
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, fmt.Errorf("config: create config root %q: %w", abs, err)
		}
	}

	return &Service{
		root:        abs,
		home:        home,
		projectRoot: projectAbs,
		defaulted:   defaulted,
		notices:     notices,
	}, nil
}

// NewServiceAt builds a service at the paths it is given.
//
// ★ It honours **only** those paths: no environment variable and no default
//
//	is consulted, because the callers that use it (`openheron init`) write
//	into the directory they were pointed at and must not read a variable
//	that decides somewhere else.
func NewServiceAt(root, projectRoot string, opts ...ServiceOption) (*Service, error) {
	s := &Service{}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("config: resolve config root %q: %w", root, err)
		}
		root = abs
	}
	if projectRoot != "" {
		abs, err := filepath.Abs(projectRoot)
		if err != nil {
			return nil, fmt.Errorf("config: resolve project root %q: %w", projectRoot, err)
		}
		projectRoot = abs
	}
	return &Service{root: root, home: s.home, projectRoot: projectRoot}, nil
}

func (s *Service) Root() string { return s.root }

// Home returns the openheron root — the user-level base directory.
//
// ★ Empty means "nobody named one", and the service then keeps the old
//
//	layout: the config root is also the base of the relative paths.
func (s *Service) Home() string { return s.home }

// Notices returns what the caller should report about the way the roots were
// resolved.
//
// ★ The old environment variable names land here rather than in a log: the
//
//	service has no logger, and a line that names the variable that still
//	works is worth one line in the caller's log.
func (s *Service) Notices() []string {
	if s == nil {
		return nil
	}
	return s.notices
}

// RootIsDefaulted reports whether the config root was derived instead of
// being named explicitly.
//
// Callers use it to tell "the user asked for a config root that is not
// there" apart from "the user did not ask for one, so we made one" — the
// two need different advice.
func (s *Service) RootIsDefaulted() bool { return s.defaulted }

// DefaultProjectRoot returns the project root the process works in when a
// session belongs to no project.
//
// ★ It is the **default**, not the only one: a session that belongs to a
//
//	project works in that project's own root, and this is where the rest of
//	them work.
func (s *Service) DefaultProjectRoot() string { return s.projectRoot }

// base is what a relative path in the configuration is measured from.
//
// ★ The openheron root when there is one, the config root otherwise.
//
//	The state and the log file are runtime state, and the runtime state
//	lives in the user-level base directory — not in the configuration tree,
//	which the user may point anywhere.
func (s *Service) base() string {
	if s.home != "" {
		return s.home
	}
	return s.root
}

// DataDir resolves the data directory: absolute as given, relative against
// the openheron root.
func (s *Service) DataDir(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = DirState
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(s.base(), v)
	}
	if err := os.MkdirAll(v, 0o755); err != nil {
		return "", fmt.Errorf("config: create data directory: %w", err)
	}
	return v, nil
}

// LogFile resolves the configured log file against the **openheron root** and
// creates the directory it lives in. Empty means no file, and that is not an
// error: stderr is the default destination.
//
// ★ The openheron root, not the config root. The log file is runtime state,
//
//	and the runtime state lives in the user-level base directory — the
//	configuration tree is the user's to point anywhere.
func (s *Service) LogFile(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !isAbsPath(v) {
		v = filepath.Join(s.base(), v)
	}
	if err := os.MkdirAll(filepath.Dir(v), 0o755); err != nil {
		return "", fmt.Errorf("config: create log directory: %w", err)
	}
	return v, nil
}

// ResolveDir resolves a relative path against the default project root.
//
// ★ The project root and not the config root: `static_dir` and the knowledge
//
//	base corpus are the one thing that points **into a project**, and a
//	project is a directory.
func (s *Service) ResolveDir(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(s.DefaultProjectRoot(), v)
}

func (s *Service) Load() (*Config, error) {
	return s.loadWith(nil)
}

func (s *Service) loadWith(m *mutation) (*Config, error) {
	if m == nil {
		return s.loadWithMany(nil)
	}
	return s.loadWithMany(map[string]*mutation{m.Rel: m})
}

func (s *Service) loadWithMany(ms map[string]*mutation) (*Config, error) {
	cfg, err := s.loadOnly(ms)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.Version = s.Fingerprint()
	return cfg, nil
}

func (s *Service) LoadUnchecked() (*Config, error) {
	return s.loadOnly(nil)
}

func (s *Service) loadOnly(ms map[string]*mutation) (*Config, error) {
	cfg := &Config{
		Providers:  map[string]*decl.Provider{},
		Models:     map[string]*decl.Model{},
		Agents:     map[string]*decl.Agent{},
		ConfigRoot: s.root,
		Server:     defaultServer(),
		KB:         defaultKB(),
		secrets:    s.secrets,
	}

	for _, cat := range Categories {
		cat := cat
		// ★ A partition with no loader is not read into `Config`: skills are
		//   the runtime's own backend's business. The entry exists for the
		//   surface (manifest, path rules, editor), and `loadDir` with a nil
		//   loader would panic on the first file it found.
		if cat.loader == nil {
			continue
		}
		if err := s.loadDir(filepath.Join(s.root, cat.Dir), cat.Ext, cat.Dir, ms,
			cfg.loaderFor(cat)); err != nil {
			return nil, err
		}
	}

	cfg.noteRetiredDirs(s.root)
	cfg.noticeConfigToolsAbsent()
	cfg.collectBadDurations()
	return cfg, nil
}

// retiredDirs are the partitions that used to be there.
//
// ★ The `policy/` partition is gone: the tiers live in the files that
//
//	provide the tools, and the caps live in `server/config.yaml`. A
//	configuration that still carries the directory is **dropped**, and a
//	dropped configuration that says nothing is the worst kind — the user
//	reads "it started fine" and believes their tiers still apply.
var retiredDirs = []struct {
	Dir   string
	Where string
}{
	{Dir: "policy", Where: "the tiers live in the file that provides each tool (`allows:`), " +
		"and the caps live in server/config.yaml"},
}

// noteRetiredDirs tells the user that a retired partition is still on disk.
//
// ★ This is a **notice**, not an error: the directory is not ours to
//
//	refuse — the rest of the configuration may be perfectly good, and a
//	broken file elsewhere would keep it from loading. So the process
//	starts, the notice is in `deferred` (and so in the startup log), and
//	the user can act on it.
func (c *Config) noteRetiredDirs(root string) {
	for _, r := range retiredDirs {
		dir := filepath.Join(root, r.Dir)
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			continue
		}
		c.deferKey("%s/ is gone and was not read — %s", r.Dir, r.Where)
	}
}

func (c *Config) loaderFor(cat Category) func(name string, data []byte) error {
	if cat.loader == nil {
		return nil
	}
	return func(name string, data []byte) error { return cat.loader(c, name, data) }
}

func (s *Service) loadDir(dir, ext, cat string, ms map[string]*mutation,
	loader func(name string, data []byte) error) error {

	pending := map[string]*mutation{}
	for _, m := range ms {
		if path.Dir(filepath.ToSlash(m.Rel)) != cat {
			continue
		}
		pending[path.Base(filepath.ToSlash(m.Rel))] = m
	}
	applyNew := func(names []string) error {
		for _, name := range names {
			m := pending[name]
			if m.Delete {
				continue
			}
			if err := loader(name, m.Content); err != nil {
				return fmt.Errorf("config: parse %s: %w", m.Rel,
					&FileError{Path: m.Rel, Err: err})
			}
		}
		return nil
	}
	pendingNames := func() []string {
		out := make([]string, 0, len(pending))
		for name := range pending {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return applyNew(pendingNames())
	}
	if err != nil {
		return fmt.Errorf("config: read directory %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	consumed := map[string]bool{}
	for _, name := range names {
		if m, ok := pending[name]; ok {
			consumed[name] = true
			if m.Delete {
				continue
			}
			if err := loader(name, m.Content); err != nil {
				return fmt.Errorf("config: parse %s: %w", m.Rel,
					&FileError{Path: m.Rel, Err: err})
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("config: read %s: %w", filepath.Join(dir, name), err)
		}
		if err := loader(name, data); err != nil {
			// ★ The path, not the bare name: two partitions can hold a file
			//   called web_search.yaml, and "parse web_search.yaml" would not
			//   say which one. The same wording as the write path's errors.
			//
			//   The path is also carried by the error itself, so a reader can
			//   ask which file is broken without taking it out of the text.
			return fmt.Errorf("config: parse %s: %w", cat+"/"+name,
				&FileError{Path: cat + "/" + name, Err: err})
		}
	}

	var newNames []string
	for _, name := range pendingNames() {
		if consumed[name] {
			continue
		}
		newNames = append(newNames, name)
	}
	return applyNew(newNames)
}

// ErrEmptyDocument signals that a config file carries no YAML document at
// all: it is empty, or it holds nothing but comments.
//
// ★ The YAML decoder reports this as io.EOF. That is true and useless —
//
//	"EOF" tells a user nothing about what to do. The distinction matters:
//	"your file has no content" is a different problem from "your file has a
//	typo", and the fix is different too.
//
// ★ The error exists because the comment-only template is a real file shape:
//
//	tools/ ships a section template that is nothing but comments, and a file
//	built from it has no document at all.
var ErrEmptyDocument = errors.New("the file is empty (or contains only comments)")

func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrEmptyDocument
		}
		return err
	}
	return nil
}

func mergeExtra(outer, inner map[string]any) map[string]any {
	if len(outer) == 0 {
		return inner
	}
	out := make(map[string]any, len(outer)+len(inner))
	for k, v := range outer {
		out[k] = v
	}
	for k, v := range inner {
		out[k] = v
	}
	return out
}

// isAbsPath reports whether a configured path means "from the root".
//
// ★ filepath.IsAbs alone is not enough on Windows: IsAbs(`\abs`) is false
//
//	because a drive is missing, so a configuration that said /usr/share/doc
//	would be joined onto the project root and silently accepted. The prefix
//	checks keep the meaning the author wrote, on either platform.
func isAbsPath(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\")
}

func (s *Service) resolvePath(p string) string {
	if p == "" || isAbsPath(p) {
		return p
	}
	return filepath.Join(s.root, p)
}

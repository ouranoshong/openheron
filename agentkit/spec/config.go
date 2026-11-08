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
)

type Category struct {
	ID       string
	Label    string
	Dir      string
	Ext      string
	Editable bool
	hot      bool
	loader   func(c *Config, name string, data []byte) error
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

const ConfigDirEnv = "OPENHERON_CONFIG_DIR"

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
	root        string
	projectRoot string
	defaulted   bool
}

// NewService resolves the configuration root and the project root.
//
// The configuration root is resolved in exactly three steps, in this order:
//
//  1. root, when it is not empty
//  2. $OPENHERON_CONFIG_DIR, when it is set
//  3. <projectRoot>/.openheron, created when it does not exist yet
//
// There is deliberately no upward search for an ancestor's .openheron/.
// Probing made the effective configuration root depend on the working
// directory, so the same command read a different configuration depending on
// where it happened to be started — the classic way a test run ends up
// writing into the developer's own session history.
func NewService(root, projectRoot string) (*Service, error) {
	if projectRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("config: resolve working directory: %w", err)
		}
		projectRoot = wd
	}
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("config: resolve project root %q: %w", projectRoot, err)
	}

	defaulted := false
	if root == "" {
		root = os.Getenv(ConfigDirEnv)
	}
	if root == "" {
		root = filepath.Join(projectRoot, DefaultBaseDirName)
		defaulted = true
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("config: resolve config root %q: %w", root, err)
	}
	if defaulted {
		// Only the defaulted root is created: an explicit --config-dir or
		// $OPENHERON_CONFIG_DIR pointing nowhere is a user typo, and it must
		// not turn into a silently empty configuration tree.
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, fmt.Errorf("config: create config root %q: %w", abs, err)
		}
	}
	return &Service{root: abs, projectRoot: projectRoot, defaulted: defaulted}, nil
}

func NewServiceAt(root, projectRoot string) (*Service, error) {
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
	return &Service{root: root, projectRoot: projectRoot}, nil
}

func (s *Service) Root() string { return s.root }

// RootIsDefaulted reports whether the config root was derived from the
// project root instead of being named explicitly.
//
// Callers use it to tell "the user asked for a config root that is not
// there" apart from "the user did not ask for one, so we made one" — the
// two need different advice.
func (s *Service) RootIsDefaulted() bool { return s.defaulted }

func (s *Service) ProjectRoot() string { return s.projectRoot }

func (s *Service) DataDir(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = DirState
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(s.root, v)
	}
	if err := os.MkdirAll(v, 0o755); err != nil {
		return "", fmt.Errorf("config: create data directory: %w", err)
	}
	return v, nil
}

func (s *Service) ResolveDir(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(s.ProjectRoot(), v)
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
	}

	for _, cat := range Categories {
		cat := cat
		if err := s.loadDir(filepath.Join(s.root, cat.Dir), cat.Ext, cat.Dir, ms,
			cfg.loaderFor(cat)); err != nil {
			return nil, err
		}
	}

	cfg.noteRetiredDirs(s.root)
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

func (s *Service) resolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.root, p)
}

package spec

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type yamlNode = yaml.Node

func (c *Config) loadProvider(file string, data []byte) error {
	id := strings.TrimSuffix(file, ".yaml")
	if id == "" {
		return fmt.Errorf("provider file name is empty")
	}

	var p decl.Provider
	if err := decodeStrict(data, &p); err != nil {
		return err
	}
	p.ID = id

	if p.Type == "" {
		return fmt.Errorf("missing type")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("missing base_url")
	}
	rawKey := p.APIKey
	key, resolved, err := expandValue(rawKey)
	if err != nil {
		return fmt.Errorf("api_key: %w", err)
	}
	if !resolved {
		p.APIKey = ""
		c.deferFile(DirLLM+"/"+id+".yaml",
			"api_key env %s is not set; calls to this endpoint will fail with 401 Unauthorized",
			envVarName(rawKey))
	} else {
		p.APIKey = key
	}

	for ref := range c.Models {
		if ProviderIDOf(ref) == id {
			delete(c.Models, ref)
		}
	}
	for i, m := range p.Models {
		if m == nil {
			continue
		}
		if m.ID == "" {
			return fmt.Errorf("models[%d]: missing id", i)
		}
		if m.SummarizeAt.Declared {
			c.deferFile(DirLLM+"/"+id+".yaml",
				"models[%s].summarize_at is no longer read — the compaction "+
					"threshold belongs to the agent; set summarize_at in the agent's own file "+
					"(agents/<id>.md), and use 0 to keep compaction off", m.ID)
		}
		c.Models[id+"/"+m.ID] = m
	}

	c.Providers[id] = &p
	return nil
}

func (c *Config) loadAgent(file string, data []byte) error {
	id := strings.TrimSuffix(file, ".md")
	fm, body, fmLine, err := splitFrontmatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}

	var a decl.Agent
	if err := decodeStrict(fm, &a); err != nil {
		// ★ The decoder counted lines inside the frontmatter block; the
		//   reader is looking at the file. The shift is what makes the two
		//   agree, and it is zero for the ordinary file that opens with `---`.
		shift := 0
		if fmLine > 0 {
			shift = fmLine - 1
		}
		return &parseShift{lines: shift, err: fmt.Errorf("%s: frontmatter: %w", id, err)}
	}
	a.ID = id
	a.File = DirAgent + "/" + file
	a.Instruction = body

	if strings.TrimSpace(a.ModelRef) == "" {
		return fmt.Errorf("%s: must declare model (no inheritance, no fallback)", id)
	}
	if _, err := ParseRef(a.ModelRef); err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}
	// ★ The old key is refused rather than renamed: the two mean
	//   **opposite** things, and renaming the key alone would turn a
	//   blacklist into a whitelist without saying so.
	if len(a.DisabledTools) > 0 {
		return fmt.Errorf("%s: `disabled_tools` was renamed to `tools`, and the meaning is "+
			"the other way round: `tools` says which tools this agent **gets**, "+
			"and `disabled_tools` said which ones it does not. Delete `disabled_tools` and "+
			"list the tools the agent should have", id)
	}
	if err := validateAgentTools(id, a.Tools); err != nil {
		return err
	}

	c.Agents[id] = &a
	return nil
}

// validateAgentTools refuses the tools entries that read like wildcards but
// are not ones.
//
// ★ Only a trailing "*" is a pattern, and a lone "*" is not one at all:
//
//	"mcp*foo" reads like a wildcard and is not one, and "tools: ["*"]" would
//	name every tool there is — which is the opposite of what a pattern
//	usually means. A rule whose reach differs from what it reads as is a
//	silent mismatch, so both are refused here — at load time, where the file
//	that says them is known.
func validateAgentTools(agentID string, tools []string) error {
	for _, entry := range tools {
		if _, _, err := decl.ParseToolNamePattern(entry); err != nil {
			return fmt.Errorf("agent %s: tools %w", agentID, err)
		}
	}
	return nil
}

func (c *Config) loadServer(file string, data []byte) error {
	s := c.Server
	if err := decodeStrict(data, &s); err != nil {
		return err
	}
	// ★ A malformed level is not fatal: the process still has to start, so
	//   it falls back to info and says so in deferred — the same rule as
	//   frontend_mode. The normalised value is what the effective config
	//   carries, so nothing downstream re-parses a spelling.
	level, err := normalizeLogLevel(s.LogLevel)
	if err != nil {
		c.deferKey("log_level fell back to info: %v", err)
	}
	s.LogLevel = level
	// ★ A negative cap is not a failed start. The rotator treats 0 as "no
	//   cap", so a negative value means the same thing after normalisation,
	//   and the notice says so — refusing to boot over a log setting would
	//   take the server down for a typo in the least important section.
	s.Log.File = strings.TrimSpace(s.Log.File)
	for _, cap := range []struct {
		key  string
		val  *int
		what string
	}{
		{"log.max_size_mb", &s.Log.MaxSizeMB, "size"},
		{"log.max_age_days", &s.Log.MaxAgeDays, "age"},
		{"log.max_backups", &s.Log.MaxBackups, "backup count"},
	} {
		if *cap.val < 0 {
			c.deferKey("%s: a negative %s cap means no cap, using 0", cap.key, cap.what)
			*cap.val = 0
		}
	}
	c.Server = s
	return nil
}

// normalizeLogLevel returns the canonical spelling, or info plus the reason.
func normalizeLogLevel(s string) (string, error) {
	if _, err := obs.ParseLevel(s); err != nil {
		return "info", err
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return "debug", nil
	case "warn", "warning":
		return "warn", nil
	case "error":
		return "error", nil
	default:
		return "info", nil
	}
}

// toolHints name what each tools/ file must carry.
//
// ★ The file's content is the tool's own configuration. ★ The file does not
//
//	decide which tools exist — the provider does: a provider whose
//	configuration is absent mounts nothing, and the filesystem tools are
//	mounted unconditionally (build/snapshot.go). So an empty file is "the
//	tool is on, with nothing configured" — which is never what the user
//	meant, and the hint says what to write instead.
var toolHints = map[string]string{
	"filesystem": "the filesystem tool set's tiers (allows)",
	"http":       "the HTTP tool's configuration (methods, headers, timeout)",
	"web_search": "the web search configuration (provider, max_results, timeout)",
	"wikipedia":  "the Wikipedia configuration (top_k, language, timeout)",
	"kb":         "the knowledge base configuration (corpus, index, model)",
}

// ToolFileNames are the file names tools/ accepts, sorted.
//
// ★ The loader, the write gate and the two error messages all read this one
//
//	list. Two copies of a whitelist is two answers to "may I write this file",
//	and the day they disagree the editor saves a file the loader rejects.
func ToolFileNames() []string {
	out := make([]string, 0, len(toolHints))
	for name := range toolHints {
		out = append(out, name+".yaml")
	}
	sort.Strings(out)
	return out
}

// decodeTool decodes one tools/ file.
//
// ★ An empty file is rejected with a hint rather than a bare parse error:
//
//	the content IS the tool's configuration, so "there is no configuration"
//	is the useful thing to say — and it is the difference between a message
//	the user can act on and a bare "EOF".
func decodeTool(data []byte, v any, hint string) error {
	err := decodeStrict(data, v)
	if errors.Is(err, ErrEmptyDocument) {
		return fmt.Errorf("%s is missing: %w", hint, err)
	}
	return err
}

// loadTool loads one tools/ file.
//
//	http.yaml        → decl.HTTPToolsCfg
//	web_search.yaml  → decl.WebSearchCfg
//	wikipedia.yaml   → decl.WikipediaCfg
//	kb.yaml          → decl.KB
func (c *Config) loadTool(file string, data []byte) error {
	switch strings.TrimSuffix(file, ".yaml") {
	case "http":
		var t decl.HTTPToolsCfg
		if err := decodeTool(data, &t, toolHints["http"]); err != nil {
			return err
		}
		// ★ The proxy is checked **here**, not when the tool is built: a
		//   wrong proxy means every request of this tool fails, and the
		//   user has to hear about it while the file is the thing being
		//   edited.
		if _, err := ParseProxy(t.Proxy); err != nil {
			return err
		}
		c.Tools.HTTP = &t
		return nil
	case "web_search":
		var t decl.WebSearchCfg
		if err := decodeTool(data, &t, toolHints["web_search"]); err != nil {
			return err
		}
		if _, err := ParseProxy(t.Proxy); err != nil {
			return err
		}
		c.Tools.WebSearch = &t
		return nil
	case "wikipedia":
		var t decl.WikipediaCfg
		if err := decodeTool(data, &t, toolHints["wikipedia"]); err != nil {
			return err
		}
		c.Tools.Wikipedia = &t
		return nil
	case "filesystem":
		var t decl.FilesystemCfg
		if err := decodeTool(data, &t, toolHints["filesystem"]); err != nil {
			return err
		}
		c.Tools.Filesystem = &t
		return nil
	case "kb":
		return c.loadKB(data)
	default:
		return fmt.Errorf("tools/ only accepts %s; unknown file %q",
			strings.Join(ToolFileNames(), ", "), file)
	}
}

func (c *Config) loadKB(data []byte) error {
	k := c.KB
	var probe struct {
		Splitter *struct {
			Headers *map[string]string `yaml:"headers"`
		} `yaml:"splitter"`
	}
	if err := yaml.Unmarshal(data, &probe); err == nil &&
		probe.Splitter != nil && probe.Splitter.Headers != nil {
		k.Splitter.Headers = nil
	}
	if err := decodeTool(data, &k, toolHints["kb"]); err != nil {
		return err
	}
	// ★ The old `corpus` key still loads — a rename must not break a
	//   working configuration. It only says so, in `deferred`, and the
	//   new key wins when both are present.
	if k.CorpusLegacy != "" {
		c.deferFile(DirTools+"/kb.yaml",
			"the key `corpus` was renamed to `corpus_dir` — "+
				"the value is a directory, and every other directory key says so "+
				"(`data_dir`, `static_dir`); rename it to keep the notice away")
		if k.CorpusDir == "" {
			k.CorpusDir = k.CorpusLegacy
		}
	}
	k.CorpusLegacy = ""
	c.KB = k
	return nil
}

// AllowedTools returns the built-in tools that run **without a nod**.
//
// ★ The default is ask, and the exceptions are named: by the file that
//
//	provides the tool (`allows:`), and — for the tools that have no file
//	of their own — by the code.
//
// ★ The list is the built-in half only: an MCP server's exceptions live in
//
//	the server's own file, in the server's own vocabulary.
func (c *Config) AllowedTools() []string {
	if c == nil {
		return nil
	}
	var out []string
	add := func(names []string) {
		for _, n := range names {
			if n != "" {
				out = append(out, n)
			}
		}
	}
	if t := c.Tools.HTTP; t != nil {
		add(t.Allows)
	}
	if t := c.Tools.WebSearch; t != nil {
		add(t.Allows)
	}
	if t := c.Tools.Wikipedia; t != nil {
		add(t.Allows)
	}
	if t := c.Tools.Filesystem; t != nil {
		add(t.Allows)
	}
	add(c.KB.Allows)
	return out
}

// ── mcps/<server>.yaml ──────────────────────────────────────────────────

// loadMCP loads one mcps/<server>.yaml file.
//
// ★ The file's name is the server's name, and it is the namespace segment
//
//	of that server's tools: mcp__<server>__<tool>. A name that does not
//	match the pattern is a load-time error — we wrote it, so we can fix
//	it, and a name with a run of underscores could not be read back.
func (c *Config) loadMCP(file string, data []byte) error {
	name := strings.TrimSuffix(file, ".yaml")
	if !decl.ValidMCPServerName(name) {
		return fmt.Errorf("mcps/%s: the server name %q must match %s", file, name, decl.MCPServerNamePattern)
	}

	var s decl.MCPServerCfg
	if err := decodeStrict(data, &s); err != nil {
		return err
	}
	s.Name = name

	transport := strings.TrimSpace(s.Transport)
	if transport == "" {
		transport = decl.MCPTransportStdio
	}
	switch transport {
	case decl.MCPTransportStdio, decl.MCPTransportHTTP:
	default:
		// ★ No silent fallback to stdio: a typo in the transport must not
		//   run a different server than the one the file names.
		return fmt.Errorf("mcps/%s: unknown transport %q (supported: %s, %s)",
			file, s.Transport, decl.MCPTransportStdio, decl.MCPTransportHTTP)
	}
	s.Transport = transport

	if transport == decl.MCPTransportHTTP {
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("mcps/%s: url is required for an http server", file)
		}
		// ★ An http server is a remote process: this process's environment
		//   is not its environment, so there is nothing to hand it. A key
		//   that does nothing is worse than a refusal — the user would
		//   believe the server runs with that variable set.
		if yamlHasKey(data, "env") {
			return fmt.Errorf("mcps/%s: env is not allowed on an http server — "+
				"it has no child process to hand an environment to", file)
		}
	} else if strings.TrimSpace(s.Command) == "" {
		return fmt.Errorf("mcps/%s: command is required for a stdio server", file)
	}

	env, err := c.expandMCPMaps(file, "env", s.Env)
	if err != nil {
		return err
	}
	s.Env = env

	headers, err := c.expandMCPMaps(file, "headers", s.Headers)
	if err != nil {
		return err
	}
	s.Headers = headers

	c.resolveMCPDuration(file, "timeout", &s.Timeout, decl.DefaultMCPTimeout)
	c.resolveMCPDuration(file, "call_timeout", &s.CallTimeout, decl.DefaultMCPCallTimeout)

	if c.MCPs == nil {
		c.MCPs = decl.MCPFiles{}
	}
	c.MCPs[name] = &s
	return nil
}

// expandMCPMaps expands the ${ENV} / @file: references of one map.
//
// ★ A variable that is not set is not fatal: it is recorded in `deferred`
//
//	and the entry is left out, so the server starts with the inherited
//	environment instead of an empty one. A missing value is a downgrade,
//	not a reason to keep the process from starting.
func (c *Config) expandMCPMaps(file, key string, in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		val, resolved, err := expandValue(v)
		if err != nil {
			return nil, fmt.Errorf("mcps/%s: %s %s: %w", file, key, k, err)
		}
		if !resolved {
			c.deferFile(DirMCP+"/"+file,
				"%s %s refers to %s, which is not set; the entry is left out",
				key, k, envVarName(v))
			continue
		}
		out[k] = val
	}
	return out, nil
}

// resolveMCPDuration applies the default to a duration that could not be
// parsed.
//
// ★ The timeout decides how long we wait, not what happens — a wrong value
//
//	must not keep the server from starting.
func (c *Config) resolveMCPDuration(file, key string, d *decl.MCPDuration, def time.Duration) {
	if !d.Declared {
		d.Value = def
		return
	}
	if d.Bad {
		c.deferFile(DirMCP+"/"+file,
			"%s %q is not a duration; using the default %s",
			key, d.Raw, def)
		d.Value = def
		return
	}
	if d.Value <= 0 {
		d.Value = def
	}
}

// yamlHasKey reports whether the document's top-level mapping carries a key.
//
// ★ The decoder cannot answer this: an `env:` with no value decodes into a
//
//	nil map, and "the key is there and empty" has to be different from
//	"the key is absent" — an http server may not declare env: at all.
func yamlHasKey(data []byte, key string) bool {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return false
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return false
		}
		node = *node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return true
		}
	}
	return false
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, v := range append(append([]string{}, a...), b...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

type Ref struct {
	ProviderID string
	ModelID    string
}

func (r Ref) String() string { return r.ProviderID + "/" + r.ModelID }

func ParseRef(s string) (Ref, error) {
	i := strings.Index(s, "/")
	if i < 0 {
		return Ref{}, fmt.Errorf("model ref %q is missing the provider prefix; expected <provider>/<model>", s)
	}
	if i == 0 || i == len(s)-1 {
		return Ref{}, fmt.Errorf("model ref %q has an empty provider or model", s)
	}
	return Ref{ProviderID: s[:i], ModelID: s[i+1:]}, nil
}

func ProviderIDOf(ref string) string {
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i]
	}
	return ref
}

// leaveOutUndeclaredModels takes the agents whose model is not declared out
// of the tree.
//
// ★ **One agent's model is not the whole tree's business to die for.** The
//
//	rest of the configuration is perfectly good, and a process that refuses
//	to start is a process that cannot be told what is wrong with it — the
//	editor that would fix it lives in that process.
//
// ★ The agent is **left out**, not half-built: it is dropped from the tree
//
//	and from every parent's `subagents:` list, so the delegation tool does
//	not offer a worker that cannot run. The reason is said out loud — the
//	startup log, `/v2/health` and the configuration editor all carry it.
//
// ★ **The default agent is left out too.** There is no session without it,
//
//	but a session is not the process: the process is also the config editor,
//	and an editor that cannot start is an editor that cannot be used to fix
//	the very thing that keeps it from starting. The session says why when
//	it asks for an agent that is not there.
func (c *Config) leaveOutUndeclaredModels(ids []string) {
	dropped := map[string]bool{}
	for _, id := range ids {
		a := c.Agents[id]
		if a == nil {
			continue
		}
		ref, _ := ParseRef(a.ModelRef)
		if _, ok := c.Models[ref.String()]; ok {
			continue
		}
		where := a.File
		if where == "" {
			where = DirAgent + "/" + id + ".md"
		}
		reason := fmt.Sprintf("the model %s is not declared under %s/", a.ModelRef, DirLLM)
		// ★ The file is the notice's `Path`, not part of its message. The old
		//   hard error carried it in the text because the error had nowhere
		//   else to put it; a notice does, and repeating it here makes the
		//   editor print it twice — "agents/x.md · agent x (agents/x.md) …".
		c.deferFile(where,
			"agent %s references model %s which is not declared under %s/; the agent is left out",
			id, a.ModelRef, DirLLM)
		if c.leftOut == nil {
			c.leftOut = map[string]string{}
		}
		c.leftOut[id] = reason
		dropped[id] = true
		delete(c.Agents, id)
	}
	if len(dropped) == 0 {
		return
	}
	for _, id := range c.AgentNames() {
		a := c.Agents[id]
		a.SubAgents = slices.DeleteFunc(a.SubAgents, func(name string) bool {
			return dropped[name]
		})
	}
}

func (c *Config) validate() error {
	ids := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// ★ Notices are collected before the hard rules, not after the ones that
	//   happen to pass. A notice says "you probably did not mean this", and a
	//   rule that fails elsewhere must not hide it: Diagnostics reports the
	//   two together, and today's order made a typo'd tool name vanish
	//   whenever the file also named a model that does not exist.
	c.noticeCompactionWindows(ids)
	c.noticeAgentTools(ids)

	if _, ok := c.Agents[DefaultAgentID]; !ok {
		return &FileError{Path: DirAgent + "/" + DefaultAgentID + ".md", Err: fmt.Errorf(
			"agents/%s.md does not exist or could not be parsed", DefaultAgentID)}
	}

	// ★ One agent's undeclared model leaves **that agent** out; it does not
	//   take the tree down with it. The rest of the configuration is good,
	//   and a process that will not start is a process that cannot be told
	//   what is wrong with it — the editor that would fix it lives in that
	//   process.
	c.leaveOutUndeclaredModels(ids)
	ids = c.AgentNames()

	for _, id := range ids {
		a := c.Agents[id]
		ref, _ := ParseRef(a.ModelRef)
		m := c.Models[ref.String()]
		if err := checkReasoning(id, a, m); err != nil {
			return err
		}
	}

	if err := c.validateAgentGraph(ids); err != nil {
		return err
	}
	if err := c.validateKB(); err != nil {
		return err
	}
	if err := c.validateMCP(); err != nil {
		return err
	}
	return c.validateDepth(ids)
}

// checkReasoning refuses a level the model does not declare.
//
// ★ The vocabulary belongs to the **model**, not to the agent: the table under
// `reasoning:` in llm/<provider>.yaml is what a level turns into at that
// vendor. A level the table does not carry is not "a level that means
// nothing" — the request goes out with no reasoning configured at all, so
// `reasoning: max` on a model that declares off/low/high silently thinks not
// at all. That is why the declaration is checked instead of tolerated.
//
// ★ The check used to live in the settings editor, where it covered only the
// edits made in that editor. Every other way of writing the file — an editor,
// a generated file, a copy from another machine, an agent's own config_write —
// went unchecked, which is the wrong half to cover.
func checkReasoning(id string, a *decl.Agent, m *decl.Model) error {
	// ★ An agent that names no level inherits the model's reasoning_default,
	//   and a model with no table has no closed vocabulary to check against.
	if a == nil || m == nil || a.Reasoning == "" || len(m.Reasoning) == 0 {
		return nil
	}
	if _, ok := m.Reasoning[a.Reasoning]; ok {
		return nil
	}
	levels := make([]string, 0, len(m.Reasoning))
	for level := range m.Reasoning {
		levels = append(levels, level)
	}
	sort.Strings(levels)
	where := a.File
	if where == "" {
		where = DirAgent + "/" + id + ".md"
	}
	return &FileError{Path: where, Err: fmt.Errorf(
		"%s: reasoning %q is not declared by %s — the model declares: %s",
		where, a.Reasoning, a.ModelRef, strings.Join(levels, ", "))}
}

// containsName reports whether the list carries the name.
func containsName(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// noticeAgentTools reports the two things an agent's tools: list can get
// wrong without breaking anything.
//
// ★ Both are **notices**, not errors: the configuration is legal, it just
//
//	does not say what the author meant. A tool that does not exist is a
//	typo; a skill tool with no skills is a contradiction.
func (c *Config) noticeAgentTools(ids []string) {
	known := c.knownToolNames()
	for _, id := range ids {
		a := c.Agents[id]
		if a == nil {
			continue
		}
		for _, entry := range a.Tools {
			if entry == "" {
				continue
			}
			// ★ An MCP tool is discovered at runtime, so the server's own
			//   names are not knowable here: a name it does not have
			//   yet is not a typo. Every other name is checkable, and
			//   checkable exactly — a provider's tool exists only when
			//   its file is written.
			if strings.HasPrefix(entry, decl.MCPToolPrefix) {
				continue
			}
			prefix, isPattern, err := decl.ParseToolNamePattern(entry)
			if err != nil {
				continue
			}
			if isPattern {
				continue
			}
			if known[prefix] {
				continue
			}
			c.deferFile(DirAgent+"/"+id+".md",
				"tools names %q, which is not a tool that exists — "+
					"the built-in tools are %s, and a provider's tool exists only when its "+
					"file under tools/ is written", prefix, strings.Join(decl.BuiltinToolNames, ", "))
		}
		// ★ The skill tool without a skill is a contradiction: the agent
		//   can ask for a skill and there is none to load.
		if len(a.Skills) == 0 && containsName(a.Tools, decl.ToolNameSkill) {
			c.deferFile(DirAgent+"/"+id+".md",
				"tools names the %q tool but skills is empty — "+
					"the agent can ask for a skill and there is none to load",
				decl.ToolNameSkill)
		}
	}
}

// knownToolNames is every tool name that exists right now: the built-in
// ones and the ones the written providers offer.
func (c *Config) knownToolNames() map[string]bool {
	out := make(map[string]bool, len(decl.BuiltinToolNames)+len(decl.ConfigToolNames)+8)
	for _, n := range decl.BuiltinToolNames {
		out[n] = true
	}
	for _, n := range c.EnabledToolNames() {
		out[n] = true
	}
	// ★ The config tools exist whenever the process is running: they answer
	//   from the configuration service, not from a file. A name the loader
	//   does not know is reported as a typo, so they have to be here.
	for _, n := range decl.ConfigToolNames {
		out[n] = true
	}
	// ★ The knowledge base's own tools exist whenever its file is written.
	if c.KB.CorpusDir != "" || c.KB.CorpusLegacy != "" {
		out[decl.ToolNameKBSearch] = true
		out[decl.ToolNameKBIndex] = true
	}
	return out
}

// validateMCP checks the agents' mcps: lists.
//
// ★ A name that does not exist is a load-time error: a typo would
//
//	silently narrow the agent to no server at all, and a tool that is
//	missing because of a typo looks exactly like a tool that is missing
//	because the server is down.
func (c *Config) validateMCP() error {
	known := make([]string, 0, len(c.MCPs))
	for name := range c.MCPs {
		known = append(known, name)
	}
	sort.Strings(known)

	for _, id := range c.AgentNames() {
		a := c.Agents[id]
		if a == nil {
			continue
		}
		for _, name := range a.MCPServers {
			if _, ok := c.MCPs[name]; ok {
				continue
			}
			// ★ The error belongs to the **agent's** file, not to the
			//   server that is missing: the agent is where the reference
			//   lives, and it is the only place the user can remove it. A
			//   path-less error lands on the tree in the editor, and a
			//   reader there cannot click their way to the fix.
			return &FileError{Path: agentFile(a, id), Err: fmt.Errorf(
				"agent %s lists the MCP server %q, which is not declared under %s/ (declared: %s)",
				id, name, DirMCP, strings.Join(known, ", "))}
		}
		c.checkAgentMCPNames(id, a)
	}
	return nil
}

// agentFile is the path of an agent's file, with the default the loader
// would have used.
func agentFile(a *decl.Agent, id string) string {
	if a != nil && a.File != "" {
		return a.File
	}
	return DirAgent + "/" + id + ".md"
}

// checkAgentMCPNames reports the MCP entries of an agent's tools: list.
//
// ★ The two keys no longer have to agree: the tools: list is about the
//
//	**built-in** tools, and the mcps: list is about the servers. ★ So an
//	`mcp__<server>__<tool>` entry says nothing — and it is a **notice**,
//	not an error: the file still loads, the agent still gets the server's
//	tools when it mounts the server, and the user is told that the line
//	does not do what it looks like it does.
//
// ★ One notice per agent, naming every server the list mentions: three
//
//	lines of the same kind are one thing to fix, not three.
func (c *Config) checkAgentMCPNames(agentID string, a *decl.Agent) {
	if len(a.Tools) == 0 {
		return
	}
	var servers []string
	seen := map[string]bool{}
	for _, entry := range a.Tools {
		prefix, _, err := decl.ParseToolNamePattern(entry)
		if err != nil {
			continue
		}
		prefix = strings.TrimSuffix(prefix, "*")
		if !strings.HasPrefix(prefix, decl.MCPToolPrefix) {
			continue
		}
		server, _, ok := strings.Cut(strings.TrimPrefix(prefix, decl.MCPToolPrefix), decl.MCPSeparator)
		if !ok || server == "" || seen[server] {
			continue
		}
		seen[server] = true
		servers = append(servers, server)
	}
	if len(servers) == 0 {
		return
	}
	c.deferFile(agentFile(a, agentID),
		"agent %s names MCP tools in tools: (%s), but the tools list does not name MCP tools — "+
			"mcps: is what mounts a server and gives the agent its tools; drop those lines, "+
			"and add the server to mcps: if the agent should have its tools",
		agentID, strings.Join(servers, ", "))
}

// MCPServersInUse returns the servers some agent lists.
//
// ★ The list is the **union**: the snapshot's tool set is shared by every
//
//	agent, so the manager starts the servers some agent lists. A server
//	no agent lists is never started, and a server only a sub-agent lists
//	is started — that sub-agent may be the one running.
func (c *Config) MCPServersInUse() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(c.MCPs))
	for _, id := range c.AgentNames() {
		a := c.Agents[id]
		if a == nil {
			continue
		}
		for _, name := range a.MCPServers {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// noticeCompactionWindows reports the agents whose automatic compaction
// cannot happen because their model declares no window.
//
// A percentage is measured against the window: without one there is nothing
// to measure against, and the conversation grows until the endpoint refuses
// it. Saying so out loud beats silently doing nothing — the user asked for
// compaction and gets none.
func (c *Config) noticeCompactionWindows(ids []string) {
	for _, id := range ids {
		a := c.Agents[id]
		if a == nil {
			continue
		}
		if a.SummarizeAt.Off || a.SummarizeAt.Tokens > 0 {
			// An explicit token count needs no window.
			continue
		}
		ref, _ := ParseRef(a.ModelRef)
		m, ok := c.Models[ref.String()]
		if !ok || m == nil || m.ContextWindow > 0 {
			continue
		}
		c.deferFile(DirLLM+"/"+ProviderIDOf(a.ModelRef)+".yaml",
			"the model %s declares no context_window — automatic compaction is "+
				"inactive for agent %s; declare context_window, or set summarize_at to an absolute token count",
			a.ModelRef, id)
	}
}

// kbErr attributes a knowledge-base complaint to the file that carries it.
//
// ★ The knowledge base lives in `tools/kb.yaml`, and every one of these
//
//	rules is about what that file says — the model it names is declared
//	elsewhere, but the file that names it is this one.
func kbErr(format string, args ...any) error {
	return &FileError{Path: DirTools + "/kb.yaml", Err: fmt.Errorf(format, args...)}
}

func (c *Config) validateKB() error {
	if c.KB.CorpusDir == "" {
		return nil
	}
	if isAbsPath(c.KB.CorpusDir) {
		return kbErr("kb.corpus_dir must be relative to the project root, got an absolute path %q; "+
			"the index and its corpus must have the same lifetime", c.KB.CorpusDir)
	}
	if c.KB.Model == "" {
		return kbErr("kb.model is required when kb.corpus_dir is set; "+
			"it must reference a model declared with kind: embedding under %s/", DirLLM)
	}
	ref, _ := ParseRef(c.KB.Model)
	m, ok := c.Models[ref.String()]
	if !ok {
		return kbErr("kb.model %s is not declared under %s/", c.KB.Model, DirLLM)
	}
	if !m.IsEmbedding() {
		return kbErr("kb.model %s is not an embedding model; add `kind: embedding` to it "+
			"(a chat endpoint has no %s endpoint)", c.KB.Model, "/embeddings")
	}
	if m.Dimensions < 0 {
		return kbErr("kb.model %s declares dimensions: %d; it must be a positive count or "+
			"omitted (0 = unknown, learned at index time)", c.KB.Model, m.Dimensions)
	}
	return nil
}

func (c *Config) validateDepth(sorted []string) error {
	max := c.MaxDepth()
	for _, id := range sorted {
		d, ok := c.teamDepth(id)
		if !ok {
			continue
		}
		if d > max {
			return fmt.Errorf(
				"agent %s has a team nested to depth %d, hitting the cap of %d; "+
					"raise server/config.yaml agent.max_depth "+
					"or reduce the subagents nesting",
				id, d, max)
		}
	}
	return nil
}

func (c *Config) teamDepth(id string) (int, bool) {
	if _, ok := c.Agents[id]; !ok {
		return 0, false
	}
	visited := map[string]bool{}
	queue := []struct {
		name  string
		depth int
	}{{id, 0}}
	deepest := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth > deepest {
			deepest = cur.depth
		}
		if visited[cur.name] {
			continue
		}
		visited[cur.name] = true
		if sub, ok := c.Agents[cur.name]; ok {
			for _, s := range sub.SubAgents {
				queue = append(queue, struct {
					name  string
					depth int
				}{s, cur.depth + 1})
			}
		}
	}
	return deepest, true
}

// MaxDepth is the depth cap of the agent tree.
//
// ★ The cap lives in **server/config.yaml**: one place, one value. It is
//
//	not hot-reloaded — a change takes effect on the next start — and that
//	is the price of not having a second copy of the same number.
func (c *Config) MaxDepth() int {
	if c == nil || c.Server.Agent.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return c.Server.Agent.MaxDepth
}

// MaxRetries is the cap on the model calls one turn makes.
func (c *Config) MaxRetries() int {
	if c == nil || c.Server.Retry.MaxRetries <= 0 {
		return DefaultMaxRetries
	}
	return c.Server.Retry.MaxRetries
}

func (c *Config) validateAgentGraph(sorted []string) error {
	for _, id := range sorted {
		for _, sub := range c.Agents[id].SubAgents {
			if _, ok := c.Agents[sub]; !ok {
				known := make([]string, 0, len(c.Agents))
				for k := range c.Agents {
					known = append(known, k)
				}
				sort.Strings(known)
				return fmt.Errorf("agent %s subagents references unknown %q (declared: %s)",
					id, sub, strings.Join(known, ", "))
			}
		}
	}
	return c.detectCycle(sorted)
}

func (c *Config) detectCycle(sorted []string) error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string

	var visit func(string) error
	visit = func(id string) error {
		color[id] = grey
		path = append(path, id)
		for _, sub := range c.Agents[id].SubAgents {
			switch color[sub] {
			case grey:
				at := 0
				for i, p := range path {
					if p == sub {
						at = i
						break
					}
				}
				return fmt.Errorf("subagents contains a cycle: %s", strings.Join(append(append([]string{}, path[at:]...), sub), " → "))
			case white:
				if err := visit(sub); err != nil {
					return err
				}
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		return nil
	}

	for _, id := range sorted {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

type mutation struct {
	Rel     string
	Delete  bool
	Content []byte
}

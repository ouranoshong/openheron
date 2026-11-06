package decl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type yamlNode = yaml.Node

const DefaultDebounce = 200 * time.Millisecond

type Provider struct {
	ID string `yaml:"-"`

	Type string `yaml:"type"`

	APIKey string `yaml:"api_key"`

	BaseURL string `yaml:"base_url"`

	Header map[string]string `yaml:"header"`

	Timeout Duration `yaml:"timeout"`

	Extra map[string]any `yaml:"extra"`

	HintKeys []string `yaml:"hint_keys"`

	Models []*Model `yaml:"models"`
}

type Model struct {
	ID string `yaml:"id"`

	Kind string `yaml:"kind"`

	Dimensions int `yaml:"dimensions"`

	ContextWindow int `yaml:"context_window"`

	// SummarizeAt is the token count at which the agent's context is
	// compacted: past it, the conversation is replaced by a summary.
	//
	// Deprecated: the threshold belongs to the agent, not to the model —
	// see Agent.SummarizeAt. The field is still parsed so that an old
	// configuration keeps loading, but nothing reads it; the spec package
	// points at the agent setting instead.
	SummarizeAt Threshold `yaml:"summarize_at"`

	Timeout Duration `yaml:"timeout"`

	ReasoningField string `yaml:"reasoning_field"`

	ReasoningFieldFrom string `yaml:"reasoning_field_from"`

	Extra map[string]any `yaml:"extra"`

	Reasoning map[string]map[string]any `yaml:"reasoning"`

	ReasoningDefault string `yaml:"reasoning_default"`
}

func (m *Model) LevelFields(level string) (map[string]any, bool) {
	if m == nil || level == "" {
		return nil, false
	}
	f, ok := m.Reasoning[level]
	return f, ok
}

// ── agents/<agent-id>.md ─────────────────────────────────────────────────

type Agent struct {
	ID string `yaml:"-"`

	File string `yaml:"-"`

	Description string `yaml:"description"`

	ModelRef string `yaml:"model"`

	Instruction string `yaml:"-"`

	MaxIterations int `yaml:"max_iterations"`

	SubAgents []string `yaml:"subagents"`

	DisabledTools []string `yaml:"disabled_tools"`

	// Skills is the allow-list of skills this agent may use.
	//
	// ★ A nil list — the key is absent — means every skill in the skills
	//   directory: that is what an agent got before this field existed. An
	//   empty list means none. The pointer is what tells the two apart, the
	//   same way Threshold tells "not declared" from "declared off".
	//
	// ★ The names are the **folders** under skills/ (the skill's own
	//   frontmatter name is accepted as well). The folder is the identity the
	//   user sees in the directory tree; the frontmatter name is the identity
	//   the model sees in the tool.
	Skills *[]string `yaml:"skills"`

	// MCPServers is the allow-list of MCP servers this agent may use.
	//
	// ★ A nil list — the key is absent — means every configured server,
	//   and an empty list means none. The pointer tells the two apart, the
	//   same way it does for Skills: the common case is **one** place, and
	//   writing the server file is enough.
	MCPServers *[]string `yaml:"mcps"`

	Temperature *float32 `yaml:"temperature"`
	MaxTokens   *int     `yaml:"max_tokens"`

	Reasoning string `yaml:"reasoning"`

	// SummarizeAt is the compaction threshold of this agent.
	//
	// The threshold belongs to the agent, not to the model: the window is
	// the model's, but how much of it this agent fills before its history
	// is summarized is this agent's own policy. See Threshold.
	SummarizeAt Threshold `yaml:"summarize_at"`
}

// Threshold is a compaction threshold.
//
// It is spelled three ways in the file:
//
//	summarize_at: 104858   an absolute token count
//	summarize_at: 80%      a share of the model's context window
//	summarize_at: off      never compact
//
// The zero value means "not declared", which is not the same as "declared
// off": an agent that says nothing gets the default, and the default is the
// spec package's business, not this one's.
//
// Decoding only. What the value means once the window is known is resolved
// by the spec package.
type Threshold struct {
	// Percent is the share of the context window, 1..100.
	Percent int

	// Tokens is an absolute token count.
	Tokens int

	// Off is set when the threshold explicitly disables compaction.
	Off bool

	// Declared reports whether the key was present in the file at all.
	Declared bool
}

// UnmarshalYAML accepts a token count, a percentage, or "off".
func (t *Threshold) UnmarshalYAML(n *yamlNode) error {
	t.Declared = true
	switch n.Tag {
	case "!!int":
		var v int
		if err := n.Decode(&v); err != nil {
			return fmt.Errorf("cannot parse summarize_at: %w", err)
		}
		if v <= 0 {
			t.Off = true
			return nil
		}
		t.Tokens = v
		return nil
	case "!!str":
		var s string
		if err := n.Decode(&s); err != nil {
			return fmt.Errorf("cannot parse summarize_at: %w", err)
		}
		return t.parseString(strings.TrimSpace(s))
	default:
		return fmt.Errorf("summarize_at must be a token count, a percentage, or \"off\"")
	}
}

// parseString reads the string spellings: a percentage, "off", or a token
// count written as a string.
func (t *Threshold) parseString(s string) error {
	switch strings.ToLower(s) {
	case "":
		return fmt.Errorf("summarize_at must not be empty")
	case "off", "never", "none":
		t.Off = true
		return nil
	}
	if strings.HasSuffix(s, "%") {
		raw := strings.TrimSpace(strings.TrimSuffix(s, "%"))
		v, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("cannot parse summarize_at %q: not a percentage", s)
		}
		if v <= 0 || v > 100 {
			return fmt.Errorf("summarize_at %q must be between 1%% and 100%%", s)
		}
		t.Percent = v
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("cannot parse summarize_at %q: not a token count, a percentage, or \"off\"", s)
	}
	if v <= 0 {
		t.Off = true
		return nil
	}
	t.Tokens = v
	return nil
}

// ── policy/permissions.yaml ──────────────────────────────────────────────

type Policy struct {
	Ask  []string `yaml:"ask"`
	Deny []string `yaml:"deny"`
}

type Limits struct {
	Agent AgentLimit `yaml:"agent"`
	Retry RetryLimit `yaml:"retry"`
}

type AgentLimit struct {
	MaxDepth int `yaml:"max_depth"`
}

type RetryLimit struct {
	MaxRetries int `yaml:"max_retries"`
}

const DefaultMaxDepth = 1

const DefaultMaxRetries = 5

type Tier string

const (
	TierAllow Tier = "allow"
	TierAsk   Tier = "ask"
	TierDeny  Tier = "deny"
)

func (p Policy) TierOf(tool string) Tier {
	if contains(p.Deny, tool) {
		return TierDeny
	}
	if contains(p.Ask, tool) {
		return TierAsk
	}
	return TierAllow
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ── server/config.yaml ───────────────────────────────────────────────────

type Server struct {
	ListenAddr   string          `yaml:"listen_addr"`
	StaticDir    string          `yaml:"static_dir"`
	DataDir      string          `yaml:"data_dir"`
	FrontendMode string          `yaml:"frontend_mode"`
	StartTimeout LenientDuration `yaml:"start_timeout"`
	// LeaseTTL is how long a run's lease survives without progress.
	//
	// ★ It must be **longer than the provider's own timeout**: a model
	//   that is slow but alive must not be fenced out while it is
	//   thinking. The provider's timeout ends a call that is hung; the
	//   lease only has to outlast a call that is slow.
	LeaseTTL LenientDuration `yaml:"lease_ttl"`
	Watch    Watch           `yaml:"watch"`
	Retry    RetryCfg        `yaml:"retry"`
	Agent    AgentCfg        `yaml:"agent"`
}

const (
	ToolNameHTTPGet    = "request_get"
	ToolNameHTTPPost   = "request_post"
	ToolNameHTTPPut    = "request_put"
	ToolNameHTTPDelete = "request_delete"
	ToolNameWebSearch  = "duckduckgo_search"
	ToolNameWikipedia  = "wikipedia_search"
)

type ToolsCfg struct {
	HTTP      *HTTPToolsCfg `yaml:"http"`
	WebSearch *WebSearchCfg `yaml:"web_search"`
	Wikipedia *WikipediaCfg `yaml:"wikipedia"`
}

type HTTPToolsCfg struct {
	Methods []string          `yaml:"methods"`
	Headers map[string]string `yaml:"headers"`
	Timeout LenientDuration   `yaml:"timeout"`
}

type WebSearchCfg struct {
	Provider   string          `yaml:"provider"`
	MaxResults int             `yaml:"max_results"`
	Timeout    LenientDuration `yaml:"timeout"`
	Region     string          `yaml:"region"`
}

type WikipediaCfg struct {
	TopK     int             `yaml:"top_k"`
	Language string          `yaml:"language"`
	Timeout  LenientDuration `yaml:"timeout"`
	BaseURL  string          `yaml:"base_url"`
}

type AgentCfg struct {
	MaxDepth int `yaml:"max_depth"`
}

// ── mcps/<server>.yaml ──────────────────────────────────────────────────

// MCPToolPrefix is the namespace every MCP tool is exposed under.
//
// ★ The prefix is what makes a namespace usable: a rule that says
//
//	"mcp__github__*" reaches exactly the tools of one server, because the
//	boundary between the server and the tool is unambiguous — a server name
//	may not contain a run of underscores.
const MCPToolPrefix = "mcp__"

// MCPSeparator is what stands between the server and the tool in an
// exposed name.
//
// ★ It is two underscores and not one, because a server name may not
//
//	contain a run of underscores: the boundary has to be unambiguous, or
//	"mcp__a__b__x" could not be read back.
const MCPSeparator = "__"

// MCPServerNamePattern is the shape a server name may take.
//
// ★ A server name may not contain a run of underscores: otherwise
//
//	"mcp__a__b__x" cannot be read back — is that server "a" with the tool
//	"b__x", or server "a__b" with the tool "x"?
const MCPServerNamePattern = `^[a-z0-9]+(_[a-z0-9]+)*$`

var mcpServerNameRE = regexp.MustCompile(MCPServerNamePattern)

// ValidMCPServerName reports whether a server name may be used.
func ValidMCPServerName(name string) bool {
	return mcpServerNameRE.MatchString(name)
}

// ParseToolNamePattern reads one entry of an agent's disabled_tools.
//
// ★ Only a **trailing** "*" is a pattern, and a lone "*" is not one at
//
//	all: "mcp*foo" reads like a wildcard and is not one, and a rule whose
//	reach differs from what it reads as is a silent mismatch. The loader
//	refuses both; the builder only ever sees the shapes that are left.
func ParseToolNamePattern(entry string) (prefix string, isPattern bool, err error) {
	if !strings.Contains(entry, "*") {
		return entry, false, nil
	}
	if entry == "*" {
		return "", false, fmt.Errorf(
			"\"*\" is a wildcard, not a prefix pattern; write the tools out, " +
				"or use a server prefix such as \"mcp__github__*\"")
	}
	if !strings.HasSuffix(entry, "*") {
		return "", false, fmt.Errorf(
			"\"%s\" may only use a trailing \"*\"; \"mcp*foo\" reads like a wildcard but is not one",
			entry)
	}
	if strings.Count(entry, "*") > 1 {
		return "", false, fmt.Errorf(
			"\"%s\" holds more than one \"*\"; only a trailing \"*\" is a pattern", entry)
	}
	return strings.TrimSuffix(entry, "*"), true, nil
}

// The two transports an MCP server may speak.
//
// ★ "http" is streamable HTTP and only that: SSE is the older transport,
//
//	and a transport the configuration cannot ask for is a transport that
//	would have to be maintained without ever being used.
const (
	MCPTransportStdio = "stdio"
	MCPTransportHTTP  = "http"
)

// MCPMaxExposedNameLen is the longest name an MCP tool may be exposed under.
//
// ★ A longer name is skipped rather than truncated: truncating collides,
//
//	and the collision drops exactly the part that tells two tools apart.
const MCPMaxExposedNameLen = 64

// The default ceilings of an MCP server.
const (
	DefaultMCPTimeout     = 30 * time.Second
	DefaultMCPCallTimeout = 60 * time.Second
)

// MCPDuration is a duration that degrades instead of failing the load.
//
// ★ A wrong timeout changes *how long we wait*, not *what happens* — it
//
//	must not keep a server from starting. The bad value is kept so the
//	loader can name the file and the key: the package-level list every
//	other lenient duration uses carries no source at all.
type MCPDuration struct {
	// Value is the resolved duration.
	Value time.Duration
	// Declared reports whether the key was present in the file at all.
	Declared bool
	// Bad reports that the written value could not be parsed.
	Bad bool
	// Raw is the value as written.
	Raw string
}

func (d *MCPDuration) UnmarshalYAML(n *yamlNode) error {
	d.Declared = true
	d.Raw = n.Value
	var s string
	if err := n.Decode(&s); err != nil {
		d.Bad = true
		return nil
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || v < 0 {
		d.Bad = true
		return nil
	}
	d.Value = v
	return nil
}

// MCPServerCfg is one server's declaration.
//
// ★ The file is the server: the file's name is the server's name, and
//
//	removing the file removes the definition. There is no "enabled" key.
type MCPServerCfg struct {
	// Name is the server's name — the file name without the extension.
	Name string `yaml:"-"`

	// Transport is "stdio" or "http". An unknown value is a load-time
	// error: a typo must not run a different server.
	Transport string `yaml:"transport"`

	// Command is the executable to run. Stdio only, and required.
	Command string `yaml:"command"`

	// Args are the command's arguments. Stdio only.
	Args []string `yaml:"args"`

	// Env is the child's environment, layered over the inherited one.
	//
	// ★ It is stdio's business: an http server has no child process, so
	//   this process's environment is not its environment — declaring it
	//   there is a load-time error rather than a key that does nothing.
	Env map[string]string `yaml:"env"`

	// URL is the server's address. Http only, and required.
	URL string `yaml:"url"`

	// Headers are sent on every request. Http only.
	Headers map[string]string `yaml:"headers"`

	// Timeout is the ceiling for connect, initialize and list tools.
	Timeout MCPDuration `yaml:"timeout"`

	// CallTimeout is the ceiling for one tool call.
	CallTimeout MCPDuration `yaml:"call_timeout"`

	// Tools filters the server's own tools to the ones worth offering.
	//
	// ★ It is a filter, not a switch: an absent or empty list means all of
	//   them, and the list never adds a tool the server does not have.
	Tools []string `yaml:"tools"`

	// Allows names the server's own tools that run without a nod.
	//
	// ★ Everything else this server offers asks — including the tools it
	//   has not offered yet. The names are the server's own, not the
	//   exposed ones: a file speaks its own vocabulary.
	Allows []string `yaml:"allows"`
}

// MCPFiles is the mcps/ partition: one entry per server, keyed by its name.
type MCPFiles map[string]*MCPServerCfg

type Watch struct {
	Enabled  *bool           `yaml:"enabled"`
	Debounce LenientDuration `yaml:"debounce"`
}

type LenientDuration time.Duration

func (d *LenientDuration) UnmarshalYAML(n *yamlNode) error {
	var strict Duration
	if err := strict.UnmarshalYAML(n); err != nil {
		recordBadDuration(err.Error())
		*d = LenientDuration(DefaultDebounce)
		return nil
	}
	*d = LenientDuration(strict)
	return nil
}

func (d LenientDuration) Duration() time.Duration { return time.Duration(d) }

var badDurations []string

func recordBadDuration(msg string) { badDurations = append(badDurations, msg) }

func BadDurations() []string {
	out := append([]string(nil), badDurations...)
	badDurations = nil
	return out
}

type RetryCfg struct {
	MaxRetries int `yaml:"max_retries"`
}

type KB struct {
	// CorpusDir is the directory the knowledge base indexes.
	//
	// ★ It was called `corpus` before 2026-11-06. The name followed the
	//   information-retrieval term, but the value is a **directory**, and
	//   every other directory in the configuration says so: `data_dir`,
	//   `static_dir`. A user who does not know the term should still be
	//   able to read the key.
	CorpusDir string `yaml:"corpus_dir"`

	// CorpusLegacy is the old spelling, read once so an existing
	// configuration does not break on the rename.
	//
	// ★ It is **not** part of the configuration's shape: the loader keeps
	//   it only to say "this key moved" in `deferred`. Nothing else reads
	//   it.
	CorpusLegacy string `yaml:"corpus"`

	Index string `yaml:"index"`

	Model string `yaml:"model"`

	QueryInstruction string `yaml:"query_instruction"`

	BatchSize int `yaml:"batch_size"`

	MaxBatchChars int `yaml:"max_batch_chars"`

	TopK int `yaml:"top_k"`

	Splitter KBSplitter `yaml:"splitter"`
}

type KBSplitter struct {
	Headers map[string]string `yaml:"headers"`
}

type ModelKind string

const (
	ModelKindChat      ModelKind = "chat"
	ModelKindEmbedding ModelKind = "embedding"
)

func (m *Model) IsEmbedding() bool { return m != nil && ModelKind(m.Kind) == ModelKindEmbedding }

type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yamlNode) error {
	if n.Tag != "!!str" {
		return fmt.Errorf("duration must be a string with a unit (e.g. 60s / 5m); bare numbers are not allowed")
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("cannot parse duration: %w", err)
	}
	if strings.TrimSpace(s) == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("cannot parse duration %q: %w", s, err)
	}
	if v < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d Duration) Duration() time.Duration { return time.Duration(d) }

type SubAgentSpec struct {
	ID            string
	Description   string
	Instruction   string
	MaxIter       int
	ModelRef      string
	DisabledTools []string
	// Skills is the agent's skill allow-list. A nil list means every skill in
	// the skills directory; an empty list means none.
	Skills *[]string
	// MCPServers is the agent's MCP server allow-list. A nil list means every
	// configured server; an empty list means none.
	//
	// ★ A sub-agent carries a list of its own, next to its DisabledTools
	//   and its Skills: the list is read from the agent that is running,
	//   not from the root's.
	MCPServers  *[]string
	Reasoning   string
	Temperature *float32
	MaxTokens   *int
	// SummarizeAt is the agent's own compaction threshold, unresolved: a
	// percentage still needs the window of the model to become a token
	// count.
	SummarizeAt Threshold
	Sub         []SubAgentSpec
}

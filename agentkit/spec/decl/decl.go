package decl

import (
	"fmt"
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

	Temperature *float32 `yaml:"temperature"`
	MaxTokens   *int     `yaml:"max_tokens"`

	Reasoning string `yaml:"reasoning"`
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
	Watch        Watch           `yaml:"watch"`
	Retry        RetryCfg        `yaml:"retry"`
	Agent        AgentCfg        `yaml:"agent"`
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
	Corpus string `yaml:"corpus"`

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
	Reasoning     string
	Temperature   *float32
	MaxTokens     *int
	Sub           []SubAgentSpec
}

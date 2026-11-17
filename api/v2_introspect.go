package api

import (
	"context"
	"sort"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/build"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

// The /v2 introspection family exists because a surprising amount of what
// openheron knows was previously unreachable from the browser:
//   - Runtime.Status() existed but no route exposed it, so the sidebar could
//     not tell a running session from an idle one.
//   - Policy.GatedToolNames() was documented as "for frontend rendering" and
//   - spec.Deferred() only ever reached the startup log, so a silently
//     downgraded setting was invisible in the UI.
//   - Nothing reported the agent topology, which is the first thing one wants
//     to see when studying a multi-agent run.

// startupAt is stamped once so /v2/health can report uptime context.
var startupAt = time.Now().UTC()

// requireConfig returns the live config or writes 503.
func (s *Server) requireConfig(c *app.RequestContext) *spec.Config {
	if s.cfgFn == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"no config source is wired", false)
		return nil
	}
	cfg := s.cfgFn()
	if cfg == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"config is not loaded yet", true)
		return nil
	}
	return cfg
}

type ToolsSource interface {
	EffectiveTools(agentID string) []ToolVerdict
	PolicyTools() []ToolVerdict
	RuntimeTools(agentID string) Observed
	DescriptionVerdicts() []DescriptionVerdict
}

type DescriptionVerdict struct {
	AgentID    string
	Empty      bool
	Duplicates []string
	Distinct   bool
}

type Observed struct {
	Names []string
	Known bool
}

type ToolVerdict struct {
	Name          string
	Tier          string
	Reason        string
	Present       bool
	ObservedKnown bool
}

// SetToolsSource injects the source of the effective tool table.
func (s *Server) SetToolsSource(src ToolsSource) { s.toolsFn = src }

// ContextSource reports what each agent's context allows.
//
// The builder owns that lookup: the answer has to be the model the agent
// actually runs on, not the one the configuration mentions.
type ContextSource interface {
	Contexts() map[string]contract.AgentContext
}

// SetContextSource injects the source of the context windows.
func (s *Server) SetContextSource(src ContextSource) { s.contextsFn = src }

// contexts returns the context windows of every agent.
func (s *Server) contexts() map[string]contract.AgentContext {
	if s.contextsFn == nil {
		return nil
	}
	return s.contextsFn.Contexts()
}

// ── agent topology ───────────────────────────────────────────────────────

type modelView struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	Reasoning          string   `json:"reasoning"`
	ReasoningAvailable []string `json:"reasoningAvailable"`
	ReasoningDefault   string   `json:"reasoningDefault"`
	// ContextWindow is the model's context window. Zero means the user did
	// not configure one.
	ContextWindow int `json:"contextWindow"`
}

type agentView struct {
	ID            string    `json:"id"`
	Description   string    `json:"description,omitempty"`
	Model         modelView `json:"model"`
	MaxIterations int       `json:"maxIterations"`
	Subagents     []string  `json:"subagents"`
	// Tools is the agent's tool allow-list: the tools it may use.
	//
	// ★ The default is none — an absent list and an empty list mean the
	//   same thing, so there is nothing else to report.
	Tools []string `json:"tools"`
	// Skills is the agent's skill allow-list. The default is none.
	Skills                []string   `json:"skills"`
	EffectiveTools        []toolView `json:"effectiveTools,omitempty"`
	RuntimeTools          []string   `json:"runtimeTools,omitempty"`
	RuntimeToolsKnown     bool       `json:"runtimeToolsKnown"`
	DescriptionDistinct   bool       `json:"descriptionDistinct"`
	DescriptionDuplicates []string   `json:"descriptionDuplicates,omitempty"`
	// SummarizeAt is the compaction threshold of this agent, in tokens.
	// Zero means the agent never compacts — either because it says so, or
	// because the model declares no window to measure against.
	SummarizeAt int `json:"summarizeAt"`
}

type toolView struct {
	Name          string `json:"name"`
	Tier          string `json:"tier"`
	Reason        string `json:"reason,omitempty"`
	Present       bool   `json:"present"`
	ObservedKnown bool   `json:"observedKnown"`
}

type agentsView struct {
	DefaultAgent string      `json:"defaultAgent"`
	MaxDepth     int         `json:"maxDepth"`
	Agents       []agentView `json:"agents"`
	Version      string      `json:"version"`
}

func (s *Server) v2Agents(_ context.Context, c *app.RequestContext) {
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}
	c.JSON(consts.StatusOK, agentsView{
		DefaultAgent: cfg.DefaultAgent(),
		MaxDepth:     cfg.MaxDepth(),
		Agents:       s.agentViews(cfg, false),
		Version:      cfg.Version,
	})
}

func (s *Server) v2AgentDetail(_ context.Context, c *app.RequestContext) {
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}
	id := c.Param("agentId")
	views := s.agentViews(cfg, true)
	for i := range views {
		if views[i].ID == id {
			c.JSON(consts.StatusOK, views[i])
			return
		}
	}
	s.v2Fail(c, consts.StatusNotFound, "agent_not_found",
		"agent "+id+" is not declared", false)
}

// agentViews projects the declared topology.
//
//	counter: the depth guard in agent/subagent.go removes `task` once an agent
//	hits max_depth, so "what this agent may do" is a runtime property while
//	"how deep is this agent" is a configuration one. Only the latter is asked
//	for here.
func (s *Server) agentViews(cfg *spec.Config, withTools bool) []agentView {
	out := make([]agentView, 0, len(cfg.Agents))
	verdicts := s.descriptionVerdicts()
	contexts := s.contexts()
	for _, id := range cfg.AgentNames() {
		a := cfg.Agents[id]
		v := agentView{
			ID:            id,
			Description:   a.Description,
			MaxIterations: a.MaxIterations,
			Subagents:     a.SubAgents,
			Tools:         a.Tools,
			Model:         modelOf(cfg, a),
			SummarizeAt:   contexts[id].SummarizeAt,
		}
		if v.Subagents == nil {
			v.Subagents = []string{}
		}
		if v.Tools == nil {
			v.Tools = []string{}
		}
		v.Skills = append([]string{}, a.Skills...)
		if v.Skills == nil {
			v.Skills = []string{}
		}
		if d, ok := verdicts[id]; ok {
			v.DescriptionDistinct = d.Distinct
			v.DescriptionDuplicates = d.Duplicates
		}
		if withTools {
			v.EffectiveTools = s.effectiveTools(id)
			if s.toolsFn != nil {
				obs := s.toolsFn.RuntimeTools(id)
				v.RuntimeTools = obs.Names
				v.RuntimeToolsKnown = obs.Known
			}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) descriptionVerdicts() map[string]DescriptionVerdict {
	out := map[string]DescriptionVerdict{}
	if s.toolsFn == nil {
		return out
	}
	for _, v := range s.toolsFn.DescriptionVerdicts() {
		out[v.AgentID] = v
	}
	return out
}

func modelOf(cfg *spec.Config, a *decl.Agent) modelView {
	ref, err := spec.ParseRef(a.ModelRef)
	if err != nil {
		return modelView{}
	}
	// build.Resolve is the single place that knows how a ref resolves; calling
	// it here keeps the introspect endpoint from re-deriving the lookup.
	p, m, err := build.Resolve(cfg, a.ModelRef)
	if err != nil || p == nil || m == nil {
		return modelView{Provider: ref.ProviderID, Model: ref.ModelID, Reasoning: a.Reasoning}
	}
	available := make([]string, 0, len(m.Reasoning))
	for level := range m.Reasoning {
		available = append(available, level)
	}
	sort.Strings(available)
	level := a.Reasoning
	if level == "" {
		level = m.ReasoningDefault
	}
	return modelView{
		Provider:           ref.ProviderID,
		Model:              ref.ModelID,
		Reasoning:          level,
		ReasoningAvailable: available,
		ReasoningDefault:   m.ReasoningDefault,
		ContextWindow:      m.ContextWindow,
	}
}

// effectiveTools reports which tools an agent can actually reach and under
// which policy tier.
func (s *Server) effectiveTools(agentID string) []toolView {
	if s.toolsFn == nil {
		return nil
	}
	verdicts := s.toolsFn.EffectiveTools(agentID)
	out := make([]toolView, 0, len(verdicts))
	for _, v := range verdicts {
		out = append(out, toolView{
			Name:          v.Name,
			Tier:          string(v.Tier),
			Reason:        v.Reason,
			Present:       v.Present,
			ObservedKnown: v.ObservedKnown,
		})
	}
	return out
}

// policyTools reports the policy's own tiers, with no agent layering.
func (s *Server) policyTools() []toolView {
	if s.toolsFn == nil {
		return nil
	}
	verdicts := s.toolsFn.PolicyTools()
	out := make([]toolView, 0, len(verdicts))
	for _, v := range verdicts {
		out = append(out, toolView{Name: v.Name, Tier: string(v.Tier), Reason: v.Reason})
	}
	return out
}

// ── GET /v2/config ───────────────────────────────────────────────────────

// policyView is the tier model as the configuration states it.
//
// ★ There is no ask list and no deny list any more: the default is ask,
//
//	and the exceptions are the tools that run without a nod — named by
//	the file that provides them.
type policyView struct {
	DefaultTier string   `json:"defaultTier"`
	Allow       []string `json:"allow"`
}

type providerView struct {
	ID      string `json:"id"`
	BaseURL string `json:"baseUrl"`
	// HasAPIKey is a boolean on purpose: the key itself must never leave
	// the process.
	HasAPIKey bool `json:"hasApiKey"`
	// KeySource names the **place** the key came from:
	// "env:LLM_DEEPSEEK_API_KEY", "keyring:llm:deepseek", "file:/path" or
	// "none".
	//
	// ★ It is the answer to a question `hasApiKey` cannot answer: a key
	//   that is set and a key that is in force are two different facts, and
	//   only the second one says which key the server will use.
	KeySource string       `json:"keySource"`
	Models    []modelBrief `json:"models"`
}

type modelBrief struct {
	ID              string   `json:"id"`
	ContextWindow   int      `json:"contextWindow"`
	Timeout         string   `json:"timeout,omitempty"`
	ReasoningLevels []string `json:"reasoningLevels,omitempty"`
}

// SkillView is one discovered skill.
type SkillView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Context     string `json:"context,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
}

// skillList is the wire shape of GET /v2/skills.
type skillList struct {
	Skills []SkillView `json:"skills"`
}

func (s *Server) v2Skills(_ context.Context, c *app.RequestContext) {
	out := skillList{Skills: []SkillView{}}
	if s.skillsFn != nil {
		out.Skills = s.skillsFn()
	}
	c.JSON(consts.StatusOK, out)
}

type serverView struct {
	StartTimeout  string `json:"startTimeout"`
	MaxRetries    int    `json:"maxRetries"`
	MaxDepth      int    `json:"maxDepth"`
	WatchEnabled  bool   `json:"watchEnabled"`
	WatchDebounce string `json:"watchDebounce"`
	// LogLevel is the level in effect, not the configured value: an env
	// override has to be visible here or the field lies about the output.
	LogLevel string `json:"logLevel"`
}

type configView struct {
	Version      string         `json:"version"`
	Deferred     []string       `json:"deferred"`
	DefaultAgent string         `json:"defaultAgent"`
	MaxDepth     int            `json:"maxDepth"`
	Policy       policyView     `json:"policy"`
	Tools        []toolView     `json:"tools"`
	Agents       []agentView    `json:"agents"`
	Providers    []providerView `json:"providers"`
	Skills       []SkillView    `json:"skills"`
	Server       serverView     `json:"server"`
	// MCPServers are the configured MCP servers: the configuration says
	// what exists, and the panel says what is up.
	MCPServers []mcpServerBrief `json:"mcpServers"`
}

// allowNames picks the tools that run without a nod out of the tier table.
//
// ★ The table is the truth about the tiers, so the allow list is read off
//
//	it — a second source would be a second answer.
func allowNames(tiers []toolView) []string {
	out := make([]string, 0, len(tiers))
	for _, t := range tiers {
		if t.Tier == string(decl.TierAllow) && t.Reason == "" {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) v2Config(_ context.Context, c *app.RequestContext) {
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}

	// ★ The tier table comes from the builder, not from the configuration:
	//   the allow list is assembled from three places (the code's own
	//   built-in readers, a file's `allows:`, an MCP server's `allows:`),
	//   and recomputing half of it here would make this view contradict the
	//   tool table it carries next to it.
	tiers := s.toolTiers()

	out := configView{
		Version:      cfg.Version,
		Deferred:     cfg.Deferred(),
		DefaultAgent: cfg.DefaultAgent(),
		MaxDepth:     cfg.MaxDepth(),
		Policy: policyView{
			DefaultTier: string(decl.TierAsk),
			Allow:       allowNames(tiers),
		},
		Tools:      tiers,
		Agents:     s.agentViews(cfg, false),
		MCPServers: configuredMCP(cfg),
		Server: serverView{
			StartTimeout:  cfg.Server.StartTimeout.Duration().String(),
			MaxRetries:    cfg.MaxRetries(),
			MaxDepth:      cfg.MaxDepth(),
			WatchEnabled:  cfg.Server.Watch.Enabled == nil || *cfg.Server.Watch.Enabled,
			WatchDebounce: cfg.Server.Watch.Debounce.Duration().String(),
			LogLevel:      s.logLevel(),
		},
	}

	for _, id := range sortedKeys(cfg.Providers) {
		p := cfg.Providers[id]
		pv := providerView{
			ID: p.ID,
			// hasApiKey is a boolean on purpose: the key itself must never
			// leave the process.
			HasAPIKey: p.APIKey != "",
			// ★ The source names the place, not the value.
			KeySource: p.KeySource,
			Models:    []modelBrief{},
		}
		if p.BaseURL != "" {
			pv.BaseURL = p.BaseURL
		}
		for _, m := range p.Models {
			brief := modelBrief{ID: m.ID, ContextWindow: m.ContextWindow}
			if m.Timeout > 0 {
				brief.Timeout = m.Timeout.Duration().String()
			}
			if len(m.Reasoning) > 0 {
				brief.ReasoningLevels = make([]string, 0, len(m.Reasoning))
				for level := range m.Reasoning {
					brief.ReasoningLevels = append(brief.ReasoningLevels, level)
				}
				sort.Strings(brief.ReasoningLevels)
			}
			pv.Models = append(pv.Models, brief)
		}
		out.Providers = append(out.Providers, pv)
	}
	if out.Providers == nil {
		out.Providers = []providerView{}
	}

	if s.skillsFn != nil {
		out.Skills = s.skillsFn()
	}
	if out.Skills == nil {
		out.Skills = []SkillView{}
	}
	c.JSON(consts.StatusOK, out)
}

func (s *Server) toolTiers() []toolView {
	return s.policyTools()
}

// ── GET /v2/health ───────────────────────────────────────────────────────

func (s *Server) v2Health(_ context.Context, c *app.RequestContext) {
	body := map[string]any{
		"status":     "ok",
		"startedAt":  startupAt.Format(rfc3339Nano),
		"uptimeSecs": int64(time.Since(startupAt).Seconds()),
	}
	if cfg := s.cfgFn; cfg != nil {
		if live := cfg(); live != nil {
			body["configVersion"] = live.Version
			body["deferred"] = nonNil(live.Deferred())
		}
	}
	c.JSON(consts.StatusOK, body)
}

// ── GET /v2/runtime ──────────────────────────────────────────────────────

func (s *Server) v2Runtime(ctx context.Context, c *app.RequestContext) {
	type statusView struct {
		ThreadID  string `json:"threadId"`
		State     string `json:"state"`
		RunID     string `json:"runId,omitempty"`
		StartedAt string `json:"startedAt,omitempty"`
	}
	sessions := make([]statusView, 0)
	for _, st := range s.rt.Status() {
		v := statusView{ThreadID: st.SessionID, State: string(st.Phase), RunID: st.RunID}
		if !st.StartedAt.IsZero() {
			v.StartedAt = st.StartedAt.UTC().Format(rfc3339Nano)
		}
		sessions = append(sessions, v)
	}

	body := map[string]any{"sessions": sessions}
	if report, err := s.sm.Inspect(ctx); err == nil {
		// Ownerless is the one that bites: a checkpoint with no pending
		// approval cannot be recovered, because the interrupt id lives inside
		// the gob payload openheron never decodes.
		body["consistency"] = map[string]int{
			"clean":          report.Clean,
			"pendingOk":      report.PendingOK,
			"orphan":         report.Orphan,
			"ownerless":      report.Ownerless,
			"skippedBadJson": report.SkippedBadJSON,
		}
	}
	if s.poolFn != nil {
		body["pool"] = s.poolFn()
	}
	c.JSON(consts.StatusOK, body)
}

// ── small helpers ────────────────────────────────────────────────────────

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package api

import (
	"context"
	"sort"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/build"
	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
)

// The /v2 introspection family exists because a surprising amount of what
// openheron knows was previously unreachable from the browser:
//
//   - Runtime.Status() existed but no route exposed it, so the sidebar could
//     not tell a running session from an idle one.
//   - Policy.GatedToolNames() was documented as "for frontend rendering" and
//     had no route at all. ★ 它后来**整个删掉**了（2026-10-21）：/v2/agents
//     报的是每个工具的**生效结论**（ToolVerdict：档位 + 被哪层摘的 + 运行期
//     有没有出现过），比一份「需要人批准的名字」多得多，也少一份要同步的真相。
//   - spec.Deferred() only ever reached the startup log, so a silently
//     downgraded setting was invisible in the UI.
//   - Nothing reported the agent topology, which is the first thing one wants
//     to see when studying a multi-agent run.

// startupAt is stamped once so /v2/health can report uptime context.
var startupAt = time.Now().UTC()

// requireConfig returns the live config or writes 503.
func (s *Server) requireConfig(c *app.RequestContext) *spec.Config {
	if s.cfgFn == nil {
		v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"no config source is wired", false)
		return nil
	}
	cfg := s.cfgFn()
	if cfg == nil {
		v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"config is not loaded yet", true)
		return nil
	}
	return cfg
}

// ── 生效工具表 ────────────────────────────────────────────────────────────

// ToolsSource 报告某个 agent 的生效工具表。
//
// ★ 它是**注入**而不是让 api 包 import agent 包：判定归装配期所有
//
//	（deny 赢过 disabled_tools、兜底 allow 都在 build.Table 里），api 重算一遍
//	就是两份真相。而漂移的表现是接口说 allow、运行时其实是 deny ——
//	自省接口开始说谎，且没有任何东西会响。
type ToolsSource interface {
	// EffectiveTools 报告 agentID 名下每个工具的生效结论。
	EffectiveTools(agentID string) []ToolVerdict
	// PolicyTools 报告策略本身的三档，不含任何 agent 的分层。
	PolicyTools() []ToolVerdict
	// RuntimeTools 报告 agentID 名下**运行期真实出现过**的工具。
	RuntimeTools(agentID string) Observed
	// DescriptionVerdicts 报告各 agent 描述的**可区分性**。
	//
	// ★ 模型选专家**只依据描述** —— 描述空或与别的雷同，模型就分不出
	//   该叫谁，而症状（「模型老是不叫那个专家」）从外面看不出原因。
	DescriptionVerdicts() []DescriptionVerdict
}

// DescriptionVerdict 报告一个 agent 的描述够不够模型分辨。
//
// ★ 它是**提示**不是闸门：模型选错工具的后果由模型承担，
//
//	配置校验不该因为「两段描述像」就拦住启动。
type DescriptionVerdict struct {
	// AgentID 是被判定的 agent。
	AgentID string
	// Empty 报告描述为空 —— 最严重的一种：模型连它是干什么的都不知道。
	Empty bool
	// Duplicates 是与它**高度相似**的其它 agent id。
	Duplicates []string
	// Distinct 报告它是否可区分。
	Distinct bool
}

// Observed 是运行期观测到的事实。
//
// ★ Known 为 false 表示**还没跑过任何一轮** —— 那是正常状态，不是错误。
//
//	`task` 与 `skill` 由 eino 的中间件注册，名字不出现在任何配置文件里，
//	只有跑起来才知道有哪些。接口必须把「还不知道」与「没有」分开报：
//	此前它把两者混为一谈，于是那些工具从来没被报出来过。
type Observed struct {
	Names []string
	Known bool
}

// ToolVerdict 是单个工具的生效结论。
//
// ★ 它**不是** build.ToolVerdict 的别名 —— api 不 import agent（依赖铁律：
//
//	只经 agentkit/run 拿装配）。代价是要在 main.go 的 toolsAdapter 里翻译一次。
//	那份翻译只搬字段、不做判断，所以两份结构可以各自演进 ——
//	**前提是判定始终只在 agent 包发生**，而那是本文件下面那句
//	「判定全部委托给 ToolsSource」保证的。
type ToolVerdict struct {
	Name   string
	Tier   string
	Reason string
	// Present 报告这个名字是否真的在运行期出现过（见 [Observed]）。
	Present bool
	// ObservedKnown 表示 Present 可信；false = 还没跑过任何一轮。
	ObservedKnown bool
}

// SetToolsSource injects the source of the effective tool table.
func (s *Server) SetToolsSource(src ToolsSource) { s.toolsFn = src }

// ── agent topology ───────────────────────────────────────────────────────

type modelView struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	Reasoning          string   `json:"reasoning"`
	ReasoningAvailable []string `json:"reasoningAvailable"`
	ReasoningDefault   string   `json:"reasoningDefault"`
}

type agentView struct {
	ID string `json:"id"`
	// ★ 没有「深度」也没有「归属根」了：每个 agent 都能当会话的 agent，
	//
	//	而一个 agent 可以同时是别人的下属、又是自己的入口 —— 「它在谁的
	//	队伍里第几层」这个问题没有唯一答案。它自己的下属写在 Subagents 里。
	Description   string    `json:"description,omitempty"`
	Model         modelView `json:"model"`
	MaxIterations int       `json:"maxIterations"`
	Subagents     []string  `json:"subagents"`
	DisabledTools []string  `json:"disabledTools"`
	// EffectiveTools 是每个工具的生效结论。★ 它的**全集**由配置里点过名的
	// 名字加上运行期观测到的名字合成。
	EffectiveTools []toolView `json:"effectiveTools,omitempty"`
	// RuntimeTools 是运行期真实出现过的工具名。
	//
	// ★ 与 EffectiveTools 分开报，是因为两者回答的是不同的问题：
	//   EffectiveTools 说「这个 agent 能不能用 X」，RuntimeTools 说「除了
	//   配置里点过名的，还有哪些工具真的存在」。此前只有前者，于是由框架
	//   注册的工具从来没出现在任何地方。
	RuntimeTools []string `json:"runtimeTools,omitempty"`
	// RuntimeToolsKnown 为 false 表示**还没跑过任何一轮**，因而
	//	RuntimeTools 是空集合而非「没有工具」。
	RuntimeToolsKnown bool `json:"runtimeToolsKnown"`
	// DescriptionDistinct 报告这个 agent 的描述是否**够模型分辨**。
	//
	// ★ false = 描述为空，或与别的 agent 高度相似 —— 此时模型可能
	//   永远不叫它，而从外面看不出为什么。
	DescriptionDistinct bool `json:"descriptionDistinct"`
	// DescriptionDuplicates 是与它高度相似的其它 agent id。
	DescriptionDuplicates []string `json:"descriptionDuplicates,omitempty"`
}

type toolView struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Reason string `json:"reason,omitempty"`
	// Present 报告这个名字是否真的在运行期出现过。
	//
	// ★ 它把「挡住了一个真实存在的工具」与「策略点名了一个不存在的工具」
	//   分开 —— 后者通常是 `deny: [grep_search]` 这种拼错，而此前两者长得
	//   一模一样：都是一行 deny。
	Present bool `json:"present"`
	// ObservedKnown 表示 Present 可信；false = 还没跑过任何一轮。
	ObservedKnown bool `json:"observedKnown"`
}

type agentsView struct {
	// DefaultAgent 是新会话默认用的那个 agent。
	//
	// ★ **每个声明了的 agent 都能当会话的 agent**（装配按它自己的配置来），
	//   所以这里没有「可切换名单」那种字段 —— 前端列全部 agent 即可。
	DefaultAgent string      `json:"defaultAgent"`
	MaxDepth     int         `json:"maxDepth"`
	Agents       []agentView `json:"agents"`
	// Version 是**当前配置的指纹**。
	//
	// ★ 它让前端能知道「配置变了」：前端拿它跟上次看到的比，一变就重新
	//   拉这份列表。没有它的话，用户在**编辑器里**改了 `agents/*.md`、
	//   服务端热更新了，而前端的列表还是缓存里的旧份 —— 界面在骗人。
	Version string `json:"version"`
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
	v2Fail(c, consts.StatusNotFound, "agent_not_found",
		"agent "+id+" is not declared", false)
}

// agentViews projects the declared topology.
//
// ★ Depth comes from the declared subagents graph, not from a server-side
//
//	counter: the depth guard in agent/subagent.go removes `task` once an agent
//	hits max_depth, so "what this agent may do" is a runtime property while
//	"how deep is this agent" is a configuration one. Only the latter is asked
//	for here.
func (s *Server) agentViews(cfg *spec.Config, withTools bool) []agentView {
	out := make([]agentView, 0, len(cfg.Agents))
	// ★ 描述的可区分性**一次算完**：它是全体之间的比较（谁跟谁像），
	//   逐个算就是 O(n²) 次重复计算。
	verdicts := s.descriptionVerdicts()
	for _, id := range cfg.AgentNames() {
		a := cfg.Agents[id]
		v := agentView{
			ID:            id,
			Description:   a.Description,
			MaxIterations: a.MaxIterations,
			Subagents:     a.SubAgents,
			DisabledTools: a.DisabledTools,
			Model:         modelOf(cfg, a),
		}
		if v.Subagents == nil {
			v.Subagents = []string{}
		}
		if v.DisabledTools == nil {
			v.DisabledTools = []string{}
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

// descriptionVerdicts 把描述体检的结论按 agent id 收成一张表。
//
// ★ 没有装配源时返回空表 —— 那时「描述是否重复」无从谈起，
//
//	而报一个「都不重复」比不报更坏。
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
	}
}

// effectiveTools reports which tools an agent can actually reach and under
// which policy tier.
//
// ★ 判定**全部**委托给 [ToolsSource]：deny 赢过 disabled_tools、兜底 allow
//
//	这些规则归 agent 包的装配期所有，在这里重算一遍就是「两份真相」——
//	而漂移的表现是接口说 allow、运行时其实是 deny，没有任何东西会响。
//	没有注入源时整块省略：报告一个猜测出来的表比不报更糟。
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

type policyView struct {
	// ★ 没有 allow 那一列 —— 未点名的工具一律放行 ✅（兜底档就是 allow ✅）。
	Ask         []string `json:"ask"`
	Deny        []string `json:"deny"`
	DefaultTier string   `json:"defaultTier"`
}

type providerView struct {
	ID        string       `json:"id"`
	BaseURL   string       `json:"baseUrl"`
	HasAPIKey bool         `json:"hasApiKey"`
	Models    []modelBrief `json:"models"`
}

type modelBrief struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"contextWindow"`
	Timeout       string `json:"timeout,omitempty"`
	// ReasoningLevels 是该模型自己声明的档位词汇表（排序后）。
	//
	// ★ 为什么每个模型都要报，而不只是**已被 agent 引用**的那些：
	//
	//	配置编辑器的「新建 agent」流程要先知道每个候选模型的词汇，
	//	才能当场说清「这个档位不存在」而不是写完才知道。
	//	只报已引用的模型，编辑器就对新模型完全失明 ——
	//	而那正是它最需要出提示的时刻。
	ReasoningLevels []string `json:"reasoningLevels,omitempty"`
}

// SkillView is one discovered skill.
//
// ★ description 是**模型选不选它**的唯一依据 —— 也是用户「查看」时最想
//
//	看的那一行。所以清单必须带上它，只有 name 是不够的。
type SkillView struct {
	Name string `json:"name"`
	// Description 是这个 skill 干什么的（frontmatter 的 description）。
	Description string `json:"description,omitempty"`
	// Context 是执行模式：inline / fork / fork_with_context。
	Context string `json:"context,omitempty"`
	// Agent 是 fork 模式要用的 agent 名（空 = 当前 agent）。
	Agent string `json:"agent,omitempty"`
	// Model 是这个 skill 覆盖的模型（空 = 不覆盖）。
	Model string `json:"model,omitempty"`
}

// skillList is the wire shape of GET /v2/skills.
type skillList struct {
	Skills []SkillView `json:"skills"`
}

// v2Skills 列出这一代挂着的 skill。
//
// ★ 与 GET /v2/config 里的 skills 同一份数据 —— 两处各扫一遍目录，
//
//	清单就会在「配置变了」与「目录变了」之间各说各话。
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
}

type configView struct {
	Version  string   `json:"version"`
	Deferred []string `json:"deferred"`
	// DefaultAgent 是新会话默认用的那个 agent。
	//
	// ★ 不是「主 agent」：每个 agent 都能当会话的 agent，这只是那个
	//   「没指定时用哪个」的约定。
	DefaultAgent string         `json:"defaultAgent"`
	MaxDepth     int            `json:"maxDepth"`
	Policy       policyView     `json:"policy"`
	Tools        []toolView     `json:"tools"`
	Agents       []agentView    `json:"agents"`
	Providers    []providerView `json:"providers"`
	Skills       []SkillView    `json:"skills"`
	Server       serverView     `json:"server"`
}

func (s *Server) v2Config(_ context.Context, c *app.RequestContext) {
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}

	out := configView{
		Version:      cfg.Version,
		Deferred:     cfg.Deferred(),
		DefaultAgent: cfg.DefaultAgent(),
		MaxDepth:     cfg.MaxDepth(),
		Policy: policyView{
			Ask:  nonNil(cfg.Policy.Ask),
			Deny: nonNil(cfg.Policy.Deny),
			// ★ 兜底档就是 allow —— 策略只剩 ask / deny 两列，
			//   没点名的工具一律放行（与 decl.Policy.TierOf 同一套语义）。
			//   这里报 ask 会让诊断端点说的与实际行为相反。
			DefaultTier: string(decl.TierAllow),
		},
		Tools:  s.toolTiers(),
		Agents: s.agentViews(cfg, false),
		Server: serverView{
			StartTimeout: cfg.Server.StartTimeout.Duration().String(),
			// ★ 报**生效值**（全局基线与 policy/limits.yaml 的收紧取更严的），
			//   不是 server/spec.yaml 里的原始值 —— 否则界面上看到的数字
			//   与实际生效的不是同一个，那正是「配了不生效」的温床。
			MaxRetries:    cfg.MaxRetries(),
			MaxDepth:      cfg.MaxDepth(),
			WatchEnabled:  cfg.Server.Watch.Enabled == nil || *cfg.Server.Watch.Enabled,
			WatchDebounce: cfg.Server.Watch.Debounce.Duration().String(),
		},
	}

	for _, id := range sortedKeys(cfg.Providers) {
		p := cfg.Providers[id]
		pv := providerView{
			ID: p.ID,
			// hasApiKey is a boolean on purpose: the key itself must never
			// leave the process.
			HasAPIKey: p.APIKey != "",
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
			// 排序后输出：map 的遍历顺序随机，而前端会把这份清单显示成下拉，
			// 顺序逐次不同会让每次刷新都在跳。
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

// toolTiers 报策略视图的三档。
//
// ★ 同样走注入的 [ToolsSource]：名字的并集要收到**所有** agent 的
//
//	disabled_tools（那些工具确实存在，清单上必须有），但档位一律按
//	**空表**判 —— 策略视图里不许出现任何 agent 分层。
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

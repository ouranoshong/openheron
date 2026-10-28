package build

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec/decl"
)

// 本文件是 skill 侧的**框架适配**：eino 只给 [skill.TypedModelHub] 与
// [skill.TypedAgentHub] 两个接口，不给实现 —— SKILL.md frontmatter 的
// `model:` 与 `agent:` 得由 openheron 自己解析。
//
// ★ 「怎么接 skill」（中间件、目录探测）在 [skill.go]；这里是「eino 要的那
// 两个接口怎么实现」。
//
// ★ 两个 hub 必须**都**挂上：在 inline 模式下缺 ModelHub 会让 `model:` 被
// **静默忽略**，在 fork 模式下缺 AgentHub 会硬报错。
// skillHubs 是注入 skill 中间件的一组实现。
type skillHubs struct {
	agent *skillAgentHub
	model *skillModelHub
}

// newSkillHubs 造一组 hub。
//
// root 由 [Builder.buildNode] 在 handlers 装配完成后回填（hub 与 handlers
// 互相引用：handlers 里包含 skill 中间件，而 root 的克隆需要完整的 handlers）。
//
// ★ 两个 hub 都绑定到**传入的快照**，不是「当前全局配置」：一个 agent 实例
//
//	属于哪一代，它的 skill 模型就必须来自同一代。
func (b *Builder) newSkillHubs(snap *snapshot, base deep.Config,
	specs []decl.SubAgentSpec, self string) *skillHubs {

	models := snap.models
	if models == nil {
		models = defaultModelFactory
	}
	return &skillHubs{
		agent: &skillAgentHub{
			builder:  b,
			snap:     snap,
			self:     self,
			base:     base,
			policy:   snap.agentSpec.policy,
			maxDepth: snap.agentSpec.maxDepth,
			specs:    collectSpecs(specs),
		},
		model: &skillModelHub{resolve: func(ctx context.Context, ref string) (
			model.BaseModel[*schema.Message], error) {

			return models(ctx, snap.cfg, ref, CallIntent{})
		}},
	}
}

// collectSpecs 把规格树摊平成 name → agentSpec。
//
// ★ 摊平而不是每次递归查：fork 发生在运行期，那时拓扑已经定型；从一个
//
//	map 里查不到就是「这个名字不在这个 agent 的子树里」，可以直接报出可用
//	的名字 —— 比递归走到某个分支才发现没有要清楚得多。
func collectSpecs(subs []decl.SubAgentSpec) map[string]decl.SubAgentSpec {
	out := make(map[string]decl.SubAgentSpec, len(subs))
	var walk func([]decl.SubAgentSpec)
	walk = func(list []decl.SubAgentSpec) {
		for _, s := range list {
			out[s.ID] = s
			walk(s.Sub)
		}
	}
	walk(subs)
	return out
}

// ── ModelHub ─────────────────────────────────────────────────────────────

// skillModelHub 实现 eino 的 [skill.TypedModelHub] —— 只给接口，实现是
// openheron 自己的（eino 不提供）。
type skillModelHub struct {
	resolve func(ctx context.Context, ref string) (model.BaseModel[*schema.Message], error)

	mu    sync.Mutex
	cache map[string]model.BaseModel[*schema.Message]
}

// Get 按 frontmatter 里的原字符串解析模型。
//
// 缓存按 hub 实例（= 一个配置代际）拆：热更新换了 llm/*.yaml 之后代际 +1，
// 新 hub 自然重新解析 —— 共享进程级缓存会让「改了模型不生效」再出现一次。
func (h *skillModelHub) Get(ctx context.Context, name string) (model.BaseModel[*schema.Message], error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("skill declares an empty model")
	}
	if h.resolve == nil {
		return nil, fmt.Errorf("no model resolver is wired, cannot resolve %q", name)
	}

	h.mu.Lock()
	if m, ok := h.cache[name]; ok {
		h.mu.Unlock()
		return m, nil
	}
	h.mu.Unlock()

	m, err := h.resolve(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolve model %q: %w", name, err)
	}
	if m == nil {
		return nil, fmt.Errorf("resolve model %q: resolver returned nil", name)
	}

	h.mu.Lock()
	if h.cache == nil {
		h.cache = map[string]model.BaseModel[*schema.Message]{}
	}
	h.cache[name] = m
	h.mu.Unlock()
	return m, nil
}

// ── AgentHub ─────────────────────────────────────────────────────────────

// skillAgentHub 实现 eino 的 [skill.TypedAgentHub] —— 只给接口，实现是
// openheron 自己的（eino 不提供）。
type skillAgentHub struct {
	// builder 与 snap 是取「当前根实例」的途径。
	//
	// ★ 走 Builder 而不是直接摸池：代际号属于快照（池的键含它），
	//   池自己不再记 —— 于是「换代」这件事只有一个来源。
	builder *Builder
	snap    *snapshot
	// self 是当前 agent 的名字 —— 空名字（frontmatter 没写 agent:）落到它。
	self string
	// base 是「去掉名字与拓扑」的模板：子 agent 与 fork agent 都从它克隆。
	base deep.Config
	// root 是完整的根配置（含 handlers 与 subagents），由 buildOnce 回填。
	root     deep.Config
	policy   Policy
	maxDepth int
	specs    map[string]decl.SubAgentSpec
}

// bindRoot 回填根配置。必须在 deep.New 之前调用。
func (h *skillAgentHub) bindRoot(cfg deep.Config) { h.root = cfg }

// Get 返回 fork 模式要跑的 agent。
//
// 名字为空 ⇒ 当前 agent（eino 明写：空名字应返回默认 agent）。
// 有名字 ⇒ 本 agent 子树里声明过的那个，按子 agent 的中间件链重新构造 ——
// 不缓存，因为 skill 的 `model:` 可能给每个调用换模型，而实例的模型是构造期
// 固定的。
func (h *skillAgentHub) Get(ctx context.Context, name string,
	opts *skill.TypedAgentHubOptions[*schema.Message]) (adk.TypedAgent[*schema.Message], error) {

	name = strings.TrimSpace(name)
	var overridden model.BaseModel[*schema.Message]
	if opts != nil {
		overridden = opts.Model
	}

	if name == "" || name == h.self {
		if overridden != nil {
			// skill 写了 `model:` ⇒ 同一个 agent，换一个模型实例。
			// 底板是**已装配好的根**（含 handler 链与子 agent），只换模型。
			cfg := h.root
			cfg.ChatModel = overridden
			a, err := buildAgent(ctx, cfg)
			if err != nil {
				return nil, fmt.Errorf("build fork of %q with the skill's model: %w", h.self, err)
			}
			return a, nil
		}
		// ★ 「当前入口」不一定是默认 agent：以别的 agent 为入口时，空名字 /
		//   同名都必须回到**那个** agent，而不是悄悄切回默认 agent。
		target := h.self
		if name != "" {
			target = name
		}
		if h.builder == nil || h.snap == nil {
			return nil, fmt.Errorf("no builder is wired for skill agent %q", target)
		}
		// ★ 规格由**名字**自己找（[Builder.pooledRoot]）—— 不由这里传：
		//   传进来的规格与名字可能对不上（那时装配的是另一个 agent）。
		e, err := h.builder.pooledRoot(ctx, h.snap, target)
		if err != nil {
			return nil, err
		}
		return e.agent, nil
	}

	agentSpec, ok := h.specs[name]
	if !ok {
		return nil, fmt.Errorf("agent %q is not declared under %q (available: %s)",
			name, h.self, strings.Join(h.agentNames(), ", "))
	}

	// ★ 与树里的子 agent **同一套**装配（[Builder.buildNode]）：先过滤、
	//   再闸门、最后深度守卫。否则 fork 出来的 agent 能绕过审批 ——
	//   而审批是 ask 档唯一的执行点。
	//
	// ★ skill 同时写了 agent: 与 model: —— 框架已经用 ModelHub 解析过，
	//   直接把它作为显式覆盖；否则按 spec.ModelRef 解析（为空回落底板）。
	if h.builder == nil || h.snap == nil {
		return nil, fmt.Errorf("no builder is wired for skill agent %q", name)
	}
	base := h.base
	base.WithoutGeneralSubAgent = true

	a, _, err := h.builder.buildNode(ctx, h.snap, node{
		base:      base,
		agentSpec: &agentSpec,
		table:     specTable(h.policy, agentSpec),
		depth:     0,
		model:     overridden,
	})
	if err != nil {
		return nil, fmt.Errorf("build skill fork agent %q: %w", name, err)
	}
	return a, nil
}

// agentNames 给错误信息用：列出这个 agent 真的能 fork 的名字。
func (h *skillAgentHub) agentNames() []string {
	out := make([]string, 0, len(h.specs))
	for id := range h.specs {
		out = append(out, id)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

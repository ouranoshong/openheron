package build

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/obs"
	"openheron/agentkit/spec/decl"
	"openheron/helpers"
)

// 本文件是唯一的装配点：deep.Config 只在这里构造，deep.New 只在这里调用。
// 根与子 agent 的差别收进 [node] 的参数，装配步骤只写一遍 —— 漏一处的后果
// 是某个 agent 静默少一半文件工具，且不报错。

// baseConfig 把派生规格翻成 eino 的装配结构。
// 它是唯一构造 deep.Config 的地方 —— 让配置层直接造它，等于把装配顺序的
// 责任分给调用方。
func (b *Builder) baseConfig(snap *snapshot, root decl.SubAgentSpec) deep.Config {
	cfg := deep.Config{
		Name:                   root.ID,
		Description:            root.Description,
		Instruction:            root.Instruction,
		MaxIteration:           root.MaxIter,
		WithoutGeneralSubAgent: true, // ★ R3：我们自己声明子 agent，不开内置那个
	}
	// ★ 重试配置在这里落地：它来自配置（server/spec.yaml 的全局基线与
	//   policy/limits.yaml 的收紧取更严的那个），而**装配**归这里。
	helpers.ApplyModelRetry(&cfg, snap.cfg.MaxRetries())
	return cfg
}

// applyToolConfig 把 tool 能力接进 deep.Config。
//
// ★ 必须在这里：deep.New 只在 Backend / Shell / StreamingShell 至少一个
// 非 nil 时才注册文件系统中间件，而它才是 read_file / write_file /
// execute 的来源。漏接是静默的 —— 模型只会拿到「[tool unavailable]」。
func applyToolConfig(cfg *deep.Config, h heavyDeps) {
	cfg.Backend = h.backend
	cfg.StreamingShell = h.streamingShell
	// ★ 直接注册的工具来自注册器（知识库、将来的 MCP 等）。
	//	装配层不知道它们从哪来，只负责挂上。
	cfg.ToolsConfig.Tools = append(cfg.ToolsConfig.Tools, h.tools...)
	cfg.ToolsConfig = withUnknownToolsReply(cfg.ToolsConfig)
	// ★ EmitInternalEvents：让**子 agent 的产出冒到父的事件流**。
	//
	//	关着的时候，子 agent 是父的一个普通工具调用，它的中间过程
	//	（推理、工具调用、原始工具返回）**在工具内部**跑完 —— 界面看不到、
	//	记录里也没有，事后只能靠它自己写的那份总结。
	//
	//	打开后，eino 把子 agent 的事件转发到父的 AsyncGenerator
	//	（eino v0.9.21 的 adk/chatmodel.go：withAgentToolEventGenerator），
	//	于是：
	//	  · 界面能实时看到专家的思考与工具调用（带 subagentRunId 归属）；
	//	  · 归档里也有（可观测、可按 agent 分账）。
	//
	//	★ 隔离由**下一轮建上下文时过滤**保证（agentkit/run 的 buildInput 剔掉带
	//	  `openheron_subagent` 标记的消息），不是靠「不记录」——
	//	  eino 那句「NOT recorded in the parent agent's runSession」说的是
	//	  eino 自己的持久化，而 openheron 自己归档（sess.Append），不滤就会把
	//	  专家的原始工具输出灌回父的上下文。
	cfg.ToolsConfig.EmitInternalEvents = true
}

// buildAgent 是包内**唯一**调 [deep.New] 的地方。
//
// ★ 它是**变量**而不是函数：测试要数「真构造了几个 agent」、要看每个 agent
// 挂了什么链，而 deep.New 返回的实例不暴露这些。
//
// ★ 调用方必须已经把身份 / 模型 / handlers / 子 agent 写进 cfg —— 这里不再
// 做第二遍装配。这一条是 2026-10-21 「backend 漏接」那类回归的结论：同一项
// 装配只能有一个产地。
var buildAgent = func(ctx context.Context, cfg deep.Config) (adk.TypedAgent[*schema.Message], error) {
	return deep.New(ctx, &cfg)
}

// node 是递归装配的一层。
type node struct {
	base deep.Config
	// 这一层的声明。入口传它自己的 agentSpec，下属传自己的 agentSpec；
	// nil 只留给「fork 自己」的克隆路径。
	agentSpec *decl.SubAgentSpec
	// table 只对入口有意义：入口的边界由「会话选了哪个 agent」决定。
	table *Table
	// 入口是第 0 层；下属从 1 开始。
	depth int
	root  bool
	// backend 只给根用（skill 中间件需要它）。子 agent 从 base 继承能力。
	backend fsapi.Backend
	// 显式覆盖（skill fork 用 opts.Model）；nil ⇒ 按 root/sub 的规则解析。
	model model.BaseModel[*schema.Message]
}

// buildNode 装配一个节点及其子树。
//
// ★ 递归只覆盖「一个节点 + 它的子树」。入口的三件事不能下沉：
//
//	heavyDepsOf（工具注册器只跑一次）、skill 中间件 + bindRoot、池。
//
// ★ 每个 child 都用**同一个 base**：能力继承入口，模型回落也落到入口模型，
// 不继承父自己的 ModelRef（与今天 buildSubAgents 的行为一致）。
func (b *Builder) buildNode(ctx context.Context, snap *snapshot, n node) (
	adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error) {

	cfg := n.base
	sp := n.agentSpec

	if sp != nil {
		cfg.Name = sp.ID
		cfg.Description = sp.Description
		cfg.Instruction = sp.Instruction
		if sp.MaxIter > 0 {
			cfg.MaxIteration = sp.MaxIter
		}
	}

	// ① 模型：显式覆盖 > 入口已解析的实例 > 下属自己的引用。
	if n.model != nil {
		cfg.ChatModel = n.model
	} else if !n.root && sp != nil {
		mdl, err := resolveModel(ctx, snap, *sp, cfg.ChatModel)
		if err != nil {
			return nil, nil, err
		}
		cfg.ChatModel = mdl
	}
	if cfg.ChatModel == nil {
		return nil, nil, fmt.Errorf("agent %q has no model", cfg.Name)
	}

	// ② 先递归子树：只有 depth < maxDepth 才继续。
	var children []adk.TypedAgent[*schema.Message]
	if sp != nil && n.depth < snap.agentSpec.maxDepth {
		children = make([]adk.TypedAgent[*schema.Message], 0, len(sp.Sub))
		for i := range sp.Sub {
			childSpec := sp.Sub[i]
			child, _, err := b.buildNode(ctx, snap, node{
				base:      n.base,
				agentSpec: &childSpec,
				depth:     n.depth + 1,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("build sub-agent %q: %w", childSpec.ID, err)
			}
			children = append(children, child)
		}
	}
	cfg.SubAgents = children

	// ③ handlers：入口 = skill 链 + toolChain；下属 = toolChain。
	if n.root {
		// ★ 当前入口是谁：不是默认 agent，而是这次正在装配的那一个
		//   （baseConfig 已经把身份写进 cfg 了）。
		self := ""
		if sp != nil {
			self = sp.ID
		}
		// ★ skill fork 要在**当前入口的树**里找下属 —— 跨树找会「找到一个
		//   不在我团队里的人」。
		hubs := b.newSkillHubs(snap, n.base, snap.agentSpec.subsOf(self), self)
		handlers, err := b.skillHandlers(ctx, n.backend, hubs)
		if err != nil {
			return nil, nil, err
		}
		// ★ 边界是一个**整体**：审批三档与工具表必须来自同一处，
		//   否则「审批按 main、过滤按 task」这种错配能编过、只在运行期出事。
		boundary := newBoundary(snap.agentSpec.policy, n.table)
		cfg.Handlers = append(handlers, toolChain(boundary, snap.agentSpec.maxDepth)...)
		// ★ 回填完整的根配置：skill 同时写了 agent: 与 model: 时要克隆一份换模型。
		hubs.agent.bindRoot(cfg)
	} else if sp != nil {
		cfg.Handlers = subHandlers(snap.agentSpec.policy, *sp, snap.agentSpec.maxDepth)
	}

	// ④ buildAgent → deep.New（包内唯一调用点）。
	agent, err := buildAgent(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return agent, cfg.ChatModel, nil
}

// assemble 把一个 agent 装配出来 —— 它是这次运行的**入口**。
//
// ★ 入口是谁由**会话**决定（state 里记着那个 agent id）：装配按它自己的
//
//	声明来 —— 它的正文、模型、工具边界，以及它声明的那几个下属。
//	没有「主 agent」这回事：每个 agent 都是同一格式的同一个东西。
//
// ★ 装配的顺序是有讲究的（eino quickstart 第四、五、九章）：
//
//	① 重依赖：backend + 工具集（按代际一次）
//	② baseConfig（唯一构造点）+ applyToolConfig（工具侧接线）
//	③ 入口的模型：按它自己的 ModelRef 解析（不回落 —— 回落会让
//	   「按它的工具表、用别人的模型」这种混合运行悄悄发生）
//	④ base = 去掉 handlers / 子 agent / 身份的 cfg —— 下属继承的是它
//	⑤ buildNode 递归装配子树（每个下属自己挂审批与过滤）
func (b *Builder) assemble(ctx context.Context, snap *snapshot,
	entry *decl.SubAgentSpec, table *Table) (
	adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error) {

	heavy, err := snap.heavyDepsOf(ctx)
	if err != nil {
		return nil, nil, err
	}
	if entry == nil {
		return nil, nil, errors.New("agent: no entry agent to assemble")
	}
	entrySp := *entry

	cfg := b.baseConfig(snap, entrySp)
	// ★ ① tool 侧接线。唯一做这件事的地方是 [applyToolConfig]。
	applyToolConfig(&cfg, heavy)

	// ★ ② 入口的模型：按它自己的 ModelRef 解析。
	mdl, err := resolveModel(ctx, snap, entrySp, nil)
	if err != nil {
		return nil, nil, err
	}
	cfg.ChatModel = mdl

	// ★ ③ base：下属继承的底板 —— 去掉 handlers、子 agent 与身份。
	base := cfg
	base.Handlers = nil
	base.SubAgents = nil
	base.Name, base.Description, base.Instruction = "", "", ""

	agent, mdl, err := b.buildNode(ctx, snap, node{
		base:      base,
		agentSpec: &entrySp,
		table:     table,
		depth:     0,
		root:      true,
		backend:   heavy.backend,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build agent %q: %w", entrySp.ID, err)
	}
	return agent, mdl, nil
}

// skillHandlers 造 skill 侧中间件。没有 skills 目录不是错误；目录配了却
// 不存在时由 [resolveSkillsDir] 明确报错并降级。
func (b *Builder) skillHandlers(ctx context.Context, backend fsapi.Backend, hubs *skillHubs) (
	[]adk.TypedChatModelAgentMiddleware[*schema.Message], error) {

	mw, err := skillChain(ctx, backend, hubs)
	if err != nil {
		// ★ skill 不可用**不阻断装配**：它是个附加能力，而一个配错的 skills
		//   目录不该让整个 agent 起不来。降级要出声 —— 这也是本仓库对
		//   deferred 的既有做法。
		obs.Logger().Warn("[agent] skill middleware disabled", "err", err)
		return nil, nil
	}
	if mw == nil {
		return nil, nil
	}
	return []adk.TypedChatModelAgentMiddleware[*schema.Message]{mw}, nil
}

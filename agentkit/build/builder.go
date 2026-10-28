// Package agent builds the DeepAgent that agentkit/run drives.
//
// It imports agentkit/run for nothing: the only contract it satisfies is
// AgentBuilder, whose turn kind comes from the contract leaf package.
//
// ★ 装配分块，各归各的文件：
//
//	manager.go   配置管理器接口 + 模型工厂缝
//	spec.go      配置 → agent 包内的规格（派生）
//	tools.go     工具注册器：这一代有哪些直接注册的工具
//	builder.go   骨架：Builder、Option、Build、池
//	snapshot.go  哪一代配置：不可变快照 + 重依赖懒建
//	assemble.go  ★ **唯一装配点**：eino 的装配结构只在这里构造
//	policy.go    「谁能调什么」（数据）：Tier / Policy / Table / Boundary
//	gate.go      「谁能调什么」（机制）：过滤中间件 + 闸门中间件
//	topology.go  「谁能委派给谁」：深度守卫 + 子 agent 中间件链
//	skill.go     skill 侧：skill 中间件 + 目录探测
//	hub.go       skill 侧的框架适配：ModelHub / AgentHub（eino 只给接口）
//
// ★ tool 与 skill 的区别见 eino 官方 quickstart 第九章：tool 是动作/能力，
//
//	skill 是可复用的知识/指令包 —— 后者是**中间件**，不是工具。
package build

import (
	"context"
	"errors"
	"fmt"
	"openheron/contract"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Builder satisfies agentkit/run.AgentBuilder.
//
// ★ 状态只有一处：[snapshot]。热更新时整个换掉，而不是逐字段改。
//
// ★ **Builder 不持有配置，也没有推送式换绑**：配置变更由管理器体现，
//
//	下一次 Build 问它就行 —— 「忘了通知 builder」这类静默失效在结构上
//	不可能发生（P7）。
type Builder struct {
	// mgr 提供当前这一代配置。
	mgr ConfigManager
	// pool 复用已编好的实例。
	pool *pool
	// projectRoot / dataDir 是运行期路径（接线）。
	projectRoot, dataDir string
	// models 是「引用 + 调用意图 → 模型实例」的注入点。
	//
	// ★ 生产永远用 [llm.BuildModel]；只有**包内测试**会换成桩模型
	//   （见 §7.2）。它不是 Option —— 公开面不该出现「怎么造模型」。
	models modelFactory
	// tools 是工具注册器：负责「这一代有哪些直接注册的工具」。
	//
	// ★ 它不负责权限（Table/Policy 负责），也不管 filesystem / skill /
	//   task 这些由中间件或框架提供的工具。
	tools *toolRegistry
	// mu 守 snap（当前这一代的快照）。
	mu   sync.Mutex
	snap *snapshot
}

// Option 装配 Builder 的可选部分。
//
// ★ Option 只负责「接线」，不参与任何推导：推导只发生在 [newSnapshot]。
type Option func(*Builder)

// WithPaths 给运行期路径（知识库语料与索引的位置）。
//
// ★ 它们**不是配置**：项目根可以由 PROJECT_ROOT 覆盖，状态目录由
//
//	server/data_dir 解析而来 —— 都是「这次进程跑在哪」的答案，
//	不是「用户写了什么」。
func WithPaths(projectRoot, dataDir string) Option {
	return func(b *Builder) {
		b.projectRoot = projectRoot
		b.dataDir = dataDir
	}
}

// NewBuilder 造 Builder。
//
// ★ 它**不做任何 agent 项的初始化** —— 它连根的名字都不知道（那要问管理器）。
func NewBuilder(mgr ConfigManager, opts ...Option) *Builder {
	b := &Builder{
		mgr:    mgr,
		pool:   newPool(),
		models: defaultModelFactory,
		tools:  defaultToolRegistry(),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// currentSnapshot 返回管理器**当前这一代**的快照；没有就派生一代。
//
// ★ 自省端点（`/v2/agents`）会在第一次 Build 之前被调用。那时 `b.snap`
//
//	还是 nil，但 agentSpec / policy / table 的派生**没有 I/O** —— 让端点自己
//	触发一次派生，好过在 UI 首屏报「一个工具都没有」。
//
// ★ 已有快照时也必须问一次管理器：自省读口不能停在上一代。否则热更新后
// 第一次 Build 之前，`/v2/agents/{id}` 的 effectiveTools 与 `/v2/config`
// 的 tools 会继续报旧 policy / 旧 disabled_tools —— 看起来就是「配置没
// 更新」，而运行期下一次 Build 其实已经会用新配置。快照的派生没有 I/O，
//
//	这次检查只多一次指针比较与（换代时）一次派生。
//
// ★ 它仍然不建模型 / backend / 工具集：那些是 heavyDeps，只在 Build 里懒建。
func (b *Builder) currentSnapshot() *snapshot {
	if b.mgr == nil {
		return nil
	}
	cfg, gen := b.mgr.Current()
	s, err := b.snapshotFor(cfg, gen)
	if err != nil {
		return nil
	}
	return s
}

// Stats 返回池的可观测计数（启动巡检打印用）。
func (b *Builder) Stats() (hits, misses uint64, size int, gen uint64) {
	return b.pool.Stats()
}

// Build returns the agent for this turn.
//
// ★ 它做两件事：问管理器要**当前这一代**，然后取池（命中复用，未命中真建）。
//
//	agentkit/run 看到的签名与语义都没变 —— 池与代际都是内部实现。
//
// ★ agentID 是会话选定的 agent（storage.Session.AgentID 原样传入）：
//
//	装配**按它自己的声明**来 —— 它的正文、模型、工具边界，以及它声明的
//	那��下属。选到谁就是谁，没有「主/子」之分。
//
// ★ 空串是**防御**，不是常态：会话的 agent 在建会话时就落盘了
//
//	（state.json 的 agentId），所以正常路径上它一定是个具体名字。这里仍
//	然兜一次，是因为这个方法对外是公开接口 —— 别的调用方未必经过会话层。
func (b *Builder) Build(ctx context.Context, _ string, agentID string,
	_ contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {

	if b.mgr == nil {
		return nil, errors.New("agent: no config manager is wired")
	}
	cfg, gen := b.mgr.Current()
	snap, err := b.snapshotFor(cfg, gen)
	if err != nil {
		return nil, err
	}

	// ★ 会话选的那个 agent 就是这次运行的入口。空串 ⇒ 默认 agent。
	name := strings.TrimSpace(agentID)
	if name == "" {
		name = snap.agentSpec.defaultAgent
	}
	if _, ok := snap.agentSpec.agentSpec(name); !ok {
		return nil, fmt.Errorf("%w: %q", errAgentNotDeclared, name)
	}
	e, err := b.pooledRoot(ctx, snap, name)
	if err != nil {
		return nil, err
	}
	return e.agent, nil
}

// pooledRoot 取（或建）一个「作为入口跑」的 agent 实例。
//
// ★ **规格由名字自己找**（[spec.agentSpec]），不由调用方传进来：
//
//	入口是谁，名字说了算。以前「规格由调用方给、nil 表示默认根」——
//	于是「当前入口不是默认根」时，入口自己拿到的会是**默认根的规格**
//	（装配错了对象，而池的键还是入口的名字，问题被藏住了）。
//
// ★ 构造函数当参数传，不在池里登记。
//
//	闭包每次现造，捕获的一定是当次的快照。
func (b *Builder) pooledRoot(ctx context.Context, snap *snapshot, name string) (*entry, error) {

	sp, ok := snap.agentSpec.agentSpec(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", errAgentNotDeclared, name)
	}
	// ★ 入口的工具边界用它自己的 disabled_tools。
	//
	//	与自省端点同一个构造函数（[specTable]）—— 两边各写一遍的话，
	//	改动 deny 优先级只会生效一边。
	// ★ 每个 agent 的工具边界**不同**（各自的 disabled_tools）—— 所以表按 agent 算。
	//
	//   取的是**缓存的那张**（snapshot.tables），不是现造的：运行期观测
	//   到的工具名记在表实例上，装配与自省必须是同一批实例。
	table := snap.tableFor(name)
	if table == nil {
		return nil, fmt.Errorf("agent: %q has no tool table", name)
	}
	return b.pool.GetOrBuild(ctx, name, snap.gen,
		func(ctx context.Context) (adk.TypedAgent[*schema.Message],
			model.BaseModel[*schema.Message], error) {
			return b.assemble(ctx, snap, &sp, table)
		})
}

// errAgentNotDeclared 表示这个 agent 没有声明。
//
// ★ 声明了的都能当会话入口 —— 装配按它自己的配置来（它自己的正文、
//
//	模型、工具边界，以及它声明的那几个下属）。所以「不能当入口」这件事
//	不存在，只有「没声明」这一种拒绝。
var errAgentNotDeclared = errors.New("agent is not declared")

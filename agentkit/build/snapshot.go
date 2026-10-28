package build

import (
	"context"
	"fmt"
	"strings"
	"sync"

	localbk "github.com/cloudwego/eino-ext/adk/backend/local"
	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/llm/common"
)

// 本文件是「**现在跑的是哪一份配置**」：一份不可变的代际快照。
//
// ★ 装配链与自省端点共用它，所以 agentSpec / table / models / tools 必然同代；
// 重依赖（模型 + backend + 工具集）也挂在这里，按代际懒建一次。

// snapshot 是一代配置的**全部**内容。
type snapshot struct {
	// gen 是这一代的号。池的键含它 —— 于是「换代」这件事只有一个来源。
	gen uint64

	// cfg 是这一代的**原始配置**（重试上限、知识库配置、限制定义都在里面）。
	cfg *spec.Config
	// projectRoot / dataDir 是工具 provider 需要的运行期路径。
	projectRoot, dataDir string
	// agentSpec 是从 cfg 派生出来的装配输入。
	agentSpec agentSpec
	// tables 是每个 agent 的工具边界（各自的 disabled_tools）。
	//
	// ★ 必须是**同一批实例**被装配与自省共用：运行期观测到的工具名记在
	//   表实例上（Table.Observed），而自省端点要读的正是那些观测。
	//   每次新建一张表 ⇒ 装配用的表与自省读的表不是同一个 ⇒ 观测全丢，
	//   而现象是「什么都没观察到」—— 静默的。
	tables map[string]*Table
	// models 是这一代用的模型工厂（生产 = BuildModel；测试 = 桩）。
	models modelFactory
	// tools 是这一代用的工具注册器。
	tools *toolRegistry
	// 重依赖按代际懒建一次。快照从不被复制，所以 sync.Once 可以直接内嵌。
	heavyOnce sync.Once
	heavy     heavyDeps
	heavyErr  error
}

// heavyDeps 是一代配置的运行时产物：构造有 I/O，同一代内共用。
type heavyDeps struct {
	// backend 提供 ls / read_file / write_file / edit_file / glob / grep。
	backend fsapi.Backend
	// streamingShell 提供流式 execute。
	//
	// ★ 它与 backend 常常是**同一个** local 实例，但 deep.Config 用
	//   Backend 与 StreamingShell 两个字段分别接收，所以这里也分开存 ——
	//   合并成一个字段会让装配时不得不猜「它到底实现了哪个接口」。
	streamingShell fsapi.StreamingShell
	// tools 是注册器产出的直接工具集合（知识库、将来的 MCP 等）。
	//
	// ★ 它是**系统能力**，不是某个 agent 的声明：每个 agent 都从底板继承，
	//   所以按代际构建一次。知识库/注册器配置热更新 ⇒ 代际 +1 ⇒ 随快照重建。
	tools []tool.BaseTool
}

// heavyDepsOf 按代际支付一次重依赖的代价。
//
// ★ 懒建：换配置不该因为厂商端点配错而失败，错误留到下一次装配。
// [sync.Once] 随快照换代，所以同一代只建一次、换代必重建。
func (s *snapshot) heavyDepsOf(ctx context.Context) (heavyDeps, error) {
	s.heavyOnce.Do(func() {
		s.heavy, s.heavyErr = s.buildHeavy(ctx)
	})
	return s.heavy, s.heavyErr
}

// buildHeavy 造这一代的模型、文件后端与工具集。
func (s *snapshot) buildHeavy(ctx context.Context) (heavyDeps, error) {
	var out heavyDeps

	local, err := localbk.NewBackend(ctx, &localbk.Config{})
	if err != nil {
		return out, fmt.Errorf("build filesystem backend: %w", err)
	}
	out.backend = local
	out.streamingShell = local

	tools, err := s.tools.Build(ctx, toolEnv{
		cfg:         s.cfg,
		projectRoot: s.projectRoot,
		dataDir:     s.dataDir,
	})
	if err != nil {
		return out, err
	}
	out.tools = tools
	return out, nil
}

// newSnapshot 造一代配置快照 —— 所有从配置推导出来的东西都在这里，
// 启动与热更新共用同一条路径。
func newSnapshot(gen uint64, cfg *spec.Config, projectRoot, dataDir string,
	models modelFactory, tools *toolRegistry) (*snapshot, error) {

	sp, err := specOf(cfg, projectRoot)
	if err != nil {
		return nil, err
	}
	if sp.maxDepth <= 0 {
		sp.maxDepth = decl.DefaultMaxDepth
	}
	if models == nil {
		models = defaultModelFactory
	}
	if tools == nil {
		tools = defaultToolRegistry()
	}
	tables := tablesOf(sp)
	return &snapshot{
		gen:         gen,
		cfg:         cfg,
		projectRoot: projectRoot,
		dataDir:     dataDir,
		agentSpec:   sp,
		tables:      tables,
		models:      models,
		tools:       tools,
	}, nil
}

// snapshotFor 拿到这一代的快照。
//
// ★ 代际只允许**向前**推进：一个先读到旧代际的 Build 晚到，绝不能把
// b.snap 覆盖回旧代，否则接口报新代、装配用旧代。旧请求直接复用新快照。
func (b *Builder) snapshotFor(cfg *spec.Config, gen uint64) (*snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.snap != nil && b.snap.gen >= gen {
		return b.snap, nil
	}
	snap, err := newSnapshot(gen, cfg, b.projectRoot, b.dataDir, b.models, b.tools)
	if err != nil {
		return nil, err
	}
	b.snap = snap
	return snap, nil
}

// resolveModel 按 ModelRef 造实例；base 非 nil 时，引用为空即回落到它。
//
// ★ 两条规则刻意不对称（§5 的 P6）：**入口 agent**不回落到底板（回落到
//
//	别的 agent 会是「按它的工具表、用别人的模型」的混合运行），它没声明
//	模型时回落到**环境里的**那一份；**树里的下属 / skill fork** 在引用为空
//	时回落底板。
//
// ★ 它读的是**快照里的 cfg 与 factory**，不是「当前全局配置」：
// 一个 agent 实例属于哪一代，它的模型就必须来自同一代。
func resolveModel(ctx context.Context, snap *snapshot, sp decl.SubAgentSpec,
	base model.BaseModel[*schema.Message]) (model.BaseModel[*schema.Message], error) {

	ref := strings.TrimSpace(sp.ModelRef)
	if ref == "" {
		if base != nil {
			return base, nil
		}
		// ★ 入口 agent 没声明模型 ⇒ 环境兜底（不是别人的模型）。
		//   正常配置走不到这里：config 的校验要求每个 agent 都声明模型。
		return buildModel(ctx)
	}
	factory := snap.models
	if factory == nil {
		factory = defaultModelFactory
	}
	return factory(ctx, snap.cfg, ref, CallIntent{
		Reasoning:   sp.Reasoning,
		Temperature: sp.Temperature,
		MaxTokens:   sp.MaxTokens,
	})
}

// buildModel 按当前声明造一个厂商模型。
//
// ★ 它走 [ConfigFromEnv] 而不是各自 import 厂商包：端点方言属于配置，
//
//	代码里不出现任何一家的字段名。它只服务于「根没有 ModelRef」这一条
//	环境兜底路径；正常配置一定会走 resolveModel。
func buildModel(ctx context.Context) (model.BaseModel[*schema.Message], error) {
	mc, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	cm, err := common.New(ctx, mc)
	if err != nil {
		return nil, err
	}
	// 请求级参数（模型名 / 采样参数 / 端点方言）由调用方按 agent 注入，
	// 不在这里烘死 —— 一个实例要能服务同一端点下的多个模型。
	return common.AttachRequestOptions(cm, common.RequestOptions{
		Model:  mc.Model,
		Fields: mc.ExtraFields,
	}, "", ""), nil
}

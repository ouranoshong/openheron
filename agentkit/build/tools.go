package build

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"openheron/agentkit/obs"
	"openheron/agentkit/spec"
)

// 本文件是**工具从哪来**的注册器。
//
// ★ 它只负责按代际产出一个工具集：顺序、去重、保留名、失败策略。
//
//	它**不做权限** —— 某个 agent 能不能调，仍由 Table / Policy 决定
//	（见 docs/agent-design-v2.md §4.7 的「权限边界」）。
//
//	★ 「配置里启用的工具默认 allow 档」那条由 **config 层**给出
//	（[spec.Config.EnabledToolNames]）—— 注册器只管产出工具 ✅，
//	  「哪些工具算被启用了」是配置的事 ✅。
//
// ★ 三条注册通道不要混：
//
//	toolRegistry  → ToolsConfig.Tools（知识库、将来的 MCP / web search）
//	middleware    → 运行期 BeforeAgent 加进工具表（filesystem、skill）
//	built-in      → DeepAgent（task、write_todos）
//
//	把 middleware/built-in 的工具再注册一份，会出现「同一个工具被注册两次，
//	而两边都自洽」。

// toolEnv 是 provider 需要的一切运行期上下文。
//
// ★ 故意不含 agentID：注册器是「这一代系统有哪些工具」，不是「某个 agent 能用
//
//	哪些工具」。按 agent 裁剪只发生在 Table。
type toolEnv struct {
	cfg         *spec.Config
	projectRoot string
	dataDir     string
}

// toolProvider 是一类工具的来源。
//
// ★ provider 自己负责读配置、建客户端、决定「没配就返回空」。
//
//	注册器只负责顺序、去重与失败策略。
type toolProvider interface {
	ID() string
	// Optional 为 true：Provide 失败只降级 + 出声；false：让 Build 失败。
	//
	// ★ 它只覆盖 Provide 返回的错误。工具契约错误（空名 / Info 失败 /
	//	重名 / 保留名 / 不可执行）永远 fatal —— 那是代码缺陷，不是环境不可用。
	Optional() bool
	Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
}

// toolRegistry 是一代配置的工具来源清单。
type toolRegistry struct {
	providers []toolProvider
}

// defaultToolRegistry 是生产注册表。
//
// ★ 新增一种工具只改这里：实现一个 provider + 加一行。
//
//	不需要改 assemble / buildNode / applyToolConfig / main.go。
func defaultToolRegistry() *toolRegistry {
	return &toolRegistry{providers: []toolProvider{
		kbToolProvider{},
		// ★ 下面三个都是「连到外面去」的工具，**默认全关** ——
		//   它们的输出会原样进模型上下文（见 agent/nettools.go 的开篇）。
		httpRequestToolProvider{},
		webSearchToolProvider{},
		wikipediaToolProvider{},
		// mcpToolProvider{}, projectToolsProvider{}, ...
	}}
}

// reservedToolNames 是由 middleware / built-in 拥有的名字。注册器不能和它们抢。
var reservedToolNames = map[string]string{
	"ls": "filesystem middleware", "read_file": "filesystem middleware",
	"write_file": "filesystem middleware", "edit_file": "filesystem middleware",
	"glob": "filesystem middleware", "grep": "filesystem middleware",
	"execute": "filesystem middleware",
	"task":    "DeepAgent", "write_todos": "DeepAgent",
	"skill": "skill middleware",
}

// Build 按注册顺序产出一代工具集。
func (r *toolRegistry) Build(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	if r == nil {
		return nil, nil
	}
	seen := make(map[string]string)
	var out []tool.BaseTool

	for _, p := range r.providers {
		batch, err := p.Provide(ctx, env)
		if err != nil {
			if p.Optional() {
				obs.Logger().Warn("[agent] tool provider disabled", "provider", p.ID(), "err", err)
				continue
			}
			return nil, fmt.Errorf("tool provider %s: %w", p.ID(), err)
		}
		names, err := validateBatch(ctx, p.ID(), batch, seen)
		if err != nil {
			// 工具契约错误（Info/空名/重名/保留名/不可执行）永远 fatal。
			return nil, fmt.Errorf("tool provider %s: %w", p.ID(), err)
		}
		for _, n := range names {
			seen[n] = p.ID()
		}
		out = append(out, batch...)
	}
	return out, nil
}

// validateBatch 只校验、不写 seen：provider 必须整批成功才生效。
func validateBatch(ctx context.Context, providerID string,
	batch []tool.BaseTool, seen map[string]string) ([]string, error) {

	names := make([]string, 0, len(batch))
	local := make(map[string]bool, len(batch))
	for _, t := range batch {
		if t == nil {
			return nil, fmt.Errorf("nil tool")
		}
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool info: %w", err)
		}
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("tool info has an empty name")
		}
		if owner, ok := reservedToolNames[info.Name]; ok {
			return nil, fmt.Errorf("tool %q is owned by %s", info.Name, owner)
		}
		switch t.(type) {
		case tool.InvokableTool, tool.StreamableTool,
			tool.EnhancedInvokableTool, tool.EnhancedStreamableTool:
		default:
			return nil, fmt.Errorf("tool %q is metadata-only; it cannot be executed", info.Name)
		}
		if prev, ok := seen[info.Name]; ok {
			return nil, fmt.Errorf("duplicate tool %q from %s and %s",
				info.Name, prev, providerID)
		}
		if local[info.Name] {
			return nil, fmt.Errorf("duplicate tool %q within %s", info.Name, providerID)
		}
		local[info.Name] = true
		names = append(names, info.Name)
	}
	return names, nil
}

// kbToolProvider 把知识库的两个工具接进注册器。
//
// ★ 它只是适配器：建 embedder / store / indexer / retriever 的细节在
//
//	agentkit/build/kb.go。产物进入 heavyDeps.toolset，由 applyToolConfig 挂上。
type kbToolProvider struct{}

func (kbToolProvider) ID() string     { return "kb" }
func (kbToolProvider) Optional() bool { return true } // 配错语料不该拖垮整个 agent

func (kbToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	return buildKBTools(ctx, env.cfg, env.projectRoot, env.dataDir)
}

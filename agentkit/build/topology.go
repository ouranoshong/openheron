package build

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec/decl"
)

// 本文件是「谁能委派给谁」：深度守卫与子 agent 的声明边界。
//
// ★ 递归装配在 [assemble.go] 的 buildNode。R1 的闭环点在这里：eino 不把
// cfg.Handlers 传播给用户 SubAgents，所以每个子 agent 必须自己挂审批与过滤。

// depthKey 携带「当前这个 agent 距离根有几层」。
//
// ★ 靠 ctx 传递：task 调子 agent 时传同一个 ctx，所以父 agent 在
// BeforeAgent 里写的值，子 agent 读得到。
type depthKey struct{}

// withDepth 标注深度。
func withDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, depthKey{}, d)
}

// depthFrom 读深度；没有标注时是 0（根）。
func depthFrom(ctx context.Context) int {
	if v, ok := ctx.Value(depthKey{}).(int); ok {
		return v
	}
	return 0
}

// 深度语义：根是第 0 层；深度 d 的 agent 当且仅当 d < MaxDepth 才能再委派。
// MaxDepth=1 表示「只有根能委派」。默认值只有 config 包一份。
//
// ★ 框架没有深度限制；不自己设，子 agent 嵌套就是无界 token 消耗。

// taskToolName 是深度到顶时被摘掉的那个工具。
const taskToolName = "task"

// depthGuard 到顶摘掉 task 工具 —— 不报错：到顶的 agent 仍能做自己的事，
// 只是不能再往下派。
type depthGuard struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	max int
}

// newDepthMiddleware 造深度守卫。
func newDepthMiddleware(max int) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	if max < 0 {
		max = 0
	}
	return &depthGuard{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		max:                               max,
	}
}

// BeforeAgent 标注深度，并在到顶时摘掉 task。
func (g *depthGuard) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (
	context.Context, *adk.ChatModelAgentContext, error) {

	d := depthFrom(ctx)
	// ★ 无论是否摘工具都要往下标一层 —— 否则孙 agent 会读到自己那一层，
	//   上限就变成「每层各算一次」，形同虚设。
	next := withDepth(ctx, d+1)

	if runCtx != nil && d >= g.max {
		kept := make([]tool.BaseTool, 0, len(runCtx.Tools))
		for _, t := range runCtx.Tools {
			if info, err := t.Info(ctx); err == nil && info.Name == taskToolName {
				continue
			}
			kept = append(kept, t)
		}
		if len(kept) != len(runCtx.Tools) {
			runCtx.Tools = kept
		}
	}
	return next, runCtx, nil
}

// ── 子 agent 声明 ───────────────────────────────────────────────────────
//
// 规格类型在 [openheron/config]：装配层直接用它，不另立一份。
//
// ★ 另立一份会让「装配要哪些字段」与「配置给哪些字段」各说一套 ——
//
//	改一个字段漏改另一处时，编译不报错（两套都合法），而是运行期少一个值。
//	`agent → config` 本就是允许的单向依赖，没有理由切断它。

// specTable 建某个声明 agent 的工具表。装配与自省必须走同一处，否则
// deny 优先级改动只生效一边，而两边测试都绿。
func specTable(policy Policy, agentSpec decl.SubAgentSpec) *Table {
	return newTable(agentSpec.ID, policy.Deny, agentSpec.DisabledTools)
}

// subHandlers 装配子 agent 的中间件链：与根共用 [toolChain]，只有工具表
// 不同。抽成纯函数是为了让测试能直接验闸门，而不必猜 deep.New 的实例。
func subHandlers(policy Policy, agentSpec decl.SubAgentSpec, maxDepth int) []adk.TypedChatModelAgentMiddleware[*schema.Message] {
	// 三档与这个 agent 自己的工具表合成一个 boundary。
	return toolChain(newBoundary(policy, specTable(policy, agentSpec)), maxDepth)
}

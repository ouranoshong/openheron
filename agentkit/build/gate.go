package build

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"openheron/approval"
	"openheron/helpers"
)

// 本文件是「谁能调什么」的机制部分：过滤中间件、审批闸门，以及串起它们的
// [toolChain]。数据部分在 [policy.go]。整条链的顺序与框架位置都承重，
// 改动前先读 [toolChain] 的注释。
// filterMiddleware 让 Table 能作为 eino 中间件挂在 handler 链上。
//
// 摘除不写日志：中间件每轮都会跑，打日志会把运行数据刷进 stdout。
// 「deny 生效」的表现是工具表里少一项，由 [Table.Filter] 的返回计数与
// `/v2/agents` 的运行期观测呈现。
type filterMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	table *Table
}

// newFilterMiddleware 造一个按 Table 摘工具的中间件。
func newFilterMiddleware(t *Table) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &filterMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		table:                             t,
	}
}

// BeforeAgent 摘掉被封锁的工具。
func (m *filterMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (
	context.Context, *adk.ChatModelAgentContext, error) {

	if m.table == nil || runCtx == nil {
		return ctx, runCtx, nil
	}
	kept, removed := m.table.Filter(runCtx.Tools)
	if len(removed) > 0 {
		runCtx.Tools = kept
	}
	return ctx, runCtx, nil
}

// newApprovalMiddleware gates every ask-tier tool behind a human decision.
//
// The gate re-raises itself on resume unless it is the explicit resume target,
// because the framework may resume a different tool in the same turn.
func newApprovalMiddleware(p Policy) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &approvalMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		policy:                            p,
	}
}

// approvalMiddleware 给 ask 档的工具挂上人工决策。
//
// ★ 它只包住**工具执行**，不改工具表 —— 改表是 [filterMiddleware] 的事。
//
//	两者分开的原因见 [toolChain] 的顺序说明。
type approvalMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	policy Policy
}

// WrapInvokableToolCall 在工具真正执行前插入人工决策。
//
// ★ 只有 ask 档才挂闸。allow 放行；deny 已在装配期被摘掉工具 ——
//
//	走到这里说明摘除没生效（工具报不出名字），此时**仍然拦住**，
//	绝不让它悄悄跑起来。
func (m *approvalMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	if tCtx == nil {
		return endpoint, nil
	}
	name := tCtx.Name

	// ★ 只有 ask 档才挂闸。allow 放行；deny 已在装配期被摘掉工具 ——
	//   走到这里说明摘除没生效（工具报不出名字），此时**仍然拦住**，
	//   绝不让它悄悄跑起来。
	switch p := m.policy.TierOf(name); p {
	case TierAllow:
		return endpoint, nil
	case TierDeny:
		return func(context.Context, string, ...tool.Option) (string, error) {
			return "", fmt.Errorf("tool %q is denied by policy", name)
		}, nil
	}

	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		interrupted, _, storedArgs := tool.GetInterruptState[string](ctx)
		if !interrupted {
			return "", tool.StatefulInterrupt(ctx, &approval.ApprovalInfo{
				ToolName:        name,
				ArgumentsInJSON: args,
			}, args)
		}

		// ★ 读的是 [approval.Decision] —— 与 agentkit/run 塞进 Targets 的**同一个**
		// 具体类型。eino 的 GetResumeContext 是具体类型断言，类型对不上时
		// hasData 是 false，而下面把 false 当成「没有决定」。
		isTarget, hasData, data := tool.GetResumeContext[*approval.Decision](ctx)
		if !isTarget {
			// Resumed, but for a different component: re-raise so this tool waits
			// for its own decision instead of running unchecked.
			return "", tool.StatefulInterrupt(ctx, &approval.ApprovalInfo{
				ToolName:        name,
				ArgumentsInJSON: storedArgs,
			}, storedArgs)
		}
		if !hasData {
			// ★★ **fail closed**：这次 resume 没有带来一份可读的决定 ⇒
			//	**不许执行工具**。
			//
			//	此前这里是 `return endpoint(...)` —— 也就是「拿不到决定就放行」。
			//	那条路是被真实踩过的：agentkit/run 送的是 `*agentkit/run.ApprovalResult`，
			//	闸门读的是 `*agent.ApprovalResult`，类型断言必然失败 ⇒
			//	hasData 恒为 false ⇒ **每一次审批都被忽略**，而工具照常执行。
			//	「拒绝」与「批准」的效果完全一样。
			//
			//	宁可让这一轮失败，也不能在没有人工点头的情况下动工具 ——
			//	ask 档的全部意义就在这里。
			return "", fmt.Errorf("tool %q: approval decision is missing, refusing to run", name)
		}
		if !data.Approved {
			if data.Reason != "" {
				return fmt.Sprintf("tool %q was rejected: %s", name, data.Reason), nil
			}
			return fmt.Sprintf("tool %q was rejected", name), nil
		}
		return endpoint(ctx, storedArgs, opts...)
	}, nil
}

// toolChain 是 tool 侧中间件链（"能做什么"）。
//
// ★ Filter 必须在 Approval 之前：deny 的工具要先摘掉，否则闸门先挂中断，
// 模型拿不到「这个工具不存在」。UnknownToolScope 又必须在 Filter 之后，
// 否则它标注的是过滤前的表，会推荐一个本轮调不到的工具。
//
// ★ 整条链挂在 cfg.Handlers 上，而 eino 把它排在内置中间件之后；filesystem
// 是在自己的 BeforeAgent 里把文件工具加进 runCtx.Tools 的。所以 Filter 能
// 看到并摘掉 write_file。换到已废弃的 cfg.Middlewares 就会静默失效 ——
// TestDenyRemovesTheFilesystemToolsFromTheModel 钉住这个位置。
func toolChain(b boundary, maxDepth int) []adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return []adk.TypedChatModelAgentMiddleware[*schema.Message]{
		newFilterMiddleware(b.table),
		newApprovalMiddleware(b.policy),
		newDepthMiddleware(maxDepth),
		newUnknownToolScope(),
		helpers.NewSafeToolMiddleware(),
	}
}

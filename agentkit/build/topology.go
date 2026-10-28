package build

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec/decl"
)

type depthKey struct{}

func withDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, depthKey{}, d)
}

func depthFrom(ctx context.Context) int {
	if v, ok := ctx.Value(depthKey{}).(int); ok {
		return v
	}
	return 0
}

const taskToolName = "task"

type depthGuard struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	max int
}

func newDepthMiddleware(max int) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	if max < 0 {
		max = 0
	}
	return &depthGuard{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		max:                               max,
	}
}

func (g *depthGuard) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (
	context.Context, *adk.ChatModelAgentContext, error) {

	d := depthFrom(ctx)
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

func specTable(policy Policy, agentSpec decl.SubAgentSpec) *Table {
	return newTable(agentSpec.ID, policy.Deny, agentSpec.DisabledTools)
}

func subHandlers(policy Policy, agentSpec decl.SubAgentSpec, maxDepth int) []adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return toolChain(newBoundary(policy, specTable(policy, agentSpec)), maxDepth)
}

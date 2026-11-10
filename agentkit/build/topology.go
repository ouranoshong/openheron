package build

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
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

// specTable builds the tool table of one agent.
//
// ★ The agent's own tools: list is the **built-in** answer, and the servers
//
//	it mounts bring the rest: a mounted server's tools are the agent's
//	tools, and an unmounted one has none to bring. ★ An MCP server's tools
//	are named by their composed names — `mcp__<server>__<tool>` — so the
//	namespace is what keeps two servers' `ls` apart.
func specTable(policy Policy, agentSpec decl.SubAgentSpec) *Table {
	return newTable(agentSpec.ID, agentSpec.Tools, agentSpec.MCPServers)
}

func subHandlers(policy Policy, agentSpec decl.SubAgentSpec, maxDepth int) []adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return toolChain(newBoundary(policy, specTable(policy, agentSpec)), maxDepth)
}

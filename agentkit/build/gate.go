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

type filterMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	table *Table
}

func newFilterMiddleware(t *Table) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &filterMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		table:                             t,
	}
}

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
// The gate re-raises itself on resume unless it is the explicit resume target,
// because the framework may resume a different tool in the same turn.
func newApprovalMiddleware(p Policy) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &approvalMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		policy:                            p,
	}
}

type approvalMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	policy Policy
}

func (m *approvalMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	if tCtx == nil {
		return endpoint, nil
	}
	name := tCtx.Name

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

func toolChain(b boundary, maxDepth int) []adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return []adk.TypedChatModelAgentMiddleware[*schema.Message]{
		newFilterMiddleware(b.table),
		newApprovalMiddleware(b.policy),
		newDepthMiddleware(maxDepth),
		newUnknownToolScope(),
		helpers.NewSafeToolMiddleware(),
	}
}

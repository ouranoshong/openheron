package build

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/approval"
	"github.com/ouranoshong/openheron/helpers"
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

	// ★ The default is ask, so the gate wraps everything the exception
	//   list does not name.
	if m.policy.TierOf(name) == TierAllow {
		return endpoint, nil
	}

	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		decision, runArgs, ok, err := m.decide(ctx, name, args)
		if err != nil {
			return "", err
		}
		if !ok {
			return decision, nil
		}
		return endpoint(ctx, runArgs, opts...)
	}, nil
}

// ★ The streaming path is not an alternative to the synchronous one: it is
// the same gate. The framework reaches a tool through one of two wrappers,
// and the shell's `execute` is the one filesystem tool that is a streaming
// one — so a gate that only implements the synchronous wrapper leaves the
// tool the policy names most often unguarded. The policy file names a tool;
// the gate has to guard it whichever way the framework happens to call it.
func (m *approvalMiddleware) WrapStreamableToolCall(
	_ context.Context,
	endpoint adk.StreamableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {
	if tCtx == nil {
		return endpoint, nil
	}
	name := tCtx.Name

	// ★ The gate has two tiers only: allow runs, everything else asks.
	//   There is no "deny" — a tool an agent may not use is not in its
	//   tool set at all, so the gate never sees it.
	if m.policy.TierOf(name) == TierAllow {
		return endpoint, nil
	}

	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		decision, runArgs, ok, err := m.decide(ctx, name, args)
		if err != nil {
			return nil, err
		}
		if !ok {
			// The refusal is a message to the model, not an error: the
			// synchronous path says so too, and a model that reads "the tool
			// was rejected" can pick another way instead of falling over.
			return singleChunk(decision), nil
		}
		return endpoint(ctx, runArgs, opts...)
	}, nil
}

// decide is the gate's one decision, shared by both call paths.
//
// It returns the arguments to run with and true when the tool may run, and
// the model's reply and false when it may not — either because the human
// said no, or because the decision was missing.
func (m *approvalMiddleware) decide(ctx context.Context, name, args string) (reply string, runArgs string, ok bool, err error) {
	interrupted, _, storedArgs := tool.GetInterruptState[string](ctx)
	if !interrupted {
		return "", "", false, tool.StatefulInterrupt(ctx, &approval.ApprovalInfo{
			ToolName:        name,
			ArgumentsInJSON: args,
		}, args)
	}

	isTarget, hasData, data := tool.GetResumeContext[*approval.Decision](ctx)
	if !isTarget {
		// Resumed, but for a different component: re-raise so this tool waits
		// for its own decision instead of running unchecked.
		return "", "", false, tool.StatefulInterrupt(ctx, &approval.ApprovalInfo{
			ToolName:        name,
			ArgumentsInJSON: storedArgs,
		}, storedArgs)
	}
	if !hasData {
		return "", "", false, fmt.Errorf("tool %q: approval decision is missing, refusing to run", name)
	}
	if !data.Approved {
		if data.Reason != "" {
			return fmt.Sprintf("tool %q was rejected: %s", name, data.Reason), "", false, nil
		}
		return fmt.Sprintf("tool %q was rejected", name), "", false, nil
	}
	// The arguments to run are the ones the interrupted call stored, not the
	// ones this call carries: on resume the model may have sent something
	// else, and the tool must run what the human approved.
	return "", storedArgs, true, nil
}

// singleChunk is one message in a stream: the refusal the model reads.
func singleChunk(msg string) *schema.StreamReader[string] {
	sr, sw := schema.Pipe[string](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr
}

func toolChain(b boundary, maxDepth int) []adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return []adk.TypedChatModelAgentMiddleware[*schema.Message]{
		// ★ **First**: a call whose arguments were cut off by the model's
		//   own output limit must not run, and it must not reach a human as
		//   a card to approve either. The handlers are applied so that
		//   handlers[0] ends up outermost (eino's adk applies them in
		//   reverse), so this one sees the call before the gate does.
		newTruncationGuardMiddleware(),
		newFilterMiddleware(b.table),
		newApprovalMiddleware(b.policy),
		newDepthMiddleware(maxDepth),
		newUnknownToolScope(),
		helpers.NewSafeToolMiddleware(),
	}
}

package build

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// summarizationMiddleware builds the context-compaction middleware for one
// agent.
//
// It returns nil when the agent has no threshold: compaction is opt-in per
// agent, and a conversation that never crosses a threshold must not carry a
// middleware it will never use.
//
// The threshold belongs to the agent, the window to the model it runs on.
// The two meet here, and only here — the builder owns the model resolution,
// so nobody else gets to guess which window an agent has.
func (b *Builder) summarizationMiddleware(ctx context.Context, snap *snapshot,
	sp *decl.SubAgentSpec, mdl model.BaseModel[*schema.Message]) (adk.TypedChatModelAgentMiddleware[*schema.Message], error) {

	if sp == nil || mdl == nil {
		return nil, nil
	}

	window := modelContextOf(snap.cfg, sp.ModelRef).Window
	at := spec.SummarizeAt(sp.SummarizeAt, window)
	if at <= 0 {
		return nil, nil
	}

	mw, err := summarization.New(ctx, &summarization.Config{
		Model: mdl,
		Trigger: &summarization.TriggerCondition{
			ContextTokens: at,
		},
		Finalize: finalize,
		// The transport reads the replacement history off the
		// after_summarize event; without it there is nothing to persist.
		EmitInternalEvents: true,
	})
	if err != nil {
		return nil, err
	}
	obs.Logger().Info("[agent] context compaction enabled", "agent", sp.ID, "summarizeAt", at)
	return mw, nil
}

// compactionMiddleware attaches the compaction middleware to an agent that
// has a threshold.
//
// A middleware that cannot be built is not fatal: the conversation carries
// on without compaction, which is what happened before compaction existed.
func (b *Builder) compactionMiddleware(ctx context.Context, snap *snapshot,
	sp *decl.SubAgentSpec, mdl model.BaseModel[*schema.Message]) adk.TypedChatModelAgentMiddleware[*schema.Message] {

	mw, err := b.summarizationMiddleware(ctx, snap, sp, mdl)
	if err != nil {
		obs.Logger().Warn("[agent] context compaction disabled", "agent", agentID(sp), "err", err)
		return nil
	}
	return mw
}

// agentID names an agent for the log.
func agentID(sp *decl.SubAgentSpec) string {
	if sp == nil {
		return ""
	}
	return sp.ID
}

// finalize is the finalizer openheron hands the summarization middleware.
//
// eino's default keeps the agent's system messages in the replacement
// history, and openheron wants that: the context prompt — the rules, the
// project root, the workspace — is a system message
// (agentkit/run/input.go), and dropping it would leave the agent without
// them for the rest of the turn.
//
// The prompt is transient and never persisted: the transport takes the
// summary off the replacement history, and only the summary reaches the
// transcript. What the next turn rebuilds is the live prompt.
func finalize(ctx context.Context, original []*schema.Message,
	summary *schema.Message) ([]*schema.Message, error) {
	return summarization.DefaultFinalize(ctx, original, summary)
}

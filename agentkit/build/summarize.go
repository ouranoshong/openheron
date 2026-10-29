package build

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
)

// summarizationMiddleware builds the context-compaction middleware for the
// root agent.
//
// It returns nil when the model declares no threshold: compaction is opt-in,
// and a conversation that never crosses a threshold must not carry a
// middleware it will never use.
//
// Only the root agent gets one. A sub-agent's context lives inside its own
// run and dies with it (see agentkit/run/input.go), so there is nothing
// durable to compact.
func (b *Builder) summarizationMiddleware(ctx context.Context, snap *snapshot,
	ref string, mdl model.BaseModel[*schema.Message]) (adk.TypedChatModelAgentMiddleware[*schema.Message], error) {

	at := summarizeAtOf(snap.cfg, ref)
	if at <= 0 {
		return nil, nil
	}
	if mdl == nil {
		return nil, nil
	}

	mw, err := summarization.New(ctx, &summarization.Config{
		Model: mdl,
		Trigger: &summarization.TriggerCondition{
			ContextTokens: at,
		},
		Finalize: finalizeWithoutSystem,
		// The transport reads the replacement history off the
		// after_summarize event; without it there is nothing to persist.
		EmitInternalEvents: true,
	})
	if err != nil {
		return nil, err
	}
	obs.Logger().Info("[agent] context compaction enabled", "summarizeAt", at)
	return mw, nil
}

// summarizeAtOf reads the compaction threshold of the model a ref points at.
//
// An unresolvable ref yields no threshold rather than an error: the model
// itself was already resolved when the agent was assembled, so reaching this
// point with a broken ref means the threshold is simply not configured.
func summarizeAtOf(cfg *spec.Config, ref string) int {
	ref = strings.TrimSpace(ref)
	if cfg == nil || ref == "" {
		return 0
	}
	_, m, err := Resolve(cfg, ref)
	if err != nil || m == nil {
		return 0
	}
	return m.SummarizeAt
}

// finalizeWithoutSystem is the finalizer openheron hands the summarization
// middleware.
//
// eino's default keeps the agent's system messages in the replacement
// history. openheron's context prompt is transient — it is rebuilt every turn
// and never persisted (agentkit/run/input.go) — so a system message in the
// persisted history would shadow the live one. The summary alone is what the
// agent needs to carry the conversation forward.
func finalizeWithoutSystem(ctx context.Context, original []*schema.Message,
	summary *schema.Message) ([]*schema.Message, error) {

	msgs, err := summarization.DefaultFinalize(ctx, original, summary)
	if err != nil {
		return nil, err
	}
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil && m.Role == schema.System {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

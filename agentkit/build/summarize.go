package build

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

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
	b.logger().Info("[agent] context compaction enabled", "agent", sp.ID, "summarizeAt", at)
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
		b.logger().Warn("[agent] context compaction disabled", "agent", agentID(sp), "err", err)
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

// Summarizer turns a message list into the summary a compaction produces.
//
// It exists for the manual compaction: eino's middleware only summarizes when
// its trigger fires, and a manual command is the user asking for it now. The
// summary itself is eino's — same model, same finalizer, same wording — so a
// conversation compacted by hand and one compacted by the threshold carry the
// same kind of message.
type Summarizer struct {
	mw *summarization.TypedMiddleware[*schema.Message]
}

// Summarize returns the summary of the given messages.
//
// The summary is the last message of the replacement history that is not a
// system message: the replacement history keeps the agent's system messages
// (the context prompt lives there), and only the summary is worth keeping.
func (s *Summarizer) Summarize(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
	if s == nil || s.mw == nil {
		return nil, errors.New("no summarizer")
	}
	state := &adk.TypedChatModelAgentState[*schema.Message]{Messages: msgs}
	out, err := s.mw.Summarize(ctx, state)
	if err != nil {
		return nil, err
	}
	var summary *schema.Message
	for _, m := range out {
		if m == nil || m.Role == schema.System {
			continue
		}
		summary = m
	}
	if summary == nil {
		return nil, errors.New("the summarizer produced no summary")
	}
	return summary, nil
}

// SummarizerFor builds the summarizer of one agent.
//
// ★ It is independent of the agent's threshold. The threshold decides when
// the context is compacted **by itself**; a manual command is the user
// asking for it now, and "off" must not turn that into an error.
//
// The model is the one the agent runs on, and the finalizer is the same one
// the middleware uses — the summary must not differ by how it was asked for.
func (b *Builder) SummarizerFor(ctx context.Context, agentID string) (*Summarizer, error) {
	snap := b.currentSnapshot()
	if snap == nil {
		return nil, errors.New("agent: no configuration is loaded")
	}
	name := strings.TrimSpace(agentID)
	if name == "" {
		name = snap.agentSpec.defaultAgent
	}
	sp, ok := snap.agentSpec.agentSpec(name)
	if !ok {
		return nil, fmt.Errorf("agent %q is not declared", name)
	}
	mdl, err := resolveModel(ctx, snap, sp, nil)
	if err != nil {
		return nil, err
	}
	mw, err := summarization.New(ctx, &summarization.Config{
		Model: mdl,
		// The trigger never fires here: the summary is requested directly.
		Trigger:  &summarization.TriggerCondition{ContextTokens: 1},
		Finalize: finalize,
	})
	if err != nil {
		return nil, err
	}
	concrete, ok := mw.(*summarization.TypedMiddleware[*schema.Message])
	if !ok {
		return nil, fmt.Errorf("the summarization middleware is %T, not the expected type", mw)
	}
	return &Summarizer{mw: concrete}, nil
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

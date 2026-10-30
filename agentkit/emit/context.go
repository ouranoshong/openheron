package emit

import (
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
)

// ContextUsageOf returns the context occupancy of every agent that spoke.
//
// The baseline is each agent's last assistant message that carried usage: the
// endpoint reported the size of that request, and the next request has to
// carry all of it. A sum over the whole session would say nothing about how
// full the context is right now.
//
// The live path and the history path both call this, so the number on screen
// and the number after a refresh cannot disagree.
func ContextUsageOf(msgs []*schema.Message, contexts map[string]contract.AgentContext) map[string]contract.ContextUsage {
	out := map[string]contract.ContextUsage{}
	for _, m := range msgs {
		u, ok := contextUsageOfMessage(m, contexts)
		if !ok {
			continue
		}
		out[u.Agent] = u
	}
	return out
}

// contextUsageOfMessage reads the context occupancy off one assistant message.
//
// The message has to be an assistant message that carries usage: a tool result
// or a user message says nothing about what the model was asked.
func contextUsageOfMessage(m *schema.Message, contexts map[string]contract.AgentContext) (contract.ContextUsage, bool) {
	if m == nil || m.Role != schema.Assistant || m.ResponseMeta == nil || m.ResponseMeta.Usage == nil {
		return contract.ContextUsage{}, false
	}
	agent := message.MessageAgent(m)
	if agent == "" {
		return contract.ContextUsage{}, false
	}
	used := m.ResponseMeta.Usage.TotalTokens
	if used == 0 {
		used = m.ResponseMeta.Usage.PromptTokens + m.ResponseMeta.Usage.CompletionTokens
	}
	if used == 0 {
		return contract.ContextUsage{}, false
	}
	ctx := contexts[agent]
	return contract.ContextUsage{
		Agent:         agent,
		Provider:      ctx.Provider,
		Model:         ctx.Model,
		UsedTokens:    used,
		ContextWindow: ctx.Window,
		SummarizeAt:   ctx.SummarizeAt,
		UpdatedAt:     time.Now().UTC(),
	}, true
}

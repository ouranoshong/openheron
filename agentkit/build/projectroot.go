package build

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// roundBlockMiddleware hands the round's project root to a sub-agent.
//
// ★ A sub-agent never sees the round prompt: the framework hands it the
//
//	request and nothing else. ★ Without this the root agent works in one
//	directory and its sub-agents are told about another — or about none —
//	and the two disagree about where the code lives.
//
// ★ The block is read from the **context** at run time, and not at build
//
//	time: the tree is pooled and reused across rounds, and a block baked
//	into the instruction would be the root of the round that built it.
type roundBlockMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	block func(context.Context) string
}

func newRoundBlockMiddleware(block func(context.Context) string) adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &roundBlockMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
		block:                             block,
	}
}

func (m *roundBlockMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (
	context.Context, *adk.ChatModelAgentContext, error) {

	if m == nil || m.block == nil || runCtx == nil {
		return ctx, runCtx, nil
	}
	text := strings.TrimSpace(m.block(ctx))
	if text == "" {
		return ctx, runCtx, nil
	}
	// ★ The block goes **first**: the instruction is the rule, and the
	//   directory the rule is about comes before it.
	runCtx.Instruction = text + "\n\n" + runCtx.Instruction
	return ctx, runCtx, nil
}

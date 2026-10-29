package build

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// summarizingModel answers every call with a summary-shaped message.
//
// The summarization middleware asks the model for a summary and expects the
// <all_user_messages> block back; the agent asks it for an answer. One shape
// serves both in this test.
type summarizingModel struct {
	mu    sync.Mutex
	calls int
}

func (m *summarizingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return &schema.Message{
		Role:    schema.Assistant,
		Content: "the summary\n<all_user_messages>\n</all_user_messages>\n",
	}, nil
}

func (m *summarizingModel) Stream(context.Context, []*schema.Message, ...model.Option) (
	*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{}), nil
}

// The middleware announces the completed compaction on the agent's event
// stream. That event is the only thing the transport has to pick the
// compacted message up from, so it has to actually arrive.
func TestTheCompactionEventReachesTheEventStream(t *testing.T) {
	ctx := context.Background()
	mdl := &summarizingModel{}

	mw, err := summarization.New(ctx, &summarization.Config{
		Model:              mdl,
		Trigger:            &summarization.TriggerCondition{ContextTokens: 1},
		Finalize:           finalize,
		EmitInternalEvents: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	agent, err := deep.New(ctx, &deep.Config{
		Name:         "main",
		ChatModel:    mdl,
		MaxIteration: 1,
		Handlers:     []adk.TypedChatModelAgentMiddleware[*schema.Message]{mw},
	})
	if err != nil {
		t.Fatal(err)
	}

	iter := agent.Run(ctx, &adk.TypedAgentInput[*schema.Message]{
		Messages: []*schema.Message{{Role: schema.User, Content: "hello"}},
	})

	var after *summarization.TypedAfterSummarizeAction[*schema.Message]
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Action == nil || ev.Action.CustomizedAction == nil {
			continue
		}
		a, ok := ev.Action.CustomizedAction.(*summarization.TypedCustomizedAction[*schema.Message])
		if !ok || a.Type != summarization.ActionTypeAfterSummarize {
			continue
		}
		after = a.After
	}

	if after == nil {
		t.Fatal("the compaction never announced itself on the event stream")
	}
	// The replacement history is the system message plus the summary. The
	// transport takes the summary off it — that is the one that reaches the
	// transcript — and the system message stays in the agent's state so the
	// context prompt survives the cut.
	if len(after.Messages) != 2 {
		t.Fatalf("the compacted history has %d messages, want the system message and the summary",
			len(after.Messages))
	}
	last := after.Messages[len(after.Messages)-1]
	if last.Role == schema.System {
		t.Fatal("the summary must not be a system message")
	}
	if after.Messages[0].Role != schema.System {
		t.Fatal("the compacted history lost the system message")
	}
}

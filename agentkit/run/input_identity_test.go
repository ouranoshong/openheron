package run

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
)

// capturingAgent keeps the input it was handed.
type capturingAgent struct{ got chan []*schema.Message }

func (a *capturingAgent) Name(context.Context) string        { return "capture" }
func (a *capturingAgent) Description(context.Context) string { return "capture" }

func (a *capturingAgent) Run(ctx context.Context, in *adk.TypedAgentInput[*schema.Message], _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	go func() {
		defer gen.Close()
		select {
		case a.got <- in.Messages:
		default:
		}
	}()
	return iter
}

// ★ The message the model sees and the message the transcript holds are the
//
//	**same object**, id and all.
//
//	That is the point of building the input before persisting: the model gets
//	the stamped message, not a copy rebuilt from its text. The client then
//	keys that message by the very id the model saw.
func TestTheModelAndTheTranscriptShareTheSameMessageID(t *testing.T) {
	probe := &capturingAgent{got: make(chan []*schema.Message, 1)}
	b := &fakeBuilder{agent: func(call int, kind contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return probe
	}}
	rt, sm := newTestRuntime(t, b)
	sess := mustSession(t, sm)

	if _, err := rt.Submit(context.Background(), sess.ID(), "", "hello"); err != nil {
		t.Fatal(err)
	}

	var seen *schema.Message
	select {
	case msgs := <-probe.got:
		for _, m := range msgs {
			if m != nil && m.Role == schema.User && message.UserText(m) == "hello" {
				seen = m
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent was never called")
	}
	if seen == nil {
		t.Fatal("the model must see the question")
	}
	if !message.HasMessageID(seen) {
		t.Fatal("the message the model saw carries no id")
	}

	var stored *schema.Message
	for _, m := range sess.Messages(false) {
		if message.UserText(m) == "hello" {
			stored = m
		}
	}
	if stored == nil {
		t.Fatal("the question must be in the transcript")
	}
	if message.MessageID(seen) != message.MessageID(stored) {
		t.Fatalf("the model saw id %q but the transcript holds %q",
			message.MessageID(seen), message.MessageID(stored))
	}
}

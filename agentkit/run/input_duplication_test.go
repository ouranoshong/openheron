package run

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// countUserText returns how many user messages in the list carry this text.
func countUserText(msgs []*schema.Message, text string) int {
	n := 0
	for _, m := range msgs {
		if m != nil && m.Role == schema.User && message.UserText(m) == text {
			n++
		}
	}
	return n
}

// ★ The turn's question must reach the model exactly once.
//
//	The user message is built first and persisted after, so the history does
//	not carry it yet and the input cannot hold two copies of it. This is
//	structural: there is no "is this the same question?" check that could
//	mistake one turn's question for another's.
func TestBuildInputCarriesTheQuestionOnce(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q1"),
			schema.AssistantMessage("a1", nil),
		},
	}, schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got := countUserText(in.Messages, "hello"); got != 1 {
		t.Fatalf("the question appears %d times, want 1: %s", got, describeHistory(in.Messages))
	}
}

// ★ The message the model sees must be the very object the transcript holds.
//
//	Anything stamped on it — a message id, an attachment list — has to reach
//	the model too. A copy rebuilt from the text would carry none of it.
func TestBuildInputCarriesTheMessageObject(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	userMsg := schema.UserMessage("hello")
	message.StampMessageID(userMsg, "msg-fixed-id")

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{schema.UserMessage("q1")},
	}, userMsg)
	if err != nil {
		t.Fatal(err)
	}
	last := in.Messages[len(in.Messages)-1]
	if last != userMsg {
		t.Fatal("the input must carry the message itself, not a copy of it")
	}
	if got := message.MessageID(last); got != "msg-fixed-id" {
		t.Fatalf("the message id did not reach the model: %q", got)
	}
}

// ★ A user who asks the same thing twice has two identical user messages in
// the history. The older one must not be mistaken for this turn's question —
// that would drop the real one and answer the previous turn.
func TestBuildInputKeepsBothWhenTheUserRepeatedTheQuestion(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("hello"),
			schema.AssistantMessage("a1", nil),
			schema.UserMessage("hello"),
		},
	}, schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got := countUserText(in.Messages, "hello"); got != 3 {
		t.Fatalf("the repeated question appears %d times, want 3 (two in the history and this turn's): %s",
			got, describeHistory(in.Messages))
	}
}

// ★ A turn with no question at all is still a valid input: the context and
// the history alone.
func TestBuildInputWithoutAQuestion(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{schema.UserMessage("q1")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := countUserText(in.Messages, "q1"); got != 1 {
		t.Fatalf("the history message appears %d times, want 1: %s", got, describeHistory(in.Messages))
	}
}

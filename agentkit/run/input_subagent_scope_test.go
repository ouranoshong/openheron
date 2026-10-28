package run

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

func TestSubagentMessagesAreKeptOutOfTheModelInput(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	parentCall := assistantWithCalls("c1")
	parentResult := schema.ToolMessage("专家的总结", "c1", schema.WithToolName("task"))

	expertCall := assistantWithCalls("c2")
	expertResult := schema.ToolMessage("read_file 的原始输出", "c2", schema.WithToolName("read_file"))
	for _, m := range []*schema.Message{expertCall, expertResult} {
		message.StampAgent(m, "task")
		message.StampSubagent(m)
	}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q"),
			parentCall,
			expertCall,
			expertResult,
			parentResult,
		},
	}, "next")
	if err != nil {
		t.Fatal(err)
	}

	got := in.Messages[1:]
	for _, m := range got {
		if message.IsSubagent(m) {
			t.Fatalf("a subagent message leaked into the model input: %+v", m)
		}
		if m == expertCall || m == expertResult {
			t.Fatal("the expert's own messages must not reach the parent's context")
		}
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 (user + parent call + parent result + new query): %s",
			len(got), describeHistory(got))
	}
	assertAnswerable(t, got)
}

func TestRemovingSubagentMessagesKeepsTheParentsPairsIntact(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	parentCall := assistantWithCalls("c1")
	parentResult := schema.ToolMessage("专家的总结", "c1", schema.WithToolName("task"))
	expertCall := assistantWithCalls("c2")
	expertResult := schema.ToolMessage("read_file 的原始输出", "c2", schema.WithToolName("read_file"))
	for _, m := range []*schema.Message{expertCall, expertResult} {
		message.StampSubagent(m)
	}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q"),
			parentCall,
			expertCall,
			expertResult,
			parentResult,
		},
	}, "next")
	if err != nil {
		t.Fatal(err)
	}
	got := in.Messages[1:]

	var sawCall bool
	for _, m := range got {
		if len(m.ToolCalls) > 0 {
			sawCall = true
			continue
		}
		if sawCall {
			if m.ToolCallID != "c1" {
				t.Fatalf("the parent's tool result was lost or mismatched: %+v", m)
			}
			break
		}
	}
	assertAnswerable(t, got)
}

func TestSubagentMessagesAreFilteredInEveryTurn(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}
	expert := schema.AssistantMessage("专家的中间推理", nil)
	message.StampAgent(expert, "main")
	message.StampSubagent(expert)

	for i := 0; i < 3; i++ {
		in, err := c.buildInput(context.Background(), inputParams{
			History: []*schema.Message{schema.UserMessage("q"), expert},
		}, "next")
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range in.Messages {
			if message.IsSubagent(m) {
				t.Fatalf("turn %d: a subagent message leaked into the model input", i)
			}
		}
	}
}

func TestWithoutSubagentMessagesDoesNotMutateTheInput(t *testing.T) {
	kept := schema.UserMessage("q")
	expert := schema.AssistantMessage("专家的", nil)
	message.StampSubagent(expert)
	hist := []*schema.Message{kept, expert}

	out := withoutSubagentMessages(hist)
	if len(out) != 1 || out[0] != kept {
		t.Fatalf("out = %d messages, want the single root message", len(out))
	}
	if len(hist) != 2 {
		t.Fatalf("the input slice was mutated: %d messages, want 2", len(hist))
	}
}

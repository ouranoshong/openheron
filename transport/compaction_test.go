package transport

import (
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
)

func compactionEvent(msgs ...*schema.Message) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Action: &adk.AgentAction{
			CustomizedAction: &summarization.TypedCustomizedAction[*schema.Message]{
				Type: summarization.ActionTypeAfterSummarize,
				After: &summarization.TypedAfterSummarizeAction[*schema.Message]{
					Messages: msgs,
				},
			},
		},
	}
}

// The transport is where the agent's events are read, so it is where the
// compacted message is picked up on its way to the transcript.
func TestTheCompactionIsCaptured(t *testing.T) {
	summary := &schema.Message{Role: schema.User, Content: "summary of the conversation"}

	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		compactionEvent(summary),
		exitEvent(),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if res.Compaction == nil {
		t.Fatal("the compaction was not captured")
	}
	if res.Compaction.Content != "summary of the conversation" {
		t.Fatalf("the captured compaction is the wrong message: %q", res.Compaction.Content)
	}
}

// Only the completed compaction is a compaction: the event that announces it
// carries the old history, which must not be persisted as the new one.
func TestOnlyTheCompletedCompactionIsCaptured(t *testing.T) {
	before := &adk.TypedAgentEvent[*schema.Message]{
		Action: &adk.AgentAction{
			CustomizedAction: &summarization.TypedCustomizedAction[*schema.Message]{
				Type: summarization.ActionTypeBeforeSummarize,
				Before: &summarization.TypedBeforeSummarizeAction[*schema.Message]{
					Messages: []*schema.Message{{Role: schema.User, Content: "old history"}},
				},
			},
		},
	}

	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{before, exitEvent()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compaction != nil {
		t.Fatalf("the pre-compaction event must not be captured: %q", res.Compaction.Content)
	}
}

// A run without a compaction reports none.
func TestARunWithoutACompaction(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("working")),
		exitEvent(),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compaction != nil {
		t.Fatal("a run that never compacted must not report a compaction")
	}
}

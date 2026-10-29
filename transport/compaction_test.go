package transport

import (
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
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

// The compaction happens mid-turn, and the user is watching: the client has
// to learn about it from the stream.
func TestTheCompactionIsPublishedOnTheStream(t *testing.T) {
	summary := &schema.Message{Role: schema.User, Content: "summary of the conversation"}

	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		compactionEvent(summary),
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, ev := range evs {
		custom, ok := ev.(*events.CustomEvent)
		if !ok || custom.Name != "context_compacted" {
			continue
		}
		found = true
		value, _ := custom.Value.(map[string]any)
		if value["agent"] != "main" {
			t.Fatalf("the event names the wrong agent: %v", value["agent"])
		}
		if at, _ := value["at"].(string); at == "" {
			t.Fatal("the event must say when the compaction happened")
		}
	}
	if !found {
		t.Fatal("the compaction never reached the stream")
	}
}

// A run without a compaction says nothing about one.
func TestARunWithoutACompactionSaysNothingOnTheStream(t *testing.T) {
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("working")),
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if custom, ok := ev.(*events.CustomEvent); ok && custom.Name == "context_compacted" {
			t.Fatal("a run that never compacted must not announce one")
		}
	}
}

// A sub-agent compacts inside its own run. Its summary is not a boundary
// of the session's history — writing it to the transcript would cut the
// root's effective history at a sub-agent's summary.
func TestASubAgentsCompactionIsNotTheSessions(t *testing.T) {
	summary := &schema.Message{Role: schema.User, Content: "summary of the conversation"}
	ev := compactionEvent(summary)
	ev.AgentName = "coder"

	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		ev,
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compaction != nil {
		t.Fatal("a sub-agent's compaction must not become the session's compaction")
	}
}

// The replacement history keeps the agent's system messages — the context
// prompt lives there. The transcript gets the summary alone.
func TestTheSummaryIsPickedOutOfTheReplacementHistory(t *testing.T) {
	prompt := &schema.Message{Role: schema.System, Content: "you are the context prompt"}
	summary := &schema.Message{Role: schema.User, Content: "summary of the conversation"}

	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		compactionEvent(prompt, summary),
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compaction == nil {
		t.Fatal("the compaction was not captured")
	}
	if res.Compaction.Content != "summary of the conversation" {
		t.Fatalf("the captured message = %q, want the summary", res.Compaction.Content)
	}
}

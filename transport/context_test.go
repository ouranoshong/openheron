package transport

import (
	"github.com/ouranoshong/openheron/agentkit/emit"

	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
)

func usageEvent(prompt, completion, total int) *adk.TypedAgentEvent[*schema.Message] {
	msg := assistantText("answer")
	msg.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      total,
		},
	}
	return textEvent(msg)
}

func testContexts() map[string]contract.AgentContext {
	return map[string]contract.AgentContext{
		"main": {Window: 128000, SummarizeAt: 102400, Provider: "deepseek", Model: "flash"},
	}
}

// The occupancy is the size of the last request, not the sum of the session:
// a session can carry a large total and an almost empty context.
func TestTheContextUsageIsTheLastCallNotTheSum(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		usageEvent(100, 20, 120),
		usageEvent(300, 40, 340),
		exitEvent(),
	}, emit.Options{RootAgent: "main", Contexts: testContexts()})
	if err != nil {
		t.Fatal(err)
	}

	got := emit.ContextUsageOf(res.Intermediates, testContexts())
	if len(got) != 1 {
		t.Fatalf("the occupancy covers %d agents, want 1", len(got))
	}
	u := got["main"]
	if u.UsedTokens != 340 {
		t.Fatalf("used = %d tokens, want 340 (the last call, not the sum)", u.UsedTokens)
	}
	if u.ContextWindow != 128000 {
		t.Fatalf("window = %d, want 128000", u.ContextWindow)
	}
	if u.SummarizeAt != 102400 {
		t.Fatalf("summarizeAt = %d, want 102400", u.SummarizeAt)
	}
	if u.Provider != "deepseek" || u.Model != "flash" {
		t.Fatalf("the model is wrong: %s/%s", u.Provider, u.Model)
	}
}

// An agent that never spoke has no occupancy.
func TestAnAgentThatNeverSpokeIsAbsent(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		usageEvent(100, 20, 120),
		exitEvent(),
	}, emit.Options{RootAgent: "main", Contexts: testContexts()})
	if err != nil {
		t.Fatal(err)
	}
	got := emit.ContextUsageOf(res.Intermediates, testContexts())
	if _, ok := got["coder"]; ok {
		t.Fatal("an agent that never spoke must not be reported")
	}
}

// The endpoint reports no usage: there is no occupancy to show, and the meter
// must be empty rather than "0 tokens".
func TestNoUsageMeansNoOccupancy(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("answer")),
		exitEvent(),
	}, emit.Options{RootAgent: "main", Contexts: testContexts()})
	if err != nil {
		t.Fatal(err)
	}
	if got := emit.ContextUsageOf(res.Intermediates, testContexts()); len(got) != 0 {
		t.Fatalf("no usage must mean no occupancy, got %+v", got)
	}
}

// The occupancy goes out on the stream, so the meter moves while the run
// goes instead of only at the end.
func TestTheOccupancyIsPublishedOnTheStream(t *testing.T) {
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		usageEvent(300, 40, 340),
		exitEvent(),
	}, emit.Options{RootAgent: "main", Contexts: testContexts()})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, ev := range evs {
		custom, ok := ev.(*events.CustomEvent)
		if !ok || custom.Name != "context_usage" {
			continue
		}
		found = true
		value, _ := custom.Value.(map[string]any)
		if value["agent"] != "main" {
			t.Fatalf("the event names the wrong agent: %v", value["agent"])
		}
		// The value crossed a JSON boundary, so the numbers are floats now.
		if got := numberOf(value["used_tokens"]); got != 340 {
			t.Fatalf("the event carries %v tokens, want 340", value["used_tokens"])
		}
		if got := numberOf(value["context_window"]); got != 128000 {
			t.Fatalf("the event carries a window of %v, want 128000", value["context_window"])
		}
	}
	if !found {
		t.Fatal("the occupancy never reached the stream")
	}
}

// numberOf reads a number that may have crossed a JSON boundary.
func numberOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return -1
	}
}

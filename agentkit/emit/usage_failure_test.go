package emit

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
)

// ★ A run that ends in an error still spent tokens on every call it
//
//	completed before the failure. Those messages are archived, and the
//	session's own usage view reads them back — so the derivation has to
//	report them too, on the error path and not only on the successful
//	exit.
func TestAFailedRunStillReportsTheUsageItSpent(t *testing.T) {
	d := &deriver{opts: Options{}}
	d.intermediates = []*schema.Message{assistantWithUsage("m1", 100, 20, 10)}

	arts := d.artifacts()
	if arts.Usage.PromptTokens != 100 {
		t.Fatalf("prompt tokens = %d, want 100 (the tokens the run spent before it failed)",
			arts.Usage.PromptTokens)
	}
	if arts.Usage.CompletionTokens != 20 {
		t.Fatalf("completion tokens = %d, want 20", arts.Usage.CompletionTokens)
	}
	if arts.Usage.TotalTokens != 120 {
		t.Fatalf("total tokens = %d, want 120", arts.Usage.TotalTokens)
	}
	if arts.Usage.CachedTokens != 10 {
		t.Fatalf("cached tokens = %d, want 10", arts.Usage.CachedTokens)
	}
}

// The endpoint's own number wins: a run whose messages already carry a
// usage must not have it recomputed from the same messages.
func TestTheEndpointsOwnUsageIsKept(t *testing.T) {
	d := &deriver{opts: Options{}}
	d.usage = contract.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}
	d.intermediates = []*schema.Message{assistantWithUsage("m1", 100, 20, 10)}

	arts := d.artifacts()
	if arts.Usage.TotalTokens != 10 {
		t.Fatalf("total tokens = %d, want 10 (the endpoint's own figure)", arts.Usage.TotalTokens)
	}
}

// A run whose endpoint reported nothing has no usage to report, and the
// fallback must not invent one.
func TestARunWithoutReportedUsageReportsNone(t *testing.T) {
	d := &deriver{opts: Options{}}
	d.intermediates = []*schema.Message{schema.AssistantMessage("答完了", nil)}

	arts := d.artifacts()
	if arts.Usage.TotalTokens != 0 {
		t.Fatalf("total tokens = %d, want 0 (the endpoint reported none)", arts.Usage.TotalTokens)
	}
}

// assistantWithUsage builds an archived assistant message carrying the
// endpoint's token accounting.
func assistantWithUsage(id string, prompt, completion, cached int) *schema.Message {
	m := schema.AssistantMessage("答完了", nil)
	m.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			PromptTokenDetails: schema.PromptTokenDetails{
				CachedTokens: cached,
			},
		},
	}
	return m
}

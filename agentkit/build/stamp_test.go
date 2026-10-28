package build

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/message"
)

type stubStampModel struct {
	msg    *schema.Message
	chunks []*schema.Message
}

func (s *stubStampModel) Generate(_ context.Context, _ []*schema.Message,
	_ ...model.Option) (*schema.Message, error) {
	if s.msg != nil {
		return s.msg, nil
	}
	return schema.AssistantMessage("ok", nil), nil
}

func (s *stubStampModel) Stream(_ context.Context, _ []*schema.Message,
	_ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if s.chunks == nil {
		return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("ok", nil)}), nil
	}
	return schema.StreamReaderFromArray(s.chunks), nil
}

func (s *stubStampModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return s, nil
}

func TestStampedModelStampsTheGeneratedMessage(t *testing.T) {
	inner := &stubStampModel{msg: schema.AssistantMessage("答完了", nil)}
	cm := stampModel(inner, "deepseek/deepseek-flash", "high")

	out, err := cm.Generate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := message.ModelRef(out); got != "deepseek/deepseek-flash" {
		t.Fatalf("model = %q", got)
	}
	if got := message.ThinkingTier(out); got != "high" {
		t.Fatalf("thinking = %q", got)
	}
}

func TestStampedModelStampsOnlyTheFirstChunk(t *testing.T) {
	inner := &stubStampModel{chunks: []*schema.Message{
		{Role: schema.Assistant, Content: "a"},
		{Role: schema.Assistant, Content: "b"},
		{Role: schema.Assistant, Content: "c"},
	}}
	cm := stampModel(inner, "deepseek/deepseek-flash", "high")

	sr, err := cm.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []*schema.Message
	for {
		m, err := sr.Recv()
		if err != nil {
			break
		}
		got = append(got, m)
	}
	if len(got) != 3 {
		t.Fatalf("got %d chunks, want 3", len(got))
	}
	if message.ModelRef(got[0]) != "deepseek/deepseek-flash" {
		t.Fatalf("the first chunk must carry the stamp: %v", got[0].Extra)
	}
	for _, m := range got[1:] {
		if message.ModelRef(m) != "" {
			t.Fatalf("only the first chunk may be stamped: %v", m.Extra)
		}
	}
}

func TestTheStampSurvivesTheChunkMergeAsAString(t *testing.T) {
	inner := &stubStampModel{chunks: []*schema.Message{
		{Role: schema.Assistant, Content: "a"},
		{Role: schema.Assistant, Content: "b"},
	}}
	cm := stampModel(inner, "deepseek/deepseek-flash", "high")
	sr, err := cm.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []*schema.Message
	for {
		m, err := sr.Recv()
		if err != nil {
			break
		}
		chunks = append(chunks, m)
	}
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := merged.Extra[message.ExtraModel].(string)
	if !ok {
		t.Fatalf("the merged stamp must stay a string, got %T: %v",
			merged.Extra[message.ExtraModel], merged.Extra[message.ExtraModel])
	}
	if ref != "deepseek/deepseek-flash" {
		t.Fatalf("model = %q", ref)
	}
}

func TestEffectiveTierMatchesTheInjectedTier(t *testing.T) {
	m := &decl.Model{ID: "m", ReasoningDefault: "default"}
	in := CallIntent{Reasoning: "high"}

	ro := RequestOptionsFor(&decl.Provider{}, m, in)
	if len(ro.Fields) != 0 {
		t.Fatalf("an unknown tier must inject nothing, got %v", ro.Fields)
	}
	if got := effectiveTier(m, in); got != "high" {
		t.Fatalf("tier = %q, want high", got)
	}
}

func TestEffectiveTierFallsBackToTheModelDefault(t *testing.T) {
	m := &decl.Model{ID: "m", ReasoningDefault: "default"}
	if got := effectiveTier(m, CallIntent{}); got != "default" {
		t.Fatalf("tier = %q, want default", got)
	}
	if got := effectiveTier(&decl.Model{ID: "m"}, CallIntent{}); got != "" {
		t.Fatalf("tier = %q, want empty", got)
	}
}

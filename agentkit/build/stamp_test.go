package build

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec/decl"
	"openheron/message"
)

// ── 响应消息盖出处 ───────────────────────────────────────────────────────

// stubStampModel 是一个只回固定消息的聊天模型。
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

// ★ Generate 的响应消息要带上模型与档位 —— 事后要能回答「这是谁产的」。
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

// ★ Stream 只盖**第一个** chunk ——
//
//	eino 的 ConcatMessages 会把各 chunk 的 Extra 合并，而 internal.ConcatMaps
//	对同名键是「收集成 []any」。每个 chunk 都盖 ⇒ 合并后值变成切片。
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
	// ★ 只有第一个带戳。
	if message.ModelRef(got[0]) != "deepseek/deepseek-flash" {
		t.Fatalf("the first chunk must carry the stamp: %v", got[0].Extra)
	}
	for _, m := range got[1:] {
		if message.ModelRef(m) != "" {
			t.Fatalf("only the first chunk may be stamped: %v", m.Extra)
		}
	}
}

// ★ 合并之后那个键仍然是**一个字符串**，不是切片 ——
//
//	这是「只盖第一个」的全部理由。
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

// ── 档位的算法只有一处 ───────────────────────────────────────────────────

// ★ 记录的档位必须与**实际注入请求**的档位一致 ——
//
//	两处各算一遍的话，字段会在某次改动后与实际漂移，而那正是它的全部意义。
func TestEffectiveTierMatchesTheInjectedTier(t *testing.T) {
	m := &decl.Model{ID: "m", ReasoningDefault: "default"}
	in := CallIntent{Reasoning: "high"}

	ro := RequestOptionsFor(&decl.Provider{}, m, in)
	// 档位 high 不在词汇表里 ⇒ 不注入任何思考字段。
	if len(ro.Fields) != 0 {
		t.Fatalf("an unknown tier must inject nothing, got %v", ro.Fields)
	}
	// 但**记录**的仍是配置里的那个档位（它说了 high，只是端点不认）。
	if got := effectiveTier(m, in); got != "high" {
		t.Fatalf("tier = %q, want high", got)
	}
}

// agent 没写档位 ⇒ 退到模型的 reasoning_default。
func TestEffectiveTierFallsBackToTheModelDefault(t *testing.T) {
	m := &decl.Model{ID: "m", ReasoningDefault: "default"}
	if got := effectiveTier(m, CallIntent{}); got != "default" {
		t.Fatalf("tier = %q, want default", got)
	}
	// 模型也没有 default ⇒ 空 = 没注入思考字段。
	if got := effectiveTier(&decl.Model{ID: "m"}, CallIntent{}); got != "" {
		t.Fatalf("tier = %q, want empty", got)
	}
}

package message

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// ★ checkpoint 的载荷里**带着消息** —— eino 把中断时的图状态整个编进去
//
//	（compose.InterruptInfo{State: …}，而 *schema.Message 已注册 gob 名
//	`_eino_message`）。于是消息的 Extra 也要能被 gob 编。
//
//	★ 而 gob 编不了「未注册的接口值」：
//
//	同一个键出现在 ≥2 个 chunk 时，eino 的 concatMaps 会把它的值
//	**变成切片**（internal/concat.go:113）—— 而切片类型没注册给 gob，
//	于是 checkpoint 写入直接失败。
//	失败发生在「停机提交 checkpoint」的那一刻，表现为
//	「模型说要调工具 → 直接 RUN_ERROR」，看起来像模型出错，实际是编码器。
//
//	★ openheron 现在是安全的，靠的是两条：
//
//	① 盖章只落在**第一个** chunk 上（先写者胜，见 [StampModel]）；
//	② 盖的值都是 gob 能编的基础类型（string / int64 / bool）。
//
//	这条测试守住 ② —— ① 由 [TestStampsAreFirstWriteWins] 守住。
func TestStampsSurviveTheCheckpointEncoder(t *testing.T) {
	msg := NewAssistant("hi", nil)
	StampAgent(msg, "main")
	StampNow(msg)
	StampSubagent(msg)
	StampReasoningMs(msg, 1234)
	StampModel(msg, "deepseek/deepseek-chat", "high")

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msg); err != nil {
		t.Fatalf("the checkpoint encoder must accept a stamped message: %v", err)
	}
}

// 盖章只落在第一个 chunk 上 —— 这是「合出来的值还认得」的**前提**。
//
// ★ eino 的 concat 对**同键**不是「先写者胜」，而是**把值拼起来**
//
//	（internal/concat.go:113）：字符串首尾相接，其余类型变成切片。
//	于是两个 chunk 都盖了 agent 的话，合出来是 "mainother" ——
//	一个**谁也不属于**的 agent 名，按 agent 过滤时它会单独成组。
//
//	openheron 的做法是只盖第一个 chunk（先写者胜），所以合出来的值是干净的。
//	这条测试把两半都钉住：干净的那条，以及「不干净会变成什么样」。
func TestStampsAreFirstWriteWins(t *testing.T) {
	// ① openheron 的做法：只盖第一个 chunk。
	first := NewAssistant("hi", nil)
	StampAgent(first, "main")
	StampModel(first, "a/b", "high")

	second := NewAssistant("hi", nil)

	merged, err := ConcatChunks([]*schema.Message{first, second})
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	agent, ok := Read(merged)
	if !ok {
		t.Fatal("expected a provenance on the merged message")
	}
	if agent.Agent != "main" {
		t.Fatalf("agent = %q, want main", agent.Agent)
	}
	if agent.Model != "a/b" {
		t.Fatalf("model = %q, want a/b", agent.Model)
	}

	// ★ 而合并之后仍然要能编 —— 那是 checkpoint 的真要求。
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(merged); err != nil {
		t.Fatalf("the checkpoint encoder must accept the merged message: %v", err)
	}

	// ② 反例：两个 chunk 都盖 —— 合出来的值是垃圾。
	//    这条断言的是 eino 的当前行为，是给「为什么只盖第一个」留个证据。
	both1 := NewAssistant("hi", nil)
	StampAgent(both1, "main")
	both2 := NewAssistant("hi", nil)
	StampAgent(both2, "other")

	corrupted, err := ConcatChunks([]*schema.Message{both1, both2})
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	if got := MessageAgent(corrupted); got == "main" {
		t.Fatal("eino no longer concatenates same-key values — " +
			"the first-write-wins rule can be relaxed (and the merged value stays clean either way)")
	}
}

// Extra 里只放**基础类型**：gob 能编它们（Go 预注册了基本类型）。
//
// ★ 这一条不是洁癖：Extra 是 map[string]any，放进去的任何非基础类型
//
//	（比如 []any）都会让 checkpoint 写入失败 —— 而失败发生在
//	「停机提交」那一刻，症状是 RUN_ERROR，离原因很远。
func TestProvenanceValuesAreGobEncodable(t *testing.T) {
	msg := NewAssistant("hi", nil)
	StampAgent(msg, "main")
	StampNow(msg)
	StampSubagent(msg)
	StampReasoningMs(msg, 1234)
	StampModel(msg, "a/b", "high")

	for k, v := range msg.Extra {
		switch v.(type) {
		case string, int64, int, bool, float64, []byte:
			// gob 编得了 ✓
		default:
			t.Fatalf("Extra[%q] has type %T — gob cannot encode an unregistered interface value, "+
				"and the checkpoint write would fail at stop time", k, v)
		}
	}

	// 顺带钉住时间戳的形状：定宽 UTC 字符串（可按字典序排序）。
	ts := TSString(msg)
	if ts == "" {
		t.Fatal("expected a timestamp")
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000000Z07:00", ts); err != nil {
		t.Fatalf("the timestamp must be fixed width: %v", err)
	}
}

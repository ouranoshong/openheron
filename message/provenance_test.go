package message

import (
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestStampAgentRoundTrip(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampAgent(m, "coder")
	if got := MessageAgent(m); got != "coder" {
		t.Fatalf("agent = %q, want coder", got)
	}
}

// 归属是「谁产出的」这个事实 —— 后标的不能推翻先标的。
func TestStampAgentFirstWriteWins(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampAgent(m, "coder")
	StampAgent(m, "reviewer")
	if got := MessageAgent(m); got != "coder" {
		t.Fatalf("agent = %q, want coder (the first stamp wins)", got)
	}
}

// 没有归属标记 = 不知道是谁。老会话（加标记之前写的）属于这一类。
func TestMessageAgentEmptyWhenUnstamped(t *testing.T) {
	if got := MessageAgent(&schema.Message{Role: schema.User}); got != "" {
		t.Fatalf("agent = %q, want empty", got)
	}
	if got := MessageAgent(nil); got != "" {
		t.Fatalf("agent = %q, want empty", got)
	}
}

func TestStampTSRoundTrip(t *testing.T) {
	want := time.Date(2026, 10, 22, 8, 13, 28, 718477470, time.UTC)
	m := &schema.Message{Role: schema.Assistant}
	StampTS(m, want)

	got, ok := MessageTS(m)
	if !ok {
		t.Fatal("the timestamp must be readable")
	}
	if !got.Equal(want) {
		t.Fatalf("ts = %s, want %s", got, want)
	}
}

// ★ 最要紧的一条：存储的格式必须**定宽**，否则字典序 ≠ 时间序。
//
// 这不是洁癖 —— 「按时间排序」正是这个时间戳存在的理由之一。`time.RFC3339Nano`
// 会丢掉末尾的零，于是 "…28.1Z" 与 "…28.100000001Z" 长度不同，字典序比较
// 会把**更晚**的那个排在前面（'0' < 'Z'），排序整个反过来。
func TestTimestampFormatIsFixedWidthAndSortable(t *testing.T) {
	base := time.Date(2026, 10, 22, 8, 13, 28, 0, time.UTC)

	// 两个相差 100ns 的时刻；后者的纳秒位不是 0，前者的全是 0。
	early := base.Add(100 * time.Millisecond)
	late := base.Add(100*time.Millisecond + 100)

	a := &schema.Message{Role: schema.Assistant}
	b := &schema.Message{Role: schema.Assistant}
	StampTS(a, early)
	StampTS(b, late)

	sa, sb := TSString(a), TSString(b)
	if len(sa) != len(sb) {
		t.Fatalf("the two timestamps must have the same width: %q vs %q", sa, sb)
	}
	if !(sa < sb) {
		t.Fatalf("the earlier one must sort first: %q vs %q", sa, sb)
	}

	// ★ 对照组：RFC3339Nano 会怎么排。
	na, nb := early.Format(time.RFC3339Nano), late.Format(time.RFC3339Nano)
	if na < nb {
		t.Fatalf("the premise is wrong: RFC3339Nano happened to sort correctly (%q vs %q)", na, nb)
	}
}

// 一律 UTC：时区偏移会让同一批消息的字符串长度都不一样。
func TestTimestampIsUTC(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	m := &schema.Message{Role: schema.Assistant}
	StampTS(m, time.Date(2026, 10, 22, 16, 13, 28, 0, loc))

	s := TSString(m)
	if s[len(s)-1] != 'Z' {
		t.Fatalf("the timestamp must be in UTC, got %q", s)
	}
}

// 时刻的语义是「产生的那一刻」，不是「归档的那一刻」。
func TestStampTSFirstWriteWins(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	first := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	StampTS(m, first)
	StampTS(m, first.Add(time.Hour))

	got, _ := MessageTS(m)
	if !got.Equal(first) {
		t.Fatalf("ts = %s, want the first stamp %s", got, first)
	}
}

// 没有时间戳 = 不知道什么时候 —— 与「时刻是零值」是两回事。
func TestMessageTSReportsAbsence(t *testing.T) {
	if _, ok := MessageTS(&schema.Message{Role: schema.User}); ok {
		t.Fatal("an unstamped message must report no timestamp")
	}
	if _, ok := MessageTS(nil); ok {
		t.Fatal("a nil message must report no timestamp")
	}
}

// 两个键住在同一个 Extra 里 —— 它们回答的是同一个问题的两半。
func TestBothStampsShareTheExtra(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampAgent(m, "coder")
	StampNow(m)

	if len(m.Extra) != 2 {
		t.Fatalf("Extra must carry exactly the two provenance keys, got %v", m.Extra)
	}
	if _, ok := m.Extra[ExtraAgent]; !ok {
		t.Fatalf("Extra is missing %q: %v", ExtraAgent, m.Extra)
	}
	if _, ok := m.Extra[ExtraTS]; !ok {
		t.Fatalf("Extra is missing %q: %v", ExtraTS, m.Extra)
	}
}

// ── 作用域标记（产生在委派内部） ─────────────────────────────────────────

// 标记与 agent 归属**正交**：一条消息可以既是「coder 产出的」，
// 又是「产生在一次委派里面的」。
func TestSubagentFlagIsIndependentOfTheAgentStamp(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampAgent(m, "coder")
	StampSubagent(m)

	if got := MessageAgent(m); got != "coder" {
		t.Fatalf("agent = %q, want coder", got)
	}
	if !IsSubagent(m) {
		t.Fatal("the scope flag must be readable independently of the agent stamp")
	}
}

// 没打标记的消息一律报 false —— 包括 nil 与老会话。
func TestIsSubagentIsFalseWithoutTheFlag(t *testing.T) {
	if IsSubagent(nil) {
		t.Fatal("a nil message is not a subagent one")
	}
	if IsSubagent(&schema.Message{Role: schema.Assistant}) {
		t.Fatal("a message without the flag must not report as a subagent one")
	}
	m := &schema.Message{Role: schema.Assistant}
	StampAgent(m, "coder")
	if IsSubagent(m) {
		t.Fatal("the agent stamp alone must not imply the scope flag")
	}
}

// 幂等：先写者胜。
func TestStampSubagentIsIdempotent(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampSubagent(m)
	StampSubagent(m)
	if len(m.Extra) != 1 {
		t.Fatalf("Extra = %v, want exactly the scope key", m.Extra)
	}
}

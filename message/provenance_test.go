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

func TestStampAgentFirstWriteWins(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampAgent(m, "coder")
	StampAgent(m, "reviewer")
	if got := MessageAgent(m); got != "coder" {
		t.Fatalf("agent = %q, want coder (the first stamp wins)", got)
	}
}

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

func TestTimestampFormatIsFixedWidthAndSortable(t *testing.T) {
	base := time.Date(2026, 10, 22, 8, 13, 28, 0, time.UTC)

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

	na, nb := early.Format(time.RFC3339Nano), late.Format(time.RFC3339Nano)
	if na < nb {
		t.Fatalf("the premise is wrong: RFC3339Nano happened to sort correctly (%q vs %q)", na, nb)
	}
}

func TestTimestampIsUTC(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	m := &schema.Message{Role: schema.Assistant}
	StampTS(m, time.Date(2026, 10, 22, 16, 13, 28, 0, loc))

	s := TSString(m)
	if s[len(s)-1] != 'Z' {
		t.Fatalf("the timestamp must be in UTC, got %q", s)
	}
}

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

func TestMessageTSReportsAbsence(t *testing.T) {
	if _, ok := MessageTS(&schema.Message{Role: schema.User}); ok {
		t.Fatal("an unstamped message must report no timestamp")
	}
	if _, ok := MessageTS(nil); ok {
		t.Fatal("a nil message must report no timestamp")
	}
}

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

func TestStampSubagentIsIdempotent(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant}
	StampSubagent(m)
	StampSubagent(m)
	if len(m.Extra) != 1 {
		t.Fatalf("Extra = %v, want exactly the scope key", m.Extra)
	}
}

package message

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// The marker has to survive the transcript's JSON round trip: a compaction
// that only lives in memory is the failure the whole feature exists to
// prevent.
func TestCompactionMarkerSurvivesJSON(t *testing.T) {
	msg := &schema.Message{Role: schema.User, Content: "summary of the conversation"}
	StampCompaction(msg, Compaction{Superseded: 12, SavedTokens: 3400, At: time.Now().UTC()})

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var back schema.Message
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}

	c, ok := ReadCompaction(&back)
	if !ok {
		t.Fatal("the compaction marker did not survive the round trip")
	}
	if c.Superseded != 12 || c.SavedTokens != 3400 {
		t.Fatalf("compaction = %+v, want superseded=12 saved=3400", c)
	}
	if c.At.IsZero() {
		t.Fatal("the compaction lost its timestamp")
	}
}

func TestReadCompactionOnAPlainMessage(t *testing.T) {
	if _, ok := ReadCompaction(&schema.Message{Role: schema.User, Content: "hello"}); ok {
		t.Fatal("a plain message must not read as a compaction")
	}
	if _, ok := ReadCompaction(nil); ok {
		t.Fatal("a nil message must not read as a compaction")
	}
}

// The effective view is the suffix that starts at the last compaction.
func TestEffectiveView(t *testing.T) {
	plain := func(text string) *schema.Message {
		return &schema.Message{Role: schema.User, Content: text}
	}
	compacted := func(superseded int) *schema.Message {
		m := plain("summary")
		StampCompaction(m, Compaction{Superseded: superseded})
		return m
	}

	cases := []struct {
		name string
		in   []*schema.Message
		want int
	}{
		{"no compaction keeps everything", []*schema.Message{plain("a"), plain("b")}, 2},
		{"a compaction starts the view", []*schema.Message{plain("a"), compacted(1), plain("b")}, 2},
		{"the newest compaction wins", []*schema.Message{compacted(0), plain("a"), compacted(2), plain("b")}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveView(tc.in); len(got) != tc.want {
				t.Fatalf("effective view has %d messages, want %d", len(got), tc.want)
			}
		})
	}
}

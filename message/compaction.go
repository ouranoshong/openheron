package message

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

// ExtraCompaction marks the message that replaced a stretch of history.
//
// The marker lives in the same Extra map as the provenance stamps: it is
// metadata about a message, and it has to survive the transcript's JSON round
// trip like they do.
const ExtraCompaction = "openheron_compaction"

// Compaction is what a compacted message carries.
//
// It marks the boundary of the effective history: the agent's context starts
// at the last message carrying this marker. Everything before it stays on
// disk — compaction is not deletion — but it is no longer sent to the model.
type Compaction struct {
	// Superseded is how many transcript messages this message replaced.
	Superseded int `json:"superseded"`
	// SavedTokens is how many tokens the compaction is estimated to have
	// removed from the next request. It is an estimate: the trigger counts
	// with eino's counter, which is itself an estimate.
	SavedTokens int `json:"saved_tokens"`
	// At is when the compaction happened.
	At time.Time `json:"at"`
}

// StampCompaction marks a message as a compaction boundary.
func StampCompaction(msg *schema.Message, c Compaction) {
	if msg == nil {
		return
	}
	put(msg, ExtraCompaction, c)
}

// ReadCompaction reads the compaction marker off a message.
//
// The marker survives a JSON round trip as a map, not as the struct it was
// written as, so both shapes are accepted.
func ReadCompaction(msg *schema.Message) (Compaction, bool) {
	if msg == nil || msg.Extra == nil {
		return Compaction{}, false
	}
	switch v := msg.Extra[ExtraCompaction].(type) {
	case Compaction:
		return v, true
	case *Compaction:
		if v == nil {
			return Compaction{}, false
		}
		return *v, true
	case map[string]any:
		return compactionFromMap(v)
	default:
		return Compaction{}, false
	}
}

// IsCompaction reports whether a message is a compaction boundary.
func IsCompaction(msg *schema.Message) bool {
	_, ok := ReadCompaction(msg)
	return ok
}

func compactionFromMap(m map[string]any) (Compaction, bool) {
	c := Compaction{
		Superseded:  intOf(m["superseded"]),
		SavedTokens: intOf(m["saved_tokens"]),
	}
	if s, ok := m["at"].(string); ok && s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			c.At = t
		}
	}
	return c, true
}

// intOf reads a number that may have crossed a JSON boundary.
func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// EffectiveView returns the suffix of a transcript that starts at the last
// compaction message.
//
// A transcript that was never compacted carries no marker, and its effective
// history is the whole thing. A transcript compacted twice keeps only the
// newest boundary: the older summary is itself superseded, and it stays on
// disk as part of the record.
func EffectiveView(msgs []*schema.Message) []*schema.Message {
	start := 0
	for i, m := range msgs {
		if IsCompaction(m) {
			start = i
		}
	}
	return msgs[start:]
}

package message

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

const (
	ExtraAgent       = "openheron_agent"
	ExtraTS          = "openheron_ts"
	ExtraSubagent    = "openheron_subagent"
	ExtraReasoningMs = "openheron_reasoning_ms"
	ExtraModel       = "openheron_model"
	ExtraThinking    = "openheron_thinking"

	// ExtraMessageID is the message's own identity.
	//
	// ★ It lives in Extra because eino's schema.Message has no id field, and
	//   Extra is the one place a message can carry metadata that survives the
	//   transcript's JSON round trip (see StampCompaction for the same shape).
	//
	// ★ It must be a **string**: the checkpoint encodes messages with gob,
	//   and gob cannot encode an unregistered value in an interface — see
	//   the type allow-list in provenance_gob_test.go.
	ExtraMessageID = "openheron_message_id"
)

const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

type Provenance struct {
	Agent       string
	TS          time.Time
	Subagent    bool
	ReasoningMs int64
	Model       string
	Thinking    string
}

func (p Provenance) TSString() string {
	if p.TS.IsZero() {
		return ""
	}
	return p.TS.UTC().Format(tsLayout)
}

func (p Provenance) Stamp(msg *schema.Message) {
	if msg == nil {
		return
	}
	if p.Agent != "" {
		put(msg, ExtraAgent, p.Agent)
	}
	if !p.TS.IsZero() {
		put(msg, ExtraTS, p.TS.UTC().Format(tsLayout))
	}
	if p.Subagent {
		put(msg, ExtraSubagent, true)
	}
	if p.ReasoningMs > 0 {
		put(msg, ExtraReasoningMs, p.ReasoningMs)
	}
	if p.Model != "" {
		put(msg, ExtraModel, p.Model)
	}
	if p.Thinking != "" {
		put(msg, ExtraThinking, p.Thinking)
	}
}

func put(msg *schema.Message, key string, value any) {
	if msg.Extra == nil {
		msg.Extra = map[string]any{}
	}
	if _, ok := msg.Extra[key]; ok {
		return
	}
	msg.Extra[key] = value
}

func Read(msg *schema.Message) (Provenance, bool) {
	if msg == nil || msg.Extra == nil {
		return Provenance{}, false
	}
	p := Provenance{
		Agent:       MessageAgent(msg),
		Subagent:    IsSubagent(msg),
		ReasoningMs: ReasoningMs(msg),
		Model:       ModelRef(msg),
		Thinking:    ThinkingTier(msg),
	}
	if ts, ok := MessageTS(msg); ok {
		p.TS = ts
	}
	_, hasTS := msg.Extra[ExtraTS]
	ok := hasTS || p.Agent != "" || p.Subagent || p.ReasoningMs != 0 ||
		p.Model != "" || p.Thinking != ""
	return p, ok
}

func StampAgent(msg *schema.Message, agent string) {
	Provenance{Agent: agent}.Stamp(msg)
}

func MessageAgent(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	v, _ := msg.Extra[ExtraAgent].(string)
	return v
}

func StampSubagent(msg *schema.Message) {
	Provenance{Subagent: true}.Stamp(msg)
}

func IsSubagent(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	v, _ := msg.Extra[ExtraSubagent].(bool)
	return v
}

func StampReasoningMs(msg *schema.Message, ms int64) {
	Provenance{ReasoningMs: ms}.Stamp(msg)
}

func ReasoningMs(msg *schema.Message) int64 {
	if msg == nil || msg.Extra == nil {
		return 0
	}
	switch v := msg.Extra[ExtraReasoningMs].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func StampTS(msg *schema.Message, t time.Time) {
	Provenance{TS: t}.Stamp(msg)
}

func StampNow(msg *schema.Message) {
	StampTS(msg, time.Now())
}

func TSString(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	s, _ := msg.Extra[ExtraTS].(string)
	return s
}

func MessageTS(msg *schema.Message) (time.Time, bool) {
	if msg == nil || msg.Extra == nil {
		return time.Time{}, false
	}
	s, ok := msg.Extra[ExtraTS].(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(tsLayout, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func StampModel(msg *schema.Message, ref, tier string) {
	Provenance{Model: ref, Thinking: tier}.Stamp(msg)
}

func ModelRef(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	s, _ := msg.Extra[ExtraModel].(string)
	return s
}

func ThinkingTier(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	s, _ := msg.Extra[ExtraThinking].(string)
	return s
}

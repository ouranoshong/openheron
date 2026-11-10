package message

import (
	"github.com/cloudwego/eino/schema"
)

// StampMessageID gives the message its own identity.
//
// ★ The id is written **once**. A second stamp is ignored, so a message that
//
//	is re-stamped on its way through the pipeline keeps the identity it was
//	born with — that is what makes it stable.
//
// ★ It is a string because the checkpoint encodes messages with gob, and gob
//
//	cannot encode a value of an unregistered type inside an interface (see
//	the type allow-list in provenance_gob_test.go).
func StampMessageID(msg *schema.Message, id string) {
	if msg == nil || id == "" {
		return
	}
	put(msg, ExtraMessageID, id)
}

// MessageID returns the message's own identity, or "" when it has none.
//
// ★ An empty result is a normal state, not an error: a transcript written
//
//	before messages carried ids has none, and the reader falls back to
//	deriving one from the message's position.
func MessageID(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	switch v := msg.Extra[ExtraMessageID].(type) {
	case string:
		return v
	default:
		return ""
	}
}

// HasMessageID reports whether the message carries its own identity.
func HasMessageID(msg *schema.Message) bool {
	return MessageID(msg) != ""
}

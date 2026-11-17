package message

import "github.com/cloudwego/eino/schema"

// ExtraToolError marks a tool result whose call failed.
//
// ★ The marker lives in Extra with the provenance stamps: it is metadata
//
//	about a message, and it has to survive the transcript's JSON round trip
//	like they do.
//
// ★ It is a **string** and not a struct: the checkpoint encodes messages
//
//	with gob, and gob cannot encode an unregistered value in an interface.
const ExtraToolError = "openheron_tool_error"

// StampToolError marks a tool result as the result of a failed call.
//
// ★ The reason is kept, not just the fact: the content of a failed call
//
//	already says what the model read, and the reason says what the call
//	tried to do.
func StampToolError(msg *schema.Message, reason string) {
	if msg == nil {
		return
	}
	put(msg, ExtraToolError, reason)
}

// ReadToolError reads the failure off a tool result.
//
// ★ The presence of the key is the fact; the reason inside it is a detail.
//
// ★ The reason survives a JSON round trip as a string, so it is read as one.
func ReadToolError(msg *schema.Message) (string, bool) {
	if msg == nil || msg.Extra == nil {
		return "", false
	}
	switch v := msg.Extra[ExtraToolError].(type) {
	case string:
		return v, true
	case bool:
		return "", v
	default:
		return "", false
	}
}

// IsToolError reports whether a message is the result of a failed call.
func IsToolError(msg *schema.Message) bool {
	_, failed := ReadToolError(msg)
	return failed
}

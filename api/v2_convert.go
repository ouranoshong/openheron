package api

import (
	"strconv"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// toAgUiMessage maps one persisted openheron message onto the AG-UI wire shape.
// index is the message's position in the transcript; it is what makes the
// derived id unique (see stableID).
// The same shape streams back during a run and is served by
// GET /v2/sessions/{id}/messages, so the client feeds both through one
// reducer instead of keeping two view models in sync.
// Returns false for a message that carries nothing renderable.
func toAgUiMessage(m *schema.Message, index int) (types.Message, bool) {
	if m == nil {
		return types.Message{}, false
	}
	out := types.Message{ID: stableID(m, index)}

	prov, _ := message.Read(m)

	if prov.Agent != "" {
		out.Name = prov.Agent
	}

	if ts := prov.TSString(); ts != "" {
		out.Metadata = types.Metadata{"createdAt": ts}
	}
	if reasoning := message.AssistantDeltaReasoning(m); reasoning != "" {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["reasoning"] = reasoning
		if ms := prov.ReasoningMs; ms > 0 {
			out.Metadata["reasoningMs"] = ms
		}
	}
	if prov.Model != "" {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["model"] = prov.Model
	}
	if prov.Thinking != "" {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["thinking"] = prov.Thinking
	}
	// A compacted message is not a chat bubble: it stands for the stretch of
	// conversation it replaced. The marker travels with it so the client can
	// render a divider instead of a message.
	if c, ok := message.ReadCompaction(m); ok {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["compaction"] = map[string]any{
			"superseded":  c.Superseded,
			"savedTokens": c.SavedTokens,
			"at":          c.At.UTC().Format(time.RFC3339Nano),
		}
	}

	switch m.Role {
	case schema.User:
		out.Role = types.RoleUser
		out.Content = message.UserText(m)
	case schema.System:
		out.Role = types.RoleDeveloper
		out.Content = m.Content
	case schema.Tool:
		out.Role = types.RoleTool
		out.Content = message.Text(m)
		out.ToolCallID = m.ToolCallID
	case schema.Assistant, "":
		out.Role = types.RoleAssistant
		out.Content = message.AssistantText(m)
		out.ToolCalls = convertToolCalls(m)
	default:
		out.Role = types.RoleAssistant
		out.Content = message.Text(m)
	}

	// A compaction is worth showing even when it carries nothing else: the
	// marker is the whole point of the message, and the summary behind it is
	// an implementation detail.
	if out.Content == "" && len(out.ToolCalls) == 0 && out.ToolCallID == "" &&
		out.Metadata["reasoning"] == nil && out.Metadata["compaction"] == nil {
		return types.Message{}, false
	}
	return out, true
}

func convertToolCalls(m *schema.Message) []types.ToolCall {
	calls := message.ToolCalls(m)
	if len(calls) == 0 {
		return nil
	}
	out := make([]types.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if tc.Name == "" {
			continue
		}
		id := tc.ID
		if id == "" {
			// The transcript is append-only and a tool call without an id can
			// never be matched to its result; give it a stable derived one so
			// the card still renders instead of vanishing.
			id = "call-" + firstToken(message.Text(m))
		}
		out = append(out, types.ToolCall{
			ID:   id,
			Type: types.ToolCallTypeFunction,
			Function: types.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Args,
			},
		})
	}
	return out
}

// firstToken returns a short identifier fragment used only for synthesised
// tool-call ids.
func firstToken(v string) string {
	const maxRunes = 8
	r := []rune(v)
	if len(r) > maxRunes {
		r = r[:maxRunes]
	}
	out := make([]rune, 0, len(r))
	for _, c := range r {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

// stableID derives a message id from content **and position**, so repeated
// fetches of the same transcript produce the same ids and the client's reducer
// keys stay stable across a reload.
func stableID(m *schema.Message, index int) string {
	var b []byte
	b = append(b, []byte(string(m.Role))...)
	b = append(b, 0)
	b = append(b, []byte(message.Text(m))...)
	b = append(b, 0)
	if m.ToolCallID != "" {
		b = append(b, []byte(m.ToolCallID)...)
	}
	b = append(b, 0)
	b = strconv.AppendInt(b, int64(index), 10)
	return "msg-" + hexOf(b)
}

// hexOf is a tiny FNV-1a hex digest; the ids only need to be stable and
// collision-resistant within one transcript, not globally unique.
func hexOf(b []byte) string {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var h uint64 = offset64
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = digits[h&0xf]
		h >>= 4
	}
	return string(out)
}

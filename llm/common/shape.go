package common

import (
	"github.com/cloudwego/eino/schema"
)

// normalizeWireShape gives every message exactly one shape for its body.
//
// ★ The wire has **one** `content` field, and it is either a string or a
//
//	list — never both. The openai client refuses to marshal a message that
//	carries both ("can't use both Content and MultiContent properties
//	simultaneously"), and the request dies before a single byte is sent.
//
// ★ The rule belongs **here**, at the boundary, and not in the assembly.
//
//	Two shapes reach this point, and only one of them is ours:
//
//	  - A message this turn assembled: the body moved into a text part and
//	    Content was cleared (see the attachment assembly). Already fine.
//
//	  - A message **the transcript already holds**. A session that was
//	    written before the assembly learned to move the body has a message
//	    with a Content *and* a picture part, and the base64 with it. That
//	    history is durable: it cannot be rewritten, and the next question
//	    about it would fail the same way every time.
//
//	    Normalizing here repairs it on the way out, once, for every session
//	    that has one — and it holds for any future path that assembles a
//	    message, because this is the last place the message is touched.
//
// ★ The body moves into a **text part** and Content is cleared, rather than
//
//	the other way round. The parts are the richer shape: they can carry a
//	picture, and a picture without its body is a picture the model cannot
//	place. A message whose Content is dropped and whose parts carry no text
//	would be silently mutilated.
//
// ★ The copy shares the message's Extra map, so the id and the attachment
//
//	list ride along — they are facts about the message, and the copy is the
//	same message as far as they are concerned.
func normalizeWireShape(in []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, len(in))
	for i, m := range in {
		out[i] = oneShape(m)
	}
	return out
}

// oneShape returns the message in its single shape.
func oneShape(m *schema.Message) *schema.Message {
	if m == nil {
		return nil
	}
	// ★ The legacy field is a second list on the same wire field. A message
	//   that carries one of them and a Content has the same conflict, so it
	//   is normalized too — and it is emptied rather than merged, because
	//   the deprecated field has no place on the wire any more.
	if len(m.MultiContent) > 0 && m.Content != "" {
		cp := *m
		cp.Content = ""
		cp.MultiContent = nil
		return &cp
	}
	if len(m.UserInputMultiContent) == 0 || m.Content == "" {
		return m
	}

	cp := *m
	cp.Content = ""
	cp.UserInputMultiContent = append(
		[]schema.MessageInputPart{{
			Type: schema.ChatMessagePartTypeText,
			Text: m.Content,
		}},
		m.UserInputMultiContent...,
	)
	return &cp
}

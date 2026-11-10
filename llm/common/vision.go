package common

import (
	"github.com/cloudwego/eino/schema"
)

// imageFallbackNote is what the model is told when its pictures are taken away.
//
// ★ It is not optional. A model that does not know its picture is gone will
//
//	answer about a picture it never saw — and the user cannot tell that apart
//	from "the model looked at it and found nothing".
const imageFallbackNote = "Your picture attachments were removed from this request: this model does not accept images. " +
	"The files themselves are unchanged, and their paths are listed in the workspace note."

// withoutImages returns a copy of the input with the picture parts taken out,
// and whether there were any.
//
// ★ The copy is what the retry sends. The first request already reached the
//
//	provider and was refused; the second one is the same question without the
//	pictures.
//
// ★ The note rides along as a text part, because the model has to be told —
//
//	a model that silently loses its picture answers about a picture it never
//	saw, and the user has no way to tell.
func withoutImages(in []*schema.Message) ([]*schema.Message, bool) {
	found := false
	out := make([]*schema.Message, len(in))
	for i, m := range in {
		if m == nil {
			out[i] = m
			continue
		}
		kept, dropped := keepTextParts(m.UserInputMultiContent)
		if !dropped && len(m.MultiContent) == 0 {
			out[i] = m
			continue
		}
		found = found || dropped

		cp := *m
		if dropped {
			cp.UserInputMultiContent = append(kept, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeText,
				Text: imageFallbackNote,
			})
		}
		if len(m.MultiContent) > 0 {
			// ★ The deprecated input field. Nothing in openheron produces it,
			//   but a caller may, and a picture in it is a picture too.
			legacy, legacyDropped := keepLegacyTextParts(m.MultiContent)
			if legacyDropped {
				cp.MultiContent = legacy
			}
		}
		out[i] = &cp
	}
	return out, found
}

// keepTextParts drops the picture parts and keeps the rest.
func keepTextParts(parts []schema.MessageInputPart) ([]schema.MessageInputPart, bool) {
	if len(parts) == 0 {
		return parts, false
	}
	kept := make([]schema.MessageInputPart, 0, len(parts))
	dropped := false
	for _, part := range parts {
		if part.Type == schema.ChatMessagePartTypeImageURL {
			dropped = true
			continue
		}
		kept = append(kept, part)
	}
	return kept, dropped
}

// keepLegacyTextParts drops the picture parts of the deprecated input field.
func keepLegacyTextParts(parts []schema.ChatMessagePart) ([]schema.ChatMessagePart, bool) {
	kept := make([]schema.ChatMessagePart, 0, len(parts))
	dropped := false
	for _, part := range parts {
		if part.Type == schema.ChatMessagePartTypeImageURL {
			dropped = true
			continue
		}
		kept = append(kept, part)
	}
	return kept, dropped
}

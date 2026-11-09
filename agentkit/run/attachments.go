package run

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// maxInlineImageBytes bounds the image that is inlined into a request.
//
// ★ It is much smaller than the upload limit on purpose: the upload limit
//
//	bounds a **file**, and this bounds a **request**. A 32 MiB image would
//	become 43 MiB of base64 in the prompt, and it would stay in the
//	context for every later turn of the conversation.
//
// ★ An image over the limit is **not** silently dropped — see
//
//	[inlineImages]. It falls back to a path, and the model is told.
const maxInlineImageBytes = 5 << 20 // 5 MiB

// imageMIMEs are the content types that go into the request as an image.
//
// ★ SVG is not one of them: an SVG is text, and the model can read it with
//
//	read_file like any other text file.
var imageMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/jpg":  true,
	"image/gif":  true,
	"image/webp": true,
}

// isImage reports whether the attachment is an image the model can see.
//
// ★ The type comes from the **server**, which derived it from the file's
//
//	name. A client that called a text file "image/png" would otherwise have
//	the model shown bytes that are not a picture.
func isImage(att message.Attachment) bool {
	if att.MIME != "" {
		return imageMIMEs[strings.ToLower(strings.TrimSpace(att.MIME))]
	}
	switch strings.ToLower(filepath.Ext(att.Path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// attachmentContext is what the assembly needs about the session's files.
type attachmentContext struct {
	// workspace is the session's workspace directory.
	workspace string
}

// assembleAttachments prepares the message for the model.
//
// ★ It returns the message **with its multimodal parts**, and a note for the
//
//		prompt. Two things happen here:
//
//	  - An image becomes an image part, so the model *sees* it. A path would
//	    not do: the model would have to read the file as text, and an image
//	    read as text is noise.
//
//	  - A text file stays a path, and the prompt names it. Inlining the whole
//	    file would spend the context on something the model can read on
//	    demand with read_file — and the tools are already there.
//
// ★ The message is the one the transcript holds, so the assembly **adds**
// parts to it rather than building a copy. The attachment list the
// transcript carries and the parts the model sees are the same message.
func assembleAttachments(ctx context.Context, msg *schema.Message, ac attachmentContext) (string, error) {
	atts, ok := message.ReadAttachments(msg)
	if !ok || len(atts) == 0 {
		return "", nil
	}

	var parts []schema.MessageInputPart
	var note strings.Builder
	for _, att := range atts {
		if !isImage(att) {
			continue
		}
		part, err := inlineImage(ac.workspace, att)
		if err != nil {
			// ★ A failed inline is **not** a failed turn: the file is still
			//   there, and the model can read it as text or fetch it with a
			//   tool. The note says what happened, so the model does not
			//   claim to have seen an image it never saw.
			if note.Len() == 0 {
				note.WriteString("\nThe user attached these files:")
			}
			note.WriteString("\n  - " + att.Name + " (not inlined: " + err.Error() + ")")
			continue
		}
		parts = append(parts, part)
	}

	if len(parts) > 0 {
		msg.UserInputMultiContent = append(msg.UserInputMultiContent, parts...)
	}

	// The text files are named in the prompt, with their absolute paths.
	var texts []message.Attachment
	for _, att := range atts {
		if !isImage(att) {
			texts = append(texts, att)
		}
	}
	if len(texts) > 0 || note.Len() > 0 {
		if note.Len() == 0 {
			note.WriteString("\nThe user attached these files:")
		}
		for _, att := range texts {
			note.WriteString("\n  - " + att.Name + " — " + filepath.Join(ac.workspace, att.Path))
		}
		note.WriteString("\nRead them with read_file if the question needs their content.")
	}
	return note.String(), nil
}

// inlineImage reads an image from the workspace and turns it into a part.
//
// ★ The bytes are read **now**, at request time. They are not stored on the
//
//	message: the transcript is a JSONL log with an 8 MiB line limit, and a
//	base64 image would blow it. The transcript keeps the *reference*, and
//	every request re-reads the file.
func inlineImage(workspace string, att message.Attachment) (schema.MessageInputPart, error) {
	path := filepath.Join(workspace, att.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return schema.MessageInputPart{}, err
	}
	if len(data) > maxInlineImageBytes {
		return schema.MessageInputPart{}, fmt.Errorf("the image is %d bytes, over the %d byte limit",
			len(data), maxInlineImageBytes)
	}
	mime := att.MIME
	if mime == "" {
		mime = message.MIMEOfExt(att.Path)
	}
	// ★ The bytes go in **Base64Data**, with the type in MIMEType — eino
	//   recommends that over a data: URL, and the openai adapter turns it
	//   into the shape the provider expects.
	b64 := base64.StdEncoding.EncodeToString(data)
	return schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{
				Base64Data: &b64,
				MIMEType:   mime,
			},
			Detail: schema.ImageURLDetailAuto,
		},
	}, nil
}

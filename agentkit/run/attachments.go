package run

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// maxInlineImageBytes bounds the picture that is inlined into a request.
//
// ★ It is much smaller than the upload limit on purpose: the upload limit
//
//	bounds a **file**, and this bounds a **request**. A 32 MiB image would
//	become 43 MiB of base64 in the prompt, and it would stay in the
//	context for every later turn of the conversation.
//
// ★ There is no fallback for a picture over the limit: the model is told
//
//	the file cannot be processed, rather than being given a path it would
//	read as text. An image read as text is noise, and a model that believes
//	it saw a picture answers about one it never saw.
const maxInlineImageBytes = 5 << 20 // 5 MiB

// imageMIMEs are the content types that go into the request as an image.
//
// ★ SVG is not one of them: an SVG is text, and it is named by an @ path so
//
//	the model can read it with read_file like any other text file.
var imageMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/jpg":  true,
	"image/gif":  true,
	"image/webp": true,
}

// readableExts are the source and data formats a model can read as text.
//
// ★ The extension is a **second opinion**, not a courtesy: MIMEOfExt maps
//
//	an unknown extension to application/octet-stream, and a .go or .py file
//	would otherwise be refused as binary. The MIME decides first; when it
//	says octet-stream, the extension still gets a say.
var readableExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".rst": true, ".adoc": true,
	".log": true, ".csv": true, ".tsv": true, ".json": true, ".jsonl": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true,
	".conf": true, ".env": true, ".xml": true, ".html": true, ".htm": true,
	".css": true, ".svg": true, ".sql": true, ".graphql": true, ".proto": true,
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".java": true, ".kt": true, ".kts": true, ".scala": true, ".c": true,
	".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".rs": true,
	".rb": true, ".php": true, ".sh": true, ".bash": true, ".ps1": true,
	".bat": true, ".cmd": true, ".lua": true, ".swift": true, ".dart": true,
	".vue": true, ".svelte": true, ".ex": true, ".exs": true, ".erl": true,
	".hs": true, ".clj": true, ".tf": true, ".m": true, ".mm": true,
	".r": true, ".pl": true, ".tex": true, ".gitignore": true,
}

// isImage reports whether the attachment is a picture the model can see.
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

// isReadableText reports whether an attachment may be named by an @ path.
//
// ★ A binary attachment is **not** named: giving the model a path to a PDF
//
//	invites it to read a binary file as text, which produces noise that looks
//	like content. The prompt says plainly that the file cannot be processed
//	yet; handling binary attachments is a later change.
func isReadableText(att message.Attachment) bool {
	mime := strings.ToLower(strings.TrimSpace(att.MIME))
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/yaml", "application/x-yaml",
		"application/xml", "application/javascript", "application/toml":
		return true
	}
	return readableExts[strings.ToLower(filepath.Ext(att.Path))]
}

// attachmentContext is what the assembly needs about the session's files.
type attachmentContext struct {
	// workspace is the session's workspace directory.
	workspace string
}

// mentionPath renders one absolute path as an @ token.
//
// ★ The path is **absolute** on purpose. fsx resolves a relative path against
//
//	the project root, not the session workspace, so a workspace-relative name
//	would send read_file to the wrong file — possibly a same-named file in the
//	project. See agentkit/fsx/fsx.go.
//
// ★ ToSlash keeps the token readable on every platform: a Windows path in a
//
//	prompt is a string the model has to copy into a JSON argument, and a
//	backslash there is an escape character. Go accepts forward slashes on
//	Windows.
//
// ★ A path with whitespace is quoted, the same form the composer inserts.
//
//	The context prompt tells the model to drop the quotes before calling
//	read_file.
func mentionPath(abs string) string {
	p := filepath.ToSlash(abs)
	if strings.ContainsAny(p, " \t") {
		return `@"` + p + `"`
	}
	return "@" + p
}

// attachmentPaths returns the @ header for a message and the names of the
// files that cannot be processed.
//
// ★ Pictures are skipped here: they travel as image parts, not as paths.
//
//	An @ path would make the model read picture bytes as text.
//
// ★ The header belongs to the **message**, so it is rebuilt for history too:
//
//	a later "what did that file say?" must still see which file that was.
//	The transcript keeps only the relative path in Extra; the absolute path
//	is derived here, at request time, from the session's workspace.
func attachmentPaths(msg *schema.Message, ac attachmentContext) (string, []string) {
	atts, ok := message.ReadAttachments(msg)
	if !ok || len(atts) == 0 {
		return "", nil
	}
	var lines []string
	var blocked []string
	for _, att := range atts {
		if isImage(att) {
			continue
		}
		if !isReadableText(att) {
			blocked = append(blocked, fmt.Sprintf("  - %s (%s)", att.Name, blockedReason(att)))
			continue
		}
		lines = append(lines, mentionPath(filepath.Join(ac.workspace, att.Path)))
	}
	if len(lines) == 0 {
		return "", blocked
	}
	return strings.Join(lines, "\n") + "\n\n", blocked
}

// blockedReason says why an attachment is not available, without pretending
// the model could do something about it.
func blockedReason(att message.Attachment) string {
	mime := strings.TrimSpace(att.MIME)
	if mime == "" || mime == "application/octet-stream" {
		return "binary or unknown type; not supported yet"
	}
	return mime + "; not supported yet"
}

// blockedNote is the part of the context prompt that names the files the
// model must not answer about.
func blockedNote(blocked []string) string {
	if len(blocked) == 0 {
		return ""
	}
	return "\nThe user attached files that cannot be processed yet:\n" + strings.Join(blocked, "\n") +
		"\nThese files are not available to you. Say so plainly; do not claim to have read them, " +
		"and do not try to read them as text."
}

// presentMessage returns the copy the model sees: the @ header first, then
// the user's own words, then any pictures.
//
// ★ The copy is what keeps the transcript clean — and the UI with it. The
//
//	client renders UserText(msg.Content), so writing the @ header into the
//	persisted message would show machine paths in the user's own bubble.
//
// ★ A message with a picture must carry **one** wire shape: the body moves
//
//	into a text part and Content is cleared. See llm/common/shape.go.
func presentMessage(msg *schema.Message, header string, images []schema.MessageInputPart) *schema.Message {
	if header == "" && len(images) == 0 {
		return msg
	}
	cp := *msg
	body := header + msg.Content
	if len(images) == 0 {
		cp.Content = body
		return &cp
	}
	cp.Content = ""
	parts := make([]schema.MessageInputPart, 0, len(images)+1)
	if body != "" {
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeText,
			Text: body,
		})
	}
	cp.UserInputMultiContent = append(parts, images...)
	return &cp
}

// assembleAttachments prepares the message the model sees for the current
// turn and the note that belongs to this turn's context prompt.
//
// ★ Non-picture text files are **not** inlined. Their absolute paths go into
//
//	the @ header and the model reads them with read_file. There is no
//	second request without the files, and no text-part copy of their bytes:
//	the fallback chain was removed on purpose.
func assembleAttachments(msg *schema.Message, ac attachmentContext) (string, *schema.Message) {
	header, blocked := attachmentPaths(msg, ac)
	images := inlineImages(msg, ac, &blocked)
	return blockedNote(blocked), presentMessage(msg, header, images)
}

// inlineImages turns the pictures of one message into image parts.
//
// ★ A picture that cannot be inlined is **not** given a path: it is named in
//
//	the blocked list instead. A path would invite read_file on image bytes,
//	and the model would answer about a picture it never saw.
func inlineImages(msg *schema.Message, ac attachmentContext, blocked *[]string) []schema.MessageInputPart {
	atts, ok := message.ReadAttachments(msg)
	if !ok {
		return nil
	}
	var parts []schema.MessageInputPart
	for _, att := range atts {
		if !isImage(att) {
			continue
		}
		part, err := inlineImage(ac.workspace, att)
		if err != nil {
			*blocked = append(*blocked, fmt.Sprintf("  - %s (cannot be processed: %s)", att.Name, err))
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

// inlineImage reads a picture from the workspace and turns it into a part.
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

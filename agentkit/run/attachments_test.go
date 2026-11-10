package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// pngBytes is a tiny valid PNG header — the content does not matter, only
// that the file is there and has the right size.
var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 13}

// writeFile puts a file into the workspace and returns its relative name.
func writeFile(t *testing.T, ws, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(ws, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// ★ A text file is named in the prompt, with its absolute path.
//
//	The model reads it on demand with read_file: inlining the whole file
//	would spend the context on something the tools can fetch.
func TestATextAttachmentIsNamedInThePrompt(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "notes.md", []byte("hello"))

	msg := schema.UserMessage("what does it say?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "notes.md", Path: "notes.md", Size: 5, MIME: "text/markdown"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	system := in.Messages[0].Content
	if !strings.Contains(system, "notes.md") {
		t.Fatalf("the prompt must name the attachment: %s", system)
	}
	if !strings.Contains(system, filepath.Join(ws, "notes.md")) {
		t.Fatalf("the prompt must carry the absolute path: %s", system)
	}
	if len(in.Messages[1].UserInputMultiContent) != 0 {
		t.Fatal("a text file must not become a multimodal part")
	}
}

// ★ An image becomes a multimodal part, so the model *sees* it.
//
//	A path would not do: the model would have to read the image as text, and
//	an image read as text is noise.
func TestAnImageAttachmentBecomesAMultimodalPart(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "shot.png", pngBytes)

	msg := schema.UserMessage("what is in this picture?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: int64(len(pngBytes)), MIME: "image/png"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	parts := in.Messages[1].UserInputMultiContent
	// ★ The text is a part too: the wire has one `content` field, and it is
	//   either a string or a list — never both.
	if len(parts) != 2 {
		t.Fatalf("the message carries %d parts, want the text and the picture", len(parts))
	}
	if parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("the part is a %q, want an image", parts[1].Type)
	}
	if parts[1].Image == nil || parts[1].Image.Base64Data == nil {
		t.Fatal("the image part carries no data")
	}
	if parts[1].Image.MIMEType != "image/png" {
		t.Fatalf("mime = %q, want image/png", parts[1].Image.MIMEType)
	}
	if *parts[1].Image.Base64Data == "" {
		t.Fatal("the image carries no bytes")
	}
}

// ★ The message the model sees must not carry its text **and** its parts.
//
//	★ The wire has one `content` field that is either a string or a list,
//	  and the openai adapter builds both when a message carries a Content
//	  and a multi-content at the same time. The client then refuses to
//	  marshal the request: "can't use both Content and MultiContent
//	  properties simultaneously" — and the turn dies on a picture the user
//	  attached.
//
//	So the text moves into a text part and Content is cleared.
func TestAMessageWithAPictureCarriesNoContentBesideItsParts(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "shot.png", pngBytes)

	msg := schema.UserMessage("what is in this picture?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: int64(len(pngBytes)), MIME: "image/png"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	model := in.Messages[1]
	if model.Content != "" {
		t.Fatalf("the message still carries its text: %q", model.Content)
	}
	parts := model.UserInputMultiContent
	if len(parts) != 2 {
		t.Fatalf("the message carries %d parts, want the text and the picture", len(parts))
	}
	// ★ The text comes first: it is the question, and the picture is what
	//   it is about.
	if parts[0].Type != schema.ChatMessagePartTypeText {
		t.Fatalf("the first part is a %q, want the text", parts[0].Type)
	}
	if parts[0].Text != "what is in this picture?" {
		t.Fatalf("the text part carries %q", parts[0].Text)
	}
	if parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("the second part is a %q, want the picture", parts[1].Type)
	}
}

// ★ The transcript's message must not carry the base64.
//
//	★ The transcript is a JSONL log with an 8 MiB line limit, and a 2 MiB
//	  picture is 2.7 MiB of base64. The transcript keeps the *reference*;
//	  every request re-reads the file.
func TestTheTranscriptMessageCarriesNoBase64(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "shot.png", pngBytes)

	msg := schema.UserMessage("what is in this picture?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: int64(len(pngBytes)), MIME: "image/png"},
	})

	if _, err := (InputConfig{}).buildInput(context.Background(), inputParams{Workspace: ws}, msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.UserInputMultiContent) != 0 {
		t.Fatalf("the persisted message carries %d parts", len(msg.UserInputMultiContent))
	}
	if msg.Content != "what is in this picture?" {
		t.Fatalf("the persisted message lost its text: %q", msg.Content)
	}
	// ★ The stamps are still there: the copy shares them.
	if _, ok := message.ReadAttachments(msg); !ok {
		t.Fatal("the persisted message lost its attachment list")
	}
}

// ★ A turn with only a picture and no text still asks the question.
//
//	The text part is absent, and the picture is alone in the list.
func TestAPictureOnlyTurnStillAsks(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "shot.png", pngBytes)

	msg := schema.UserMessage("")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: int64(len(pngBytes)), MIME: "image/png"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	parts := in.Messages[1].UserInputMultiContent
	if len(parts) != 1 || parts[0].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("the message carries %d parts, want the picture alone", len(parts))
	}
}

// ★ An image over the limit falls back to a path — and the model is told.
//
//	★ The point is that it is **not** silent: a model that believes it saw
//	  an image answers about an image it never saw.
func TestAnOversizedImageFallsBackToAPathAndSaysSo(t *testing.T) {
	ws := t.TempDir()
	// Just over the limit.
	big := make([]byte, maxInlineImageBytes+1)
	writeFile(t, ws, "huge.png", big)

	msg := schema.UserMessage("what is in this picture?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "huge.png", Path: "huge.png", Size: int64(len(big)), MIME: "image/png"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Messages[1].UserInputMultiContent) != 0 {
		t.Fatal("an oversized image must not be inlined")
	}
	system := in.Messages[0].Content
	if !strings.Contains(system, "huge.png") {
		t.Fatalf("the prompt must still name the file: %s", system)
	}
	if !strings.Contains(system, "not inlined") {
		t.Fatalf("the prompt must say it was not inlined: %s", system)
	}
}

// ★ A missing file does not fail the turn.
//
//	The attachment was resolved when the request arrived, so this is the
//	race where the file disappears in between. The turn still answers: the
//	model is told the file is not there, and the question is still asked.
func TestAMissingFileDoesNotFailTheTurn(t *testing.T) {
	ws := t.TempDir()

	msg := schema.UserMessage("what does it say?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "gone.md", Path: "gone.md", Size: 5},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatalf("a missing file must not fail the turn: %v", err)
	}
	if len(in.Messages) < 2 {
		t.Fatal("the input must still carry the question")
	}
}

// ★ A message with no attachments is untouched.
func TestAMessageWithoutAttachmentsIsUnchanged(t *testing.T) {
	ws := t.TempDir()
	msg := schema.UserMessage("hello")

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Messages[1].UserInputMultiContent) != 0 {
		t.Fatal("a message with no attachments must carry no parts")
	}
	if strings.Contains(in.Messages[0].Content, "attached") {
		t.Fatalf("the prompt must not mention attachments: %s", in.Messages[0].Content)
	}
}

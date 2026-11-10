package run

import (
	"context"
	"encoding/base64"
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

// abs is the slash form the @ header uses.
func abs(ws, name string) string {
	return filepath.ToSlash(filepath.Join(ws, name))
}

// ★ A text file is **not** inlined. Its absolute path goes into the @ header,
//
//	and the model reads it with read_file. The transcript keeps the user's own
//	words and the relative path in Extra — the header is a view, not a rewrite.
func TestATextAttachmentBecomesAnAtPath(t *testing.T) {
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
	model := in.Messages[1]
	if !strings.HasPrefix(model.Content, "@"+abs(ws, "notes.md")+"\n\n") {
		t.Fatalf("the header must name the absolute path first: %q", model.Content)
	}
	if !strings.HasSuffix(model.Content, "what does it say?") {
		t.Fatalf("the user's words must follow the header: %q", model.Content)
	}
	if len(model.UserInputMultiContent) != 0 {
		t.Fatalf("a text attachment must not become a part: %d parts", len(model.UserInputMultiContent))
	}

	if msg.Content != "what does it say?" {
		t.Fatalf("the transcript lost its text: %q", msg.Content)
	}
	if len(msg.UserInputMultiContent) != 0 {
		t.Fatal("the transcript gained parts")
	}
	atts, ok := message.ReadAttachments(msg)
	if !ok || len(atts) != 1 || atts[0].Path != "notes.md" {
		t.Fatalf("Extra must keep the workspace-relative path: %+v", atts)
	}
}

// ★ The rules are always present, because a history message can carry an @
//
//	header even when the current turn has no attachments at all.
func TestTheRulesExplainTheHeaderAndChunkedReading(t *testing.T) {
	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: t.TempDir()},
		schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	system := in.Messages[0].Content
	for _, want := range []string{"MUST call read_file", "offset and limit", "2000 lines", "cannot be processed yet"} {
		if !strings.Contains(system, want) {
			t.Fatalf("the prompt must say %q: %s", want, system)
		}
	}
	if strings.Contains(system, "The user attached files that cannot be processed yet") {
		t.Fatal("a turn without attachments must not name any blocked file")
	}
}

// ★ Every readable file gets its own @ line, in the order the user sent them.
//
//	★ The MIME says octet-stream for a .go file and an SVG; the extension is
//	  the second opinion that keeps both readable.
func TestEveryReadableFileGetsItsOwnAtLine(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "notes.md", []byte("hello"))
	writeFile(t, ws, "main.go", []byte("package main"))

	msg := schema.UserMessage("explain these")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "notes.md", Path: "notes.md", Size: 5, MIME: "text/markdown"},
		{Name: "main.go", Path: "main.go", Size: 12, MIME: "application/octet-stream"},
		{Name: "logo.svg", Path: "logo.svg", Size: 3, MIME: "application/octet-stream"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	content := in.Messages[1].Content
	first := strings.Index(content, abs(ws, "notes.md"))
	second := strings.Index(content, abs(ws, "main.go"))
	third := strings.Index(content, abs(ws, "logo.svg"))
	if first < 0 || second < 0 || third < 0 || !(first < second && second < third) {
		t.Fatalf("the @ lines must all appear, in attachment order: %q", content)
	}
	if strings.Count(content, "@") != 3 {
		t.Fatalf("want three @ paths, got %q", content)
	}
}

// ★ A path with whitespace is quoted, the same form the composer inserts.
func TestAPathWithSpacesIsQuoted(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "my notes.md", []byte("hello"))

	msg := schema.UserMessage("what does it say?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "my notes.md", Path: "my notes.md", Size: 5, MIME: "text/markdown"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `@"` + abs(ws, "my notes.md") + `"`
	if !strings.HasPrefix(in.Messages[1].Content, want) {
		t.Fatalf("the quoted path must lead the message: %q", in.Messages[1].Content)
	}
}

// ★ A binary attachment is **not** named by an @ path. Giving the model a
//
//	path to a PDF invites it to read binary bytes as text, which produces
//	noise that looks like content. The prompt says it cannot be processed.
func TestABinaryAttachmentIsNamedAsUnprocessable(t *testing.T) {
	ws := t.TempDir()
	msg := schema.UserMessage("what is in the archive?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "bundle.zip", Path: "bundle.zip", Size: 100, MIME: "application/zip"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	model := in.Messages[1]
	if strings.HasPrefix(model.Content, "@") {
		t.Fatalf("a binary file must not get an @ path: %q", model.Content)
	}
	if model.Content != "what is in the archive?" {
		t.Fatalf("the user's words must be untouched: %q", model.Content)
	}
	system := in.Messages[0].Content
	if !strings.Contains(system, "bundle.zip") || !strings.Contains(system, "cannot be processed yet") {
		t.Fatalf("the prompt must say the file is unavailable: %s", system)
	}
}

// ★ A picture still becomes a multimodal part, so the model *sees* it.
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
	if *parts[1].Image.Base64Data != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Fatal("the image carries the wrong bytes")
	}
}

// ★ The header precedes the question even when a picture is present: the
//
//	wire has one content field, so the body moves into the first text part.
func TestAnAtHeaderAndAPictureShareOneWireShape(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "shot.png", pngBytes)

	msg := schema.UserMessage("what is in this picture?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: int64(len(pngBytes)), MIME: "image/png"},
		{Name: "notes.md", Path: "notes.md", Size: 5, MIME: "text/markdown"},
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
	if len(parts) != 2 || parts[0].Type != schema.ChatMessagePartTypeText || parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("want a text part and a picture, got %d parts: %+v", len(parts), parts)
	}
	if !strings.HasPrefix(parts[0].Text, "@"+abs(ws, "notes.md")+"\n\nwhat is in this picture?") {
		t.Fatalf("the @ header must lead the text part: %q", parts[0].Text)
	}
}

// ★ There is no fallback for a picture over the limit: the model is told the
//
//	file cannot be processed, and is not given a path to read as text.
func TestAnOversizedImageIsNamedAsUnprocessable(t *testing.T) {
	ws := t.TempDir()
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
	if strings.Contains(in.Messages[1].Content, "@") {
		t.Fatalf("an oversized image must not get an @ path: %q", in.Messages[1].Content)
	}
	system := in.Messages[0].Content
	if !strings.Contains(system, "huge.png") || !strings.Contains(system, "cannot be processed") {
		t.Fatalf("the prompt must say the picture is unavailable: %s", system)
	}
}

// ★ A turn with only a file and no text still asks the question: the @ header
//
//	is the whole message, and the model knows what to read.
func TestAFileOnlyTurnStillAsks(t *testing.T) {
	ws := t.TempDir()
	msg := schema.UserMessage("")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "notes.md", Path: "notes.md", Size: 5, MIME: "text/markdown"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{Workspace: ws}, msg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(in.Messages[1].Content, "@"+abs(ws, "notes.md")) {
		t.Fatalf("the @ header is the question: %q", in.Messages[1].Content)
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
	if _, ok := message.ReadAttachments(msg); !ok {
		t.Fatal("the persisted message lost its attachment list")
	}
}

// ★ A picture alone, with no text, is still a question the model can see.
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

// ★ A missing file does not fail the turn.
//
//	The attachment was resolved when the request arrived, so this is the
//	race where the file disappears in between. The @ path is still named;
//	read_file will tell the model the file is gone, and the user too.
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
	if !strings.HasPrefix(in.Messages[1].Content, "@") {
		t.Fatalf("the @ path must still be named: %q", in.Messages[1].Content)
	}
}

// ★ "That file you just sent" only works if the header is rebuilt for history.
//
//	★ Extra is not part of what the model sees, and the transcript never
//	  stores the absolute path. Without this pass the later question would
//	  name no file at all.
func TestAnEarlierMessagesAtHeaderIsRebuilt(t *testing.T) {
	ws := t.TempDir()
	earlier := schema.UserMessage("here are the notes")
	message.StampAttachments(earlier, []message.Attachment{
		{Name: "notes.md", Path: "notes.md", Size: 5, MIME: "text/markdown"},
	})

	in, err := InputConfig{}.buildInput(context.Background(), inputParams{
		History:   []*schema.Message{earlier},
		Workspace: ws,
	}, schema.UserMessage("what did that file say?"))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Messages) != 3 {
		t.Fatalf("want system, history and query, got %d messages", len(in.Messages))
	}
	if !strings.HasPrefix(in.Messages[1].Content, "@"+abs(ws, "notes.md")) {
		t.Fatalf("the earlier message lost its @ header: %q", in.Messages[1].Content)
	}
	if earlier.Content != "here are the notes" {
		t.Fatalf("the persisted history was rewritten: %q", earlier.Content)
	}
	if strings.Contains(in.Messages[2].Content, "@") {
		t.Fatalf("the current turn has no attachments of its own: %q", in.Messages[2].Content)
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
	if in.Messages[1] != msg {
		t.Fatal("a message with no attachments must be the same object")
	}
	if in.Messages[1].Content != "hello" {
		t.Fatalf("the message gained a header: %q", in.Messages[1].Content)
	}
}

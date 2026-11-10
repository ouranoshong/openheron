package common

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// bothShapesMessage is a message that carries a body and a picture at the
// same time.
//
// ★ This is the shape a transcript written **before** the assembly learned to
//
//	move the body into a text part holds. It is durable: the session cannot
//	rewrite its own history, so every later question about it would fail the
//	same way.
func bothShapesMessage() *schema.Message {
	b64 := "aGVsbG8="
	return &schema.Message{
		Role:    schema.User,
		Content: "what is in this picture?",
		UserInputMultiContent: []schema.MessageInputPart{
			{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						Base64Data: &b64,
						MIMEType:   "image/png",
					},
				},
			},
		},
	}
}

// recordingServer keeps the request bodies it was given.
type recordingServer struct {
	mu     sync.Mutex
	bodies []string
}

func (s *recordingServer) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, string(raw))
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
		"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"m",
		"choices":[{"index":0,"message":{"role":"assistant","content":"the answer"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

func (s *recordingServer) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return ""
	}
	return s.bodies[0]
}

// newRecordingModel wires a model to a server that records what it was sent.
func newRecordingModel(t *testing.T, srv *recordingServer) *ChatModel {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	t.Cleanup(ts.Close)
	cm, err := New(context.Background(), &Config{
		ProviderID: "deepseek",
		APIKey:     "k",
		BaseURL:    ts.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cm
}

// ★ A message with both shapes reaches the wire in one shape.
//
//	★ This is the whole point of the normalization: the client refuses to
//	  marshal a message that carries a string and a list at the same time,
//	  and it does so before the request is sent — so the turn dies on the
//	  spot, with no answer and no error the user can act on.
func TestAMessageWithBothShapesReachesTheWireInOneShape(t *testing.T) {
	srv := &recordingServer{}
	cm := newRecordingModel(t, srv)

	if _, err := cm.Generate(context.Background(), []*schema.Message{bothShapesMessage()}); err != nil {
		t.Fatalf("the turn must not die on a message the history holds: %v", err)
	}

	body := srv.body()
	if !strings.Contains(body, "image_url") {
		t.Fatalf("the picture was dropped:\n%s", body)
	}
	if !strings.Contains(body, "what is in this picture?") {
		t.Fatalf("the body was dropped:\n%s", body)
	}

	// ★ The `content` field is a list, and it holds the body and the picture.
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("the request does not parse: %v", err)
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("the request carries %d messages, want 1", len(payload.Messages))
	}
	parts, ok := payload.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("the content is %T, want a list", payload.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("the content holds %d parts, want the body and the picture", len(parts))
	}
}

// ★ The body moves into a text part, and the picture keeps its place.
//
//	★ The parts are the richer shape: a picture without its body is a
//	  picture the model cannot place.
func TestTheBodyMovesIntoAPartAndThePictureKeepsItsPlace(t *testing.T) {
	out := normalizeWireShape([]*schema.Message{bothShapesMessage()})
	m := out[0]
	if m.Content != "" {
		t.Fatalf("the message still carries its body: %q", m.Content)
	}
	if len(m.UserInputMultiContent) != 2 {
		t.Fatalf("the message carries %d parts, want the body and the picture", len(m.UserInputMultiContent))
	}
	if m.UserInputMultiContent[0].Type != schema.ChatMessagePartTypeText {
		t.Fatalf("the first part is %q, want the text", m.UserInputMultiContent[0].Type)
	}
	if m.UserInputMultiContent[0].Text != "what is in this picture?" {
		t.Fatalf("the body is %q", m.UserInputMultiContent[0].Text)
	}
	if m.UserInputMultiContent[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("the second part is %q, want the picture", m.UserInputMultiContent[1].Type)
	}
}

// ★ The original is left alone.
//
//	★ The caller holds the message the transcript keeps; the normalization
//	  makes a copy for the wire and must not touch the one it was given.
func TestTheNormalizationLeavesTheOriginalAlone(t *testing.T) {
	original := bothShapesMessage()
	normalizeWireShape([]*schema.Message{original})

	if original.Content != "what is in this picture?" {
		t.Fatalf("the original lost its body: %q", original.Content)
	}
	if len(original.UserInputMultiContent) != 1 {
		t.Fatalf("the original was modified: %d parts", len(original.UserInputMultiContent))
	}
}

// ★ A message that already has one shape is not copied.
//
//	★ The common case is a plain text message, and the common case should
//	  cost nothing: no copy, no allocation, no surprise.
func TestAMessageWithOneShapeIsNotCopied(t *testing.T) {
	plain := schema.UserMessage("hello")
	out := normalizeWireShape([]*schema.Message{plain})
	if out[0] != plain {
		t.Fatal("a plain message must be passed through as it is")
	}

	// ★ The shape the assembly produces is passed through too.
	assembled := pictureMessage()
	out = normalizeWireShape([]*schema.Message{assembled})
	if out[0] != assembled {
		t.Fatal("an assembled message must be passed through as it is")
	}
}

// ★ The stamps ride along.
//
//	★ The id and the attachment list are facts about the message, and the
//	  copy on the wire is the same message as far as they are concerned.
func TestTheStampsRideAlong(t *testing.T) {
	original := bothShapesMessage()
	original.Extra = map[string]any{"openheron_message_id": "abc-123"}

	out := normalizeWireShape([]*schema.Message{original})
	id, ok := out[0].Extra["openheron_message_id"]
	if !ok || id != "abc-123" {
		t.Fatalf("the stamp was lost: %v", out[0].Extra)
	}
}

// ★ The deprecated list is emptied rather than merged.
//
//	★ `MultiContent` is the older name for the same wire field, and the
//	  client refuses it beside a body as well. It has no place on the wire
//	  any more, so it goes — the body stays, and nothing is invented.
func TestTheDeprecatedListIsEmptied(t *testing.T) {
	legacy := &schema.Message{
		Role:    schema.User,
		Content: "the question",
		MultiContent: []schema.ChatMessagePart{
			{Type: schema.ChatMessagePartTypeText, Text: "the question"},
		},
	}

	out := normalizeWireShape([]*schema.Message{legacy})
	if out[0].Content != "" {
		t.Fatalf("the message still carries its body: %q", out[0].Content)
	}
	if len(out[0].MultiContent) != 0 {
		t.Fatalf("the deprecated list survived: %d parts", len(out[0].MultiContent))
	}
}

// ★ A nil message is passed through.
//
//	★ The history can hold a nil, and a nil must not become a message.
func TestANilMessageIsPassedThrough(t *testing.T) {
	out := normalizeWireShape([]*schema.Message{nil})
	if out[0] != nil {
		t.Fatal("a nil message must stay nil")
	}
}

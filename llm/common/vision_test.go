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
	goopenai "github.com/meguminnnnnnnnn/go-openai"
)

// pictureMessage is a user message with a picture in it.
//
// ★ The text is empty and the text lives in a part: that is the shape the
//
//	openai adapter produces, and it is the shape that has to survive the
//	wire (the `content` field is either a string or a list).
func pictureMessage() *schema.Message {
	b64 := "aGVsbG8="
	return &schema.Message{
		Role:    schema.User,
		Content: "",
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "what is in this picture?"},
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

// pictureFreeServer is a provider that refuses pictures and answers text.
//
// ★ It is what a model with no vision looks like from the outside: the
//
//	request comes back refused, and the same request without the picture
//	comes back answered.
type pictureFreeServer struct {
	mu       sync.Mutex
	bodies   []string
	requests int
	// refuseBoth makes the retry fail too, so the first error is the one
	// that has to be reported.
	refuseBoth bool
}

func (s *pictureFreeServer) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, string(raw))
	s.requests++
	refuseBoth := s.refuseBoth
	s.mu.Unlock()

	if strings.Contains(string(raw), "image_url") || refuseBoth {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"this model does not support image input","type":"invalid_request_error"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
		"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"m",
		"choices":[{"index":0,"message":{"role":"assistant","content":"the answer"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

func (s *pictureFreeServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *pictureFreeServer) body(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.bodies) {
		return ""
	}
	return s.bodies[i]
}

// newPictureFreeModel wires a model to a provider that refuses pictures.
func newPictureFreeModel(t *testing.T, srv *pictureFreeServer) *ChatModel {
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

// ★ A model with no vision must not kill the turn.
//
//	★ The picture is refused, the same question without it is answered, and
//	  the user gets an answer instead of an error about their attachment.
func TestAModelWithoutVisionStillAnswers(t *testing.T) {
	srv := &pictureFreeServer{}
	cm := newPictureFreeModel(t, srv)

	out, err := cm.Generate(context.Background(), []*schema.Message{pictureMessage()})
	if err != nil {
		t.Fatalf("the turn must not die on a picture: %v", err)
	}
	if out == nil || out.Content != "the answer" {
		t.Fatalf("the model did not answer: %+v", out)
	}
	if srv.count() != 2 {
		t.Fatalf("the provider saw %d requests, want the refused one and the retry", srv.count())
	}
}

// ★ The retry must not carry the picture.
//
//	★ The second request is the same question without the picture; a picture
//	  in it would be refused again.
func TestTheRetryCarriesNoPicture(t *testing.T) {
	srv := &pictureFreeServer{}
	cm := newPictureFreeModel(t, srv)

	if _, err := cm.Generate(context.Background(), []*schema.Message{pictureMessage()}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(srv.body(0), "image_url") {
		t.Fatal("the first request must carry the picture")
	}
	if strings.Contains(srv.body(1), "image_url") {
		t.Fatalf("the retry still carries the picture:\n%s", srv.body(1))
	}
}

// ★ The model must be told that its picture is gone.
//
//	★ A model that does not know will answer about a picture it never saw,
//	  and the user cannot tell that apart from "the model looked and found
//	  nothing".
func TestTheModelIsToldThePictureIsGone(t *testing.T) {
	srv := &pictureFreeServer{}
	cm := newPictureFreeModel(t, srv)

	if _, err := cm.Generate(context.Background(), []*schema.Message{pictureMessage()}); err != nil {
		t.Fatal(err)
	}
	body := srv.body(1)
	if !strings.Contains(body, "does not accept images") {
		t.Fatalf("the model was not told:\n%s", body)
	}
	// ★ The question itself must survive: the retry is the same question.
	if !strings.Contains(body, "what is in this picture?") {
		t.Fatalf("the retry lost the question:\n%s", body)
	}
}

// ★ A failure that is not about the picture is reported as it was.
//
//	★ The first error is the real reason: a bad key does not become "the
//	  picture was the problem".
func TestAFailureThatIsNotAboutThePictureIsReportedAsItWas(t *testing.T) {
	srv := &pictureFreeServer{refuseBoth: true}
	cm := newPictureFreeModel(t, srv)

	_, err := cm.Generate(context.Background(), []*schema.Message{pictureMessage()})
	if err == nil {
		t.Fatal("the failure must be reported")
	}
	if !strings.Contains(err.Error(), "does not support image input") {
		t.Fatalf("the first error must be reported: %v", err)
	}
}

// ★ A request without a picture is not retried.
//
//	★ One request, one failure — the retry exists for the pictures, and a
//	  request without them has nothing to strip.
func TestARequestWithoutAPictureIsNotRetried(t *testing.T) {
	srv := &pictureFreeServer{refuseBoth: true}
	cm := newPictureFreeModel(t, srv)

	_, err := cm.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("hello"),
	})
	if err == nil {
		t.Fatal("the failure must be reported")
	}
	if srv.count() != 1 {
		t.Fatalf("the provider saw %d requests, want 1", srv.count())
	}
}

// ★ The fallback keeps the shape the wire accepts.
//
//	★ The text stays a part and Content stays empty: the `content` field is
//	  either a string or a list, and the client refuses to marshal both.
func TestTheFallbackKeepsTheShapeTheWireAccepts(t *testing.T) {
	stripped, ok := withoutImages([]*schema.Message{pictureMessage()})
	if !ok {
		t.Fatal("the picture was not found")
	}
	m := stripped[0]
	if m.Content != "" {
		t.Fatalf("the message still carries its text: %q", m.Content)
	}
	if len(m.UserInputMultiContent) != 2 {
		t.Fatalf("the message carries %d parts, want the text and the note", len(m.UserInputMultiContent))
	}
	for _, p := range m.UserInputMultiContent {
		if p.Type == schema.ChatMessagePartTypeImageURL {
			t.Fatal("a picture survived the fallback")
		}
	}

	// ★ And the whole thing marshals: that is the failure this exists for.
	if _, err := json.Marshal(map[string]any{"messages": stripped}); err != nil {
		t.Fatalf("the fallback does not marshal: %v", err)
	}
}

// ★ The original message is left alone.
//
//	★ The retry must not strip the picture from the caller's message: the
//	  transcript holds it, and the caller may send it again.
func TestTheFallbackLeavesTheOriginalAlone(t *testing.T) {
	original := pictureMessage()
	if _, ok := withoutImages([]*schema.Message{original}); !ok {
		t.Fatal("the picture was not found")
	}
	if len(original.UserInputMultiContent) != 2 {
		t.Fatalf("the original was modified: %d parts", len(original.UserInputMultiContent))
	}
}

// ★ The constraint itself: one `content` field, one shape.
//
//	★ This is the failure the whole shape exists for. The wire has a
//	  `content` that is either a string or a list, and the client refuses to
//	  marshal a message carrying both — the turn dies before the request is
//	  even sent, on a picture the user attached.
//
//	★ The test lives here because that is where the client is: the
//	  constraint is the provider's, not openheron's.
func TestTheWireRefusesAMessageWithBothAStringAndAList(t *testing.T) {
	both := goopenai.ChatCompletionMessage{
		Role:    goopenai.ChatMessageRoleUser,
		Content: "the question",
		MultiContent: []goopenai.ChatMessagePart{
			{Type: goopenai.ChatMessagePartTypeText, Text: "the question"},
		},
	}
	if _, err := json.Marshal(both); err == nil {
		t.Fatal("a message with both a string and a list must be refused")
	}

	// ★ The shape that is accepted: the list alone.
	list := goopenai.ChatCompletionMessage{
		Role: goopenai.ChatMessageRoleUser,
		MultiContent: []goopenai.ChatMessagePart{
			{Type: goopenai.ChatMessagePartTypeText, Text: "the question"},
		},
	}
	if _, err := json.Marshal(list); err != nil {
		t.Fatalf("the list alone must be accepted: %v", err)
	}
}

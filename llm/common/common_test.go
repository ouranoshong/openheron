package common

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	goopenai "github.com/meguminnnnnnnnn/go-openai"
)

func TestNewRejectsMissingConfig(t *testing.T) {
	if _, err := New(context.Background(), nil); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("want ErrNoConfig, got %v", err)
	}
}

func TestNewRejectsEmptyBaseURL(t *testing.T) {
	_, err := New(context.Background(), &Config{ProviderID: "x", APIKey: "k"})
	if !errors.Is(err, ErrNoBaseURL) {
		t.Fatalf("want ErrNoBaseURL, got %v", err)
	}
}

func TestGetTypeReturnsProviderID(t *testing.T) {
	cm, err := New(context.Background(), &Config{
		ProviderID: "deepseek",
		APIKey:     "k",
		BaseURL:    "https://api.deepseek.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := cm.GetType(); got != "deepseek" {
		t.Fatalf("GetType() = %q, want %q -- tracing relies on it to tell which endpoint is really in use", got, "deepseek")
	}
}

func TestGetTypeFallsBackWhenUnset(t *testing.T) {
	cm, err := New(context.Background(), &Config{APIKey: "k", BaseURL: "https://x.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cm.GetType(); got != "openai" {
		t.Fatalf("GetType() = %q, want the %q fallback", got, "openai")
	}
}

func TestRequestOptionsEmitsEveryConfiguredField(t *testing.T) {
	temp, max, top := float32(0.2), 4096, float32(0.9)
	r := RequestOptions{
		Fields:      map[string]any{"enable_thinking": true},
		Header:      map[string]string{"HTTP-Referer": "openheron"},
		Model:       "gpt-4o",
		Temperature: &temp,
		MaxTokens:   &max,
		TopP:        &top,
		Stop:        []string{"\n"},
	}
	if got := len(r.Options()); got != 7 {
		t.Fatalf("want 7 options, got %d", got)
	}
}

func TestRequestOptionsEmptyProducesNothing(t *testing.T) {
	if got := len((RequestOptions{}).Options()); got != 0 {
		t.Fatalf("want 0 options, got %d", got)
	}
}

func TestAttachWithoutOptionsReturnsSameInstance(t *testing.T) {
	cm, err := New(context.Background(), &Config{APIKey: "k", BaseURL: "https://x.example"})
	if err != nil {
		t.Fatal(err)
	}
	got := AttachRequestOptions(cm, RequestOptions{}, nativeReasoningField, nativeReasoningField)
	if any(got) != any(cm) {
		t.Fatal("with no options configured it must be returned unchanged, not wrapped")
	}
}

func TestAttachForwardsGetType(t *testing.T) {
	cm, err := New(context.Background(), &Config{
		ProviderID: "ark", APIKey: "k", BaseURL: "https://x.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := AttachRequestOptions(cm, RequestOptions{Model: "doubao"}, nativeReasoningField, nativeReasoningField)

	tp, ok := wrapped.(interface{ GetType() string })
	if !ok {
		t.Fatal("the wrapper must implement components.Typer - a missing agent type degrades to an empty string")
	}
	if got := tp.GetType(); got != "ark" {
		t.Fatalf("wrapped GetType() = %q, want %q", got, "ark")
	}
}

func TestAttachForwardsThroughWithTools(t *testing.T) {
	cm, err := New(context.Background(), &Config{APIKey: "k", BaseURL: "https://x.example"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := AttachRequestOptions(cm, RequestOptions{Model: "gpt-4o"}, nativeReasoningField, nativeReasoningField)

	tool := &schema.ToolInfo{Name: "noop"}
	withTools, err := wrapped.WithTools([]*schema.ToolInfo{tool})
	if err != nil {
		t.Fatal(err)
	}
	om, ok := withTools.(*optionedModel)
	if !ok {
		t.Fatalf("WithTools dropped the wrapper layer: got %T", withTools)
	}
	if len(om.opts) != len(wrapped.(*optionedModel).opts) {
		t.Fatalf("request-level options were lost after WithTools: %d -> %d",
			len(wrapped.(*optionedModel).opts), len(om.opts))
	}
}

func applyModifier(t *testing.T, field, from, body string) string {
	t.Helper()
	out, err := reasoningPayloadModifier(field, from)(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("modifier: %v", err)
	}
	return string(out)
}

func TestReasoningRenameDeletesSourceField(t *testing.T) {
	body := `{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","reasoning_content":"THOUGHT","content":"answer"}
	]}`

	out := applyModifier(t, "reasoning", "reasoning_content", body)

	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}

	asst := got.Messages[1]
	if asst["reasoning"] != "THOUGHT" {
		t.Fatalf("the target field is missing after rename: %#v", asst)
	}
	if _, still := asst["reasoning_content"]; still {
		t.Fatalf("* the source field was not deleted -- both names would be sent: %#v", asst)
	}
	if asst["content"] != "answer" {
		t.Fatalf("the body was corrupted: %#v", asst)
	}
}

func TestReasoningRenameSkipsNonAssistant(t *testing.T) {
	body := `{"messages":[{"role":"user","reasoning_content":"X","content":"hi"}]}`
	out := applyModifier(t, "reasoning", "reasoning_content", body)
	if !strings.Contains(out, "reasoning_content") {
		t.Fatalf("the user message must not be rewritten:\n%s", out)
	}
}

func TestReasoningRenameLeavesEmptyReasoning(t *testing.T) {
	body := `{"messages":[{"role":"assistant","content":"a"}]}`
	out := applyModifier(t, "reasoning", "reasoning_content", body)
	if strings.Contains(out, "reasoning") {
		t.Fatalf("an empty reasoning chain must produce no fields: \n%s", out)
	}
}

func TestReasoningRenameDoesNotCorruptOnBadJSON(t *testing.T) {
	bad := []byte("{not json")
	out, err := reasoningPayloadModifier("reasoning", "reasoning_content")(context.Background(), bad)
	if err != nil {
		t.Fatalf("a parse failure must not return an error: %v", err)
	}
	if string(out) != string(bad) {
		t.Fatalf("a parse failure must return the body unchanged: \n%s", out)
	}
}

func TestReasoningRenameDefaultsSourceField(t *testing.T) {
	body := `{"messages":[{"role":"assistant","reasoning_content":"T"}]}`
	out := applyModifier(t, "reasoning", "", body)
	if !strings.Contains(out, `"reasoning":"T"`) {
		t.Fatalf("with no explicit source field it must default to the native name: \n%s", out)
	}
}

func TestClassifyByStatus(t *testing.T) {
	cases := []struct {
		status int
		want   Kind
	}{
		{http.StatusUnauthorized, KindAuth},
		{http.StatusForbidden, KindAuth},
		{http.StatusTooManyRequests, KindRateLimit},
		{http.StatusServiceUnavailable, KindOverload},
		{529, KindOverload},
		{http.StatusBadRequest, KindBadRequest},
		{http.StatusNotFound, KindBadRequest},
		{http.StatusInternalServerError, KindUpstream},
		{0, KindUnknown},
	}
	for _, c := range cases {
		if got := classify(c.status); got != c.want {
			t.Errorf("classify(%d) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestQuotaHintDiffersFromRateLimit(t *testing.T) {
	rate := hintFor(&APIError{Kind: KindRateLimit}, nil)
	quota := hintFor(&APIError{Kind: KindQuota}, nil)
	if rate == quota {
		t.Fatalf("the rate_limit and quota hints must differ, otherwise users will apply the wrong fix")
	}
	if !strings.Contains(quota, "does NOT help") {
		t.Fatalf("the quota hint must state that lowering concurrency does NOT help: %q", quota)
	}
}

func TestBadRequestHintUsesConfiguredKeys(t *testing.T) {
	e := &APIError{Kind: KindBadRequest, Message: "unsupported value: sw_reasoning_switch"}
	if h := badRequestHint(e, []string{"sw_reasoning_switch"}); !strings.Contains(h, "yaml") {
		t.Fatalf("on a configured field name the hint must point back at the config: %q", h)
	}
	if h := badRequestHint(&APIError{Message: "unsupported value: sw_reasoning_switch"}, nil); strings.Contains(h, "yaml") {
		t.Fatalf("with no field name configured it must not guess: %q", h)
	}
	if h := badRequestHint(&APIError{Message: "model not found"}, []string{"sw_reasoning_switch"}); strings.Contains(h, "yaml") {
		t.Fatalf("a non-match must not produce a false positive: %q", h)
	}
}

// ★ "verify base_url / model name" sent a whole debugging session down the
//
//	wrong path: this 400 is the provider's own tool-schema parser reporting
//	what it saw, not a bad endpoint or model.
func TestBadRequestHintNamesToolSchemaValidation(t *testing.T) {
	e := &APIError{Kind: KindBadRequest, Message: "Invalid schema for function 'glob': " +
		`["path","path"] has non-unique elements`}
	h := badRequestHint(e, nil)
	for _, want := range []string{"tool-schema", "resend", "rejected request tools"} {
		if !strings.Contains(h, want) {
			t.Fatalf("hint %q is missing %q", h, want)
		}
	}
	if strings.Contains(h, "verify base_url") {
		t.Fatalf("the generic hint must not be used here: %q", h)
	}
	if h2 := badRequestHint(&APIError{Kind: KindBadRequest, Message: "unknown field"}, nil); !strings.Contains(h2, "verify base_url") {
		t.Fatalf("an unrelated 400 should keep the generic hint: %q", h2)
	}
}

// ★ "verify base_url" is wrong for this one too: nothing about the endpoint
//
//	is misconfigured. A history message lost its content to omitempty.
func TestBadRequestHintNamesTheEmptyAssistantMessage(t *testing.T) {
	e := &APIError{Kind: KindBadRequest,
		Message: "Invalid assistant message: content or tool_calls must be set"}
	h := badRequestHint(e, nil)
	for _, want := range []string{"assistant", "content", "history", "wire boundary"} {
		if !strings.Contains(h, want) {
			t.Fatalf("hint %q is missing %q", h, want)
		}
	}
	if strings.Contains(h, "verify base_url") {
		t.Fatalf("the generic hint must not be used here: %q", h)
	}
}

func TestContextCanceledPassesThrough(t *testing.T) {
	if got := wrap(context.Canceled, nil); !errors.Is(got, context.Canceled) {
		t.Fatalf("context.Canceled must pass through unchanged, got %#v", got)
	}
}

func TestWrapEndpointError(t *testing.T) {
	in := &goopenai.APIError{
		HTTPStatusCode: http.StatusTooManyRequests,
		HTTPStatus:     "429 Too Many Requests",
		Message:        "slow down",
		Type:           "rate_limit_error",
	}
	got := wrap(in, []string{"sw_reasoning_switch"})

	var ae *APIError
	if !errors.As(got, &ae) {
		t.Fatalf("want *APIError, got %T", got)
	}
	if ae.Kind != KindRateLimit || ae.Status != 429 || ae.Message != "slow down" {
		t.Fatalf("attribution is wrong: %+v", ae)
	}
	if ae.Hint == "" {
		t.Fatal("after attribution a remedy hint must be attached - \"429\" alone says nothing")
	}
	if !strings.Contains(ae.Error(), "429 rate_limit") {
		t.Fatalf("the Error() shape is wrong: %q", ae.Error())
	}
}

func TestWrapQuotaFromBodyWhenStatusIsZero(t *testing.T) {
	got := wrap(&goopenai.APIError{Type: "insufficient_quota", Message: "quota exceeded"}, nil)
	var ae *APIError
	if !errors.As(got, &ae) {
		t.Fatalf("want *APIError, got %T", got)
	}
	if ae.Kind != KindQuota {
		t.Fatalf("want KindQuota, got %q", ae.Kind)
	}
}

func TestWrapLeavesUnknownErrorsAlone(t *testing.T) {
	sentinel := errors.New("something else")
	if got := wrap(sentinel, nil); !errors.Is(got, sentinel) {
		t.Fatalf("when it cannot be attributed it must return unchanged, never force a Kind: %v", got)
	}
}

func TestWrapNilIsNil(t *testing.T) {
	if got := wrap(nil, nil); got != nil {
		t.Fatalf("wrap(nil) must be nil, got %v", got)
	}
}

func TestNoVendorKnowledgeInCode(t *testing.T) {
	banned := []string{
		`"reasoning_effort"`,
		`"enable_thinking"`,
		`"reasoning"`,
		`"thinking"`,
		`"thinking_config"`,
		`"reasoning_details"`,
	}

	for _, name := range []string{"model.go", "option.go", "request.go", "error.go", "stream.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			logic := stripComment(line)
			for _, key := range banned {
				if strings.Contains(logic, key) {
					t.Errorf("%s:%d vendor-specific key leaked into code: %s\n  -> endpoint knowledge belongs in .openheron/llm/<id>.yaml",
						name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

func stripComment(line string) string {
	line = strings.TrimSpace(line)
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	return line
}

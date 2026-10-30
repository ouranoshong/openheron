package common

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type loggingRoundTripFunc func(*http.Request) (*http.Response, error)

func (f loggingRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// captureLog redirects the package's own logger — the request log does not
// go through the standard logger's default instance any more.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := stdlog.Writer()
	stdlog.SetOutput(&buf)
	t.Cleanup(func() { stdlog.SetOutput(prev) })
	return &buf
}

func loggingClient(base http.RoundTripper) *http.Client {
	return WrapHTTPClient(&http.Client{Transport: base}, "test-provider")
}

func okResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}
}

func TestRequestLoggingDefaultsToSummary(t *testing.T) {
	t.Setenv(LogRequestsEnv, "")
	out := captureLog(t)

	var seen string
	client := loggingClient(loggingRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		seen = string(b)
		return okResponse(req), nil
	}))

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat/completions", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}

	if seen != body {
		t.Fatalf("the transport consumed the body: %q", seen)
	}
	got := out.String()
	if !strings.Contains(got, "provider=test-provider") {
		t.Fatalf("summary is missing the provider: %s", got)
	}
	if !strings.Contains(got, "bytes="+strconv.Itoa(len(body))) {
		t.Fatalf("summary is missing the byte count: %s", got)
	}
	if strings.Contains(got, "body:") {
		t.Fatalf("the body must stay out of the default log: %s", got)
	}
}

func TestRequestLoggingFullBodyRedactsSecrets(t *testing.T) {
	t.Setenv(LogRequestsEnv, "1")
	out := captureLog(t)

	var seen string
	client := loggingClient(loggingRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		seen = string(b)
		return okResponse(req), nil
	}))

	body := `{"model":"m","api_key":"body-secret","max_tokens":42,"messages":[{"role":"user","content":"hi"}],"nested":{"authorization":"Bearer body-secret"}}`
	req, _ := http.NewRequest(http.MethodPost,
		"https://user:pass@example.com/chat/completions?api_key=query-secret&model=m",
		strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}

	if seen != body {
		t.Fatalf("the transport consumed the body: %q", seen)
	}
	got := out.String()
	for _, want := range []string{
		`provider=test-provider`,
		`"api_key": "[redacted]"`,
		`"authorization": "[redacted]"`,
		`"max_tokens": 42`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log is missing %q:\n%s", want, got)
		}
	}
	for _, secret := range []string{"body-secret", "query-secret", "user:pass@"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked into the log:\n%s", secret, got)
		}
	}
}

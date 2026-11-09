package httplog

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/obs"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func capture(t *testing.T, level slog.Level) (*bytes.Buffer, *slog.Logger) {
	t.Helper()
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(level)
	return &buf, obs.New(&buf, lv)
}

func loggingClient(base http.RoundTripper, log *slog.Logger) *http.Client {
	return Wrap(&http.Client{Transport: base}, "test-provider", log)
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

func TestWrapFallsBackToDefaultTransport(t *testing.T) {
	c := Wrap(&http.Client{}, "p", obs.Discard())
	lt, ok := c.Transport.(*loggingTransport)
	if !ok {
		t.Fatalf("transport is %T, want *loggingTransport", c.Transport)
	}
	if lt.base != http.DefaultTransport {
		t.Fatalf("base is %T, want http.DefaultTransport (the proxy chain)", lt.base)
	}
}

func TestWrapIsIdempotentForTheSameLogger(t *testing.T) {
	log := obs.Discard()
	once := Wrap(&http.Client{}, "p", log)
	if twice := Wrap(once, "p", log); twice != once {
		t.Fatal("wrapping twice with the same logger returned a new client")
	}
}

func TestWrapReplacesRatherThanNestsAnotherLogger(t *testing.T) {
	first := Wrap(&http.Client{}, "p", obs.Discard())
	second := Wrap(first, "p", obs.Discard())
	lt, ok := second.Transport.(*loggingTransport)
	if !ok {
		t.Fatalf("transport is %T", second.Transport)
	}
	if _, nested := lt.base.(*loggingTransport); nested {
		t.Fatal("the new transport nests the old one; the request would be logged twice")
	}
}

func TestRequestLoggingDefaultsToSummary(t *testing.T) {
	t.Setenv(LogRequestsEnv, "")
	buf, log := capture(t, slog.LevelInfo)

	var seen string
	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		seen = string(b)
		return okResponse(req), nil
	}), log)

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat/completions", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}

	if seen != body {
		t.Fatalf("the transport consumed the body: %q", seen)
	}
	got := buf.String()
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

func TestRequestBodyIsDebugOnly(t *testing.T) {
	t.Setenv(LogRequestsEnv, "1")
	buf, log := capture(t, slog.LevelInfo)

	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return okResponse(req), nil
	}), log)
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "body:") {
		t.Fatalf("the body leaked at info level: %s", buf.String())
	}
}

func TestRequestLoggingFullBodyRedactsSecrets(t *testing.T) {
	t.Setenv(LogRequestsEnv, "1")
	buf, log := capture(t, slog.LevelDebug)

	var seen string
	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		seen = string(b)
		return okResponse(req), nil
	}), log)

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
	got := buf.String()
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

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

func configuredClient(base http.RoundTripper, log *slog.Logger, dump bool) *http.Client {
	return Wrap(&http.Client{Transport: base}, "test-provider", log,
		WithRequestBodyDump(dump))
}

var okTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
	return okResponse(req), nil
})

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

// ★ server/config.yaml's log_requests has to do exactly what
//
//	OPENHERON_LOG_LLM_REQUESTS=1 did: turn on the redacted body dump and the
//	raw tools line, without anyone setting an environment variable.
func TestConfiguredRequestDumpWorksWithoutTheEnvVar(t *testing.T) {
	t.Setenv(LogRequestsEnv, "")
	buf, log := capture(t, slog.LevelDebug)
	client := configuredClient(okTransport, log, true)

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"function","function":{"name":"glob","parameters":{"type":"object"}}}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "request body:") {
		t.Fatalf("the configured switch did not turn the body dump on:\n%s", got)
	}
	if !strings.Contains(got, "[llm] request tools (raw)") {
		t.Fatalf("the configured switch did not turn the raw tools line on:\n%s", got)
	}
}

func TestEnvWinsOverTheConfiguredSwitch(t *testing.T) {
	const body = `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	post := func(dump bool) string {
		buf, log := capture(t, slog.LevelDebug)
		client := configuredClient(okTransport, log, dump)
		req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
		if _, err := client.Do(req); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Run("env disables a configured true", func(t *testing.T) {
		t.Setenv(LogRequestsEnv, "false")
		if got := post(true); strings.Contains(got, "request body:") {
			t.Fatalf("an explicit env override must win:\n%s", got)
		}
	})
	t.Run("env enables a configured false", func(t *testing.T) {
		t.Setenv(LogRequestsEnv, "1")
		if got := post(false); !strings.Contains(got, "request body:") {
			t.Fatalf("an explicit env override must win:\n%s", got)
		}
	})
	t.Run("no env and a false config stays quiet", func(t *testing.T) {
		t.Setenv(LogRequestsEnv, "")
		if got := post(false); strings.Contains(got, "request body:") {
			t.Fatalf("the default must stay off:\n%s", got)
		}
	})
}

// ★ A client built for one configuration generation must not inherit the
//
//	switch from another. A re-wrap with the same value is a no-op; a changed
//	value replaces the transport rather than nesting a second one.
func TestRewrapRespectsTheConfiguredSwitch(t *testing.T) {
	log := obs.Discard()
	once := Wrap(&http.Client{}, "p", log, WithRequestBodyDump(true))
	if twice := Wrap(once, "p", log, WithRequestBodyDump(true)); twice != once {
		t.Fatal("re-wrapping with the same switch must be a no-op")
	}
	changed := Wrap(once, "p", log, WithRequestBodyDump(false))
	if changed == once {
		t.Fatal("a changed switch must produce a new client")
	}
	lt, ok := changed.Transport.(*loggingTransport)
	if !ok {
		t.Fatalf("transport is %T", changed.Transport)
	}
	if lt.dumpBody {
		t.Fatal("the new transport kept the old switch")
	}
	if _, nested := lt.base.(*loggingTransport); nested {
		t.Fatal("re-wrapping nested a second logging transport")
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

// ★ A provider that rejects a tool schema for "non-unique elements" can only
//
//	be diagnosed from the bytes that went out. The full body dump is re-marshalled
//	through a map, which collapses a duplicate JSON key; the raw tools line must
//	keep it.
func TestToolsBodyKeepsDuplicateKeys(t *testing.T) {
	t.Setenv(LogRequestsEnv, "1")
	buf, log := capture(t, slog.LevelDebug)

	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return okResponse(req), nil
	}), log)

	// The duplicate "new_string" key is the shape the provider complained about.
	// It is legal JSON; a map-based dump would swallow the first one silently.
	body := `{"model":"m","messages":[{"role":"user","content":"private question"}],` +
		`"tools":[{"type":"function","function":{"name":"edit_file","parameters":` +
		`{"type":"object","properties":{"file_path":{"type":"string"},` +
		`"new_string":{"type":"string"},"new_string":{"type":"string"}},` +
		`"required":["file_path","new_string","new_string"]}}}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}

	var rawLine string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "[llm] request tools (raw)") {
			rawLine = line
			break
		}
	}
	if rawLine == "" {
		t.Fatalf("no raw tools line:\n%s", buf.String())
	}
	if n := strings.Count(rawLine, `"new_string":{"type":"string"}`); n != 2 {
		t.Fatalf("the raw tools line lost a duplicate key (got %d):\n%s", n, rawLine)
	}
	if strings.Contains(rawLine, "private question") {
		t.Fatalf("the messages leaked into the tools line:\n%s", rawLine)
	}
}

// ★ The names tell which agent's tool table produced the request when several
//
//	agents share one provider — the difference between an accepted and a
//	rejected request may be nothing but the other tools in the array.
func TestToolsNamesShowWhichToolTableWasSent(t *testing.T) {
	t.Setenv(LogRequestsEnv, "1")
	buf, log := capture(t, slog.LevelDebug)

	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return okResponse(req), nil
	}), log)

	body := `{"model":"m","tools":[` +
		`{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}},` +
		`{"type":"function","function":{"name":"edit_file","parameters":{"type":"object"}}}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if !strings.Contains(got, "names=") {
		t.Fatalf("the tools line carries no names:\n%s", got)
	}
	for _, name := range []string{"read_file", "edit_file"} {
		if !strings.Contains(got, name) {
			t.Fatalf("the names are missing %q:\n%s", name, got)
		}
	}
}

// ★ The failure that matters is a 400 the provider attributes to our tool
//
//	schema. Waiting for someone to remember an environment variable loses the
//	one request that would have explained it, so a rejected request always
//	leaves its raw tools bytes behind, at info level and above.
func TestRejectedRequestLogsRawToolsWithoutTheEnvSwitch(t *testing.T) {
	t.Setenv(LogRequestsEnv, "")
	buf, log := capture(t, slog.LevelInfo)

	body := `{"model":"m","messages":[{"role":"user","content":"private question"}],` +
		`"tools":[{"type":"function","function":{"name":"edit_file","parameters":` +
		`{"type":"object","properties":{"file_path":{"type":"string"},` +
		`"new_string":{"type":"string"},"new_string":{"type":"string"}},` +
		`"required":["file_path","new_string","new_string"]}}}]}`
	reply := `{"error":{"message":"Invalid schema for function 'edit_file': ` +
		`[\"file_path\",\"new_string\",\"new_string\"] has non-unique elements"}}`

	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(reply)),
			Request:    req,
		}, nil
	}), log)
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	var rawLine string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "[llm] rejected request tools (raw)") {
			rawLine = line
			break
		}
	}
	if rawLine == "" {
		t.Fatalf("a rejected request left no raw tools line:\n%s", got)
	}
	for _, want := range []string{
		"status=400", "provider=test-provider", "names=", "non-unique elements",
	} {
		if !strings.Contains(rawLine, want) {
			t.Fatalf("the rejection line is missing %q:\n%s", want, rawLine)
		}
	}
	if n := strings.Count(rawLine, `"new_string":{"type":"string"}`); n != 2 {
		t.Fatalf("the rejection line lost a duplicate key (got %d):\n%s", n, rawLine)
	}
	if strings.Contains(rawLine, "private question") {
		t.Fatalf("the messages leaked into the tools line:\n%s", rawLine)
	}

	// ★ Reading the error body to build the log must not consume it: the
	//   caller still has to parse the provider's message.
	left, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != reply {
		t.Fatalf("the response body was consumed by logging: %q", left)
	}
}

func TestAcceptedRequestDoesNotLogRawTools(t *testing.T) {
	t.Setenv(LogRequestsEnv, "")
	buf, log := capture(t, slog.LevelInfo)
	client := loggingClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return okResponse(req), nil
	}), log)
	body := `{"model":"m","tools":[{"type":"function","function":{"name":"glob","parameters":{"type":"object"}}}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", strings.NewReader(body))
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "rejected request tools") {
		t.Fatalf("a successful request must stay quiet:\n%s", buf.String())
	}
}

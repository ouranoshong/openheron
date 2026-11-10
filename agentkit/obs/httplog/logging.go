// Package httplog wraps an http.Client so the model requests are written to
// the process log.
//
// ★ This is observation, not protocol. It lives next to obs rather than in
// llm/common so the protocol layer keeps zero openheron dependencies, and so
// the lines carry a level and follow the process's log level like every other
// line.
package httplog

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// LogRequestsEnv turns on the full body dump. The summary line is controlled
// by the log level instead; this variable only says whether the body is read,
// redacted and printed at debug level. ★ It wins over the configured value
// (server/config.yaml's log_requests) when it is set — an operator who has to
// capture one run should not have to edit the project. ★ A rejected request
// (4xx/5xx) always logs its raw tools array, with or without either switch —
// see [loggingTransport.logRejectedRequest].
const LogRequestsEnv = "OPENHERON_LOG_LLM_REQUESTS"

// Option configures one wrapped client.
type Option func(*loggingTransport)

// WithRequestBodyDump carries the value of server/config.yaml's log_requests.
//
// ★ This is injection, not a package-level flag: the value comes from the
//
//	same `*spec.Config` that built this model, so a client wrapped for one
//	configuration generation cannot inherit a switch from another. httplog
//	itself still knows nothing about spec — build passes the plain bool. The
//	env override is applied per request in [requestBodyDump].
func WithRequestBodyDump(enabled bool) Option {
	return func(t *loggingTransport) { t.dumpBody = enabled }
}

// Wrap returns a copy of c whose transport logs the request.
//
// ★ The base falls back to http.DefaultTransport when the client has no
// transport of its own: that is the link that reads HTTPS_PROXY and HTTP_PROXY
// from the environment, and it is what makes the sandbox Kit's credential
// substitution work.
func Wrap(c *http.Client, providerID string, log *slog.Logger, opts ...Option) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	next := &loggingTransport{provider: providerID, log: log}
	for _, o := range opts {
		o(next)
	}

	base := c.Transport
	if prev, ok := base.(*loggingTransport); ok {
		if prev.log == log && prev.provider == providerID && prev.dumpBody == next.dumpBody {
			return c
		}
		// ★ Unwrap first: nesting two transports would log the same request
		// twice, and the second copy would see an already-consumed body.
		base = prev.base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	next.base = base

	out := *c
	out.Transport = next
	return &out
}

type loggingTransport struct {
	base     http.RoundTripper
	provider string
	log      *slog.Logger
	dumpBody bool
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	full := requestBodyDump(t.dumpBody)

	var body []byte
	if full && req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			t.log.Warn("[llm] request body read failed",
				"provider", providerName(t.provider), "err", err)
		} else {
			body = b
			_ = req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(b))
			req.ContentLength = int64(len(b))
		}
	}

	t.log.Info("[llm] request",
		"provider", providerName(t.provider),
		"method", req.Method,
		"url", safeRequestURL(req.URL),
		"bytes", contentSize(req, body))

	if full {
		if len(body) == 0 {
			t.log.Debug("[llm] request body is empty", "provider", providerName(t.provider))
		} else {
			// ★ The body stays in the message: obs's handler collapses the
			// newlines into \n, which keeps the line parseable and keeps the
			// redaction readable in one piece.
			t.log.Debug("[llm] request body: "+redactedJSON(body),
				"provider", providerName(t.provider))

			// ★ redactedJSON re-marshals through a map, which silently drops
			//   duplicate JSON keys and reorders the properties. A provider
			//   that rejects a tool schema for "non-unique elements" can only
			//   be diagnosed from the bytes that actually went out, so the
			//   tools array is logged as a raw slice here. Only the tool
			//   definitions reach this line — the messages stay out of it.
			//   This copy is gated by the switch and debug level; the copy
			//   written for a rejected request is not (see below).
			if raw, names := toolsOfBody(body); raw != "" {
				t.log.Debug("[llm] request tools (raw): "+raw,
					"provider", providerName(t.provider),
					"names", names)
			}
		}
	}

	resp, err := t.base.RoundTrip(req)
	if err == nil && resp != nil && resp.StatusCode >= 400 {
		t.logRejectedRequest(resp, req, body)
	}
	return resp, err
}

// logRejectedRequest writes the exact tools bytes of a rejected request.
//
// ★ A provider that says "Invalid schema for function 'x': [...] has
//
//	non-unique elements" is reporting what its own parser saw, which is not
//	necessarily what we sent. The only way to settle that is the raw bytes of
//	the request that actually failed — so this line is unconditional on a
//	4xx/5xx, with no environment switch. The happy path stays quiet.
//
// ★ GetBody is the replay handle http.NewRequest leaves on a buffered body,
//
//	so recovering the bytes costs nothing on requests that succeed. When the
//	full-body switch is on, the bytes are already in memory. Either way the
//	response body is put back before the caller reads it.
func (t *loggingTransport) logRejectedRequest(resp *http.Response, req *http.Request, body []byte) {
	if len(body) == 0 && req.GetBody != nil {
		rc, err := req.GetBody()
		if err == nil {
			b, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr == nil {
				body = b
			}
		}
	}

	detail := ""
	if resp.Body != nil {
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		if err == nil {
			detail = strings.Join(strings.Fields(string(b)), " ")
			if len(detail) > 400 {
				detail = detail[:400]
			}
		}
	}

	raw, names := toolsOfBody(body)
	attrs := []any{
		"provider", providerName(t.provider),
		"status", resp.StatusCode,
		"detail", detail,
		"names", names,
	}
	if raw == "" {
		t.log.Warn("[llm] rejected request (no tools bytes available)", attrs...)
		return
	}
	t.log.Warn("[llm] rejected request tools (raw): "+raw, attrs...)
}

// requestBodyDump resolves env > server/config.yaml > false.
//
// ★ An empty environment means "no opinion": the configured value decides.
//
//	Any non-empty value is an explicit override for one run — the four
//	truthy spellings turn it on, everything else (including 0 / false / off)
//	turns it off. That keeps the old env-only behaviour byte for byte while
//	giving the project a default.
func requestBodyDump(configured bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(LogRequestsEnv))) {
	case "":
		return configured
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func providerName(id string) string {
	if id == "" {
		return "unknown"
	}
	return id
}

func contentSize(req *http.Request, body []byte) string {
	if body != nil {
		return strconv.Itoa(len(body))
	}
	if req.ContentLength >= 0 {
		return strconv.FormatInt(req.ContentLength, 10)
	}
	return "unknown"
}

func safeRequestURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	if c.User != nil {
		c.User = url.UserPassword(c.User.Username(), "[redacted]")
	}
	q := c.Query()
	for k := range q {
		if sensitiveKey(k) {
			q.Set(k, "[redacted]")
		}
	}
	c.RawQuery = q.Encode()
	return c.String()
}

// toolsOfBody returns the tools array exactly as it appears in the request
// body, plus the names of the tools in it.
//
// ★ The array is a json.RawMessage, not a map: unmarshalling it into a Go
//
//	value would collapse duplicate keys — the very thing a "non-unique
//	elements" rejection is about — and would reorder the properties. The
//	names are a cheap way to tell which agent's tool table produced a
//	request when several agents share one provider.
func toolsOfBody(body []byte) (string, []string) {
	var top struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &top); err != nil || len(top.Tools) == 0 {
		return "", nil
	}
	var defs []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	_ = json.Unmarshal(top.Tools, &defs)
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Function.Name)
	}
	return string(top.Tools), names
}

func redactedJSON(body []byte) string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}
	redactValue(v)
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(body)
	}
	return string(out)
}

func redactValue(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if sensitiveKey(k) {
				x[k] = "[redacted]"
				continue
			}
			redactValue(val)
		}
	case []any:
		for _, val := range x {
			redactValue(val)
		}
	}
}

func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", ""), "_", ""))
	switch k {
	case "apikey", "authorization", "secret", "password", "credential",
		"token", "accesstoken", "refreshtoken", "key":
		return true
	default:
		return false
	}
}

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

// LogRequestsEnv turns on the body dump. The summary line is controlled by
// the log level instead; this variable only says whether the body is read,
// redacted and printed at debug level.
const LogRequestsEnv = "OPENHERON_LOG_LLM_REQUESTS"

// Wrap returns a copy of c whose transport logs the request.
//
// ★ The base falls back to http.DefaultTransport when the client has no
// transport of its own: that is the link that reads HTTPS_PROXY and HTTP_PROXY
// from the environment, and it is what makes the sandbox Kit's credential
// substitution work.
func Wrap(c *http.Client, providerID string, log *slog.Logger) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	base := c.Transport
	if prev, ok := base.(*loggingTransport); ok {
		if prev.log == log && prev.provider == providerID {
			return c
		}
		// ★ Unwrap first: nesting two transports would log the same request
		// twice, and the second copy would see an already-consumed body.
		base = prev.base
	}
	if base == nil {
		base = http.DefaultTransport
	}

	out := *c
	out.Transport = &loggingTransport{base: base, provider: providerID, log: log}
	return &out
}

type loggingTransport struct {
	base     http.RoundTripper
	provider string
	log      *slog.Logger
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	full := fullRequestLogging()

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
		}
	}

	return t.base.RoundTrip(req)
}

func fullRequestLogging() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(LogRequestsEnv))) {
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

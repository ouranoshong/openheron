package common

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// OPENHERON_LOG_LLM_REQUESTS=1 task dev
const LogRequestsEnv = "OPENHERON_LOG_LLM_REQUESTS"

// stdlog writes the request log.
//
// ★ This package cannot import obs — it is a leaf with zero openheron
// dependencies — so it uses the standard logger. The flags are the ones
// hertz's logger uses, so a line reads the same as the rest of the output:
// the date, the time with microseconds, the caller's file and line.
//
// ★ There is no level tag here: this package has no levels. Everything it
// writes is a note about a request.
var stdlog = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile|log.Lmicroseconds)

func WrapHTTPClient(c *http.Client, providerID string) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	if _, ok := c.Transport.(*requestLoggingTransport); ok {
		return c
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out := *c
	out.Transport = &requestLoggingTransport{base: base, provider: providerID}
	return &out
}

type requestLoggingTransport struct {
	base     http.RoundTripper
	provider string
}

func (t *requestLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	full := fullRequestLogging()

	var body []byte
	if full && req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			stdlog.Printf("[llm] request provider=%s body read failed: %v", providerName(t.provider), err)
		} else {
			body = b
			_ = req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(b))
			req.ContentLength = int64(len(b))
		}
	}

	stdlog.Printf("[llm] request provider=%s method=%s url=%s bytes=%s",
		providerName(t.provider), req.Method, safeRequestURL(req.URL), contentSize(req, body))

	if full {
		if len(body) == 0 {
			stdlog.Printf("[llm] request provider=%s body: <empty>", providerName(t.provider))
		} else {
			stdlog.Printf("[llm] request provider=%s body:\n%s", providerName(t.provider), redactedJSON(body))
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

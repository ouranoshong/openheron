package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	goopenai "github.com/meguminnnnnnnnn/go-openai"
)

type Kind string

const (
	KindUnknown    Kind = ""
	KindAuth       Kind = "auth"
	KindRateLimit  Kind = "rate_limit"
	KindQuota      Kind = "quota"
	KindOverload   Kind = "overload"
	KindBadRequest Kind = "bad_request"
	KindUpstream   Kind = "upstream"
	KindNetwork    Kind = "network"
)

func (k Kind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	return string(k)
}

type APIError struct {
	Kind Kind

	Status int

	Message string

	Hint string

	Detail string
}

func (e *APIError) Error() string {
	var b strings.Builder
	switch {
	case e.Status > 0 && e.Message != "":
		fmt.Fprintf(&b, "%d %s —— %s", e.Status, e.Kind, e.Message)
	case e.Status > 0:
		fmt.Fprintf(&b, "%d %s", e.Status, e.Kind)
	case e.Message != "":
		b.WriteString(e.Message)
	default:
		b.WriteString("model call failed")
	}

	if e.Detail != "" && e.Kind == KindNetwork {
		fmt.Fprintf(&b, "\n  cause: %s", e.Detail)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, "\n  what to do: %s", e.Hint)
	}
	return b.String()
}

func (e *APIError) Unwrap() error { return nil }

func classify(status int) Kind {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusTooManyRequests:
		return KindRateLimit
	case status == http.StatusServiceUnavailable, status == 529:
		return KindOverload
	case status >= 400 && status < 500:
		return KindBadRequest
	case status >= 500:
		return KindUpstream
	default:
		return KindUnknown
	}
}

func wrap(err error, keys []string) error {
	if err == nil {
		return nil
	}

	var ae *APIError
	if errors.As(err, &ae) {
		return err
	}

	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &APIError{
			Kind:    KindNetwork,
			Message: "request timed out",
			Detail:  err.Error(),
			Hint:    "raise the timeout in llm/<id>.yaml; reasoning models are an order of magnitude slower",
		}
	}

	var oerr *goopenai.APIError
	if errors.As(err, &oerr) {
		e := &APIError{
			Kind:    classify(oerr.HTTPStatusCode),
			Status:  oerr.HTTPStatusCode,
			Message: oerr.Message,
			Detail:  "",
		}
		if e.Kind == KindUnknown {
			e.Kind = classifyBody(oerr)
		}
		if e.Message == "" {
			e.Message = fmt.Sprintf("endpoint returned no error body (status %d %s)",
				oerr.HTTPStatusCode, oerr.HTTPStatus)
		}
		e.Hint = hintFor(e, keys)
		return e
	}

	var ne net.Error
	if errors.As(err, &ne) {
		return &APIError{
			Kind:   KindNetwork,
			Detail: err.Error(),
			Hint:   "check that base_url is reachable and that network and TLS are healthy",
		}
	}

	return err
}

func classifyBody(raw *goopenai.APIError) Kind {
	t := strings.ToLower(raw.Type)
	c := strings.ToLower(codeString(raw.Code))
	switch {
	case strings.Contains(t, "insufficient_quota") || strings.Contains(c, "insufficient_quota"):
		return KindQuota
	case strings.Contains(t, "rate_limit") || strings.Contains(c, "rate_limit"):
		return KindRateLimit
	case strings.Contains(t, "authentication") || strings.Contains(t, "permission") ||
		strings.Contains(c, "invalid_api_key"):
		return KindAuth
	case strings.Contains(t, "overload") || strings.Contains(c, "overloaded"):
		return KindOverload
	case raw.Type != "" || raw.Code != nil:
		return KindBadRequest
	}
	return KindUnknown
}

func hintFor(e *APIError, keys []string) string {
	switch e.Kind {
	case KindAuth:
		return "check that api_key is set, not expired, and authorized for this model"
	case KindRateLimit:
		return "rate limited: lower concurrency and retry (per-key QPM cap)"
	case KindQuota:
		return "quota or balance exhausted: top up or switch account \u2014 lowering concurrency does NOT help"
	case KindOverload:
		return "endpoint is busy: retry later or switch to a fallback model"
	case KindUpstream:
		return "upstream 5xx: not a local problem; retry or switch endpoint"
	case KindBadRequest:
		return badRequestHint(e, keys)
	}
	return ""
}

func badRequestHint(e *APIError, keys []string) string {
	msg := strings.ToLower(e.Message)
	if strings.Contains(msg, "invalid schema for function") ||
		strings.Contains(msg, "non-unique elements") {
		return "the provider's own tool-schema validator rejected the request (duplicate required names it parsed, not necessarily ones we sent): resend the turn; the exact tools bytes are in the '[llm] rejected request tools (raw)' log line"
	}
	if len(keys) > 0 && mentionsAny(msg, keys...) {
		return "the request carries endpoint-specific fields it rejects: check models[].extra and thinking in .openheron/llm/<id>.yaml"
	}
	return "request rejected: verify base_url, the model name, and the parameters this endpoint supports"
}

func mentionsAny(s string, keys ...string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func codeString(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

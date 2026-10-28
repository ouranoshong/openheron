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

// Kind 是端点错误的**归类** —— 供程序判断，不供人读。
type Kind string

const (
	KindUnknown    Kind = ""
	KindAuth       Kind = "auth"        // 401 / 403 —— 凭据问题
	KindRateLimit  Kind = "rate_limit"  // 429 —— 限流，调并发有用
	KindQuota      Kind = "quota"       // 配额耗尽 —— 调并发**没用**，与限流相反
	KindOverload   Kind = "overload"    // 503 / 529 —— 端点忙
	KindBadRequest Kind = "bad_request" // 其余 4xx —— 请求本身不对
	KindUpstream   Kind = "upstream"    // 其余 5xx —— 不是本地问题
	KindNetwork    Kind = "network"     // 连不上 / 超时 / TLS
)

// String 让 Kind 能直接打给人看。
func (k Kind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	return string(k)
}

// APIError 是**归一化**后的端点错误。
//
// 它回答两个问题，而原始错误一个都不回答：
//   - [Kind]：程序据此决定重试 / 换模型 / 让人改配置
//   - [Hint]：人照着能改。★ 这是本类型存在的理由 ——「429」本身不说明
//     任何事，「把并发调小 / 检查配额」才说明。
type APIError struct {
	// Kind 是归类。
	Kind Kind

	// Status 是 HTTP 状态码；网络类错误为 0。
	Status int

	// Message 是**端点原文，一个字不改**。
	Message string

	// Hint 是处置建议。空 = 没有可建议的。
	Hint string

	// Detail 是**底层错误的原文**（如 `dial tcp …: connection refused`）。
	//
	// ★ 它与 Message 是两件事：Message 是端点回的话，Detail 是**传输层**的话 ——
	// 「打的是哪个端点」只在这里（端点没回话时 Message 是空的）。
	//   归因不能靠改写别人的信息，只能**加**。
	Detail string
}

// Error 实现 error。
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

	// ★ 传输层原文**只在它带新信息时**才打：端点回过话时（4xx/5xx）它就是
	//   同一句话的另一种写法，重复一遍只是噪音；而**请求没发出去**时
	//   （网络类）它是唯一的线索 ——「我配的 base_url 用上了吗」只在那里。
	if e.Detail != "" && e.Kind == KindNetwork {
		fmt.Fprintf(&b, "\n  cause: %s", e.Detail)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, "\n  what to do: %s", e.Hint)
	}
	return b.String()
}

// Unwrap 保留底层错误，让调用方仍能 errors.Is / errors.As 判定。
func (e *APIError) Unwrap() error { return nil }

// classify 按 HTTP 状态码归类。
func classify(status int) Kind {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusTooManyRequests:
		return KindRateLimit
	// ★ 503 与 529 **刻意都归 overload**：对用户来说「端点现在忙」只有一个动作 ——
	//   等或换降级模型。拆成「过载 vs 上游故障」是猜端点内部状态，
	//   而两种的处置完全一样，拆了只多一个说不清的分歧。
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

// wrap 把上游错误归一化成 [APIError]。
//
// ★ 它是**唯一**的错误入口（Generate 与 Stream 都走它）——所以「哪一类错误
//
//	能被归因」这件事只有一个地方说了算。
//
// 归因不了的原样返回 —— **不硬套一个 Kind**：假装归因比不归因更糟，
// 用户会照着一个错的建议去改配置。
//
// keys 是**这个端点使用的厂商字段名**，由 [Config.HintKeys] 从配置传入：协议层
// 不知道「这个端点管思考叫什么」，那是 `.openheron/llm/<id>.yaml` 的事实。
func wrap(err error, keys []string) error {
	if err == nil {
		return nil
	}

	var ae *APIError
	if errors.As(err, &ae) {
		return err
	}

	// ★ context.Canceled 是**控制流信号，不是错误** —— 原样透传。
	//   openheron 的 Runtime.Abort 就是靠 cancel 实现的，包成 APIError 会
	//   让「用户主动中止」变成「端点报错」。
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
			// 端点已经回过话了，传输层原文不带新信息。
			Detail: "",
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

// classifyBody 是 [classify] 的兜底：状态码给不出信息时（0 或非标准码），
// 从 body 的 type/code 里认一次。
//
// ★ 这是**协议级**的判断，不是厂商方言 —— type/code 都是 OpenAI Chat
//
//	Completions 协议定义的字段。
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

// hintFor 给出处置建议。
//
// ★ **限流与配额的提示必须不同**：前者调并发有用，后者是额度用尽，
//
//	调并发**只会更快用完**。两者返回同样的「稍后重试」会让用户做错事。
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

// badRequestHint 针对「请求被拒」给出更具体的线索。
//
// 命中思考相关字段时提示参数 —— 这是最高频的 400 来源。
func badRequestHint(e *APIError, keys []string) string {
	// ★ 字段名列表由配置传入，不从代码里猜 —— 猜就是��厂商方言写回代码。
	if len(keys) > 0 && mentionsAny(strings.ToLower(e.Message), keys...) {
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

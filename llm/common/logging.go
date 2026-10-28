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

// ★ 这个包用**标准 logger**（log.Printf），不用 [openheron/obs]。
//
//	不是遗漏，是约束：llm/common 是零 openheron 依赖的叶子包
//	（docs/architecture.md §3.2.1），而 obs 属于 openheron ——
//	引它就破了那条铁律。所以这里的日志出口与别处不同形。
//
// LogRequestsEnv 控制是否把**完整请求体**打进日志。
//
// ★ 默认只打一行摘要（provider / URL / 字节数）：完整请求体包含整段对话、
// 工具 schema 与可能的业务数据，默认全量打印会让正常日志变成一份会话副本。
//
//	需要排查「模型到底收到了什么」时显式打开：
//
//		OPENHERON_LOG_LLM_REQUESTS=1 make dev
//
//	摘要始终开启 —— 不打开这个开关也能确认请求到了哪个端点、多大。
const LogRequestsEnv = "OPENHERON_LOG_LLM_REQUESTS"

// WrapHTTPClient 给一个 HTTP client 套上请求日志。
//
// ★ 日志挂在 RoundTripper 而不是 payload modifier 上：
//
//	modifier 只有一个槽位，而本仓库已经用它做思维链字段改名。再挂一个会
//	把改名器覆盖掉 —— 症状是某些端点突然不认 reasoning 字段。而
//	RoundTripper 看到的是**最终**请求体：所有 option、ExtraFields、
//	payload modifier 都已经生效之后的那一份。
//
// ★ providerID 只用于归属，不参与请求。
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
			log.Printf("[llm] request provider=%s body read failed: %v", providerName(t.provider), err)
		} else {
			body = b
			_ = req.Body.Close()
			// 读过的 body 必须放回去，否则真正发出去的请求会少一段。
			req.Body = io.NopCloser(bytes.NewReader(b))
			req.ContentLength = int64(len(b))
		}
	}

	log.Printf("[llm] request provider=%s method=%s url=%s bytes=%s",
		providerName(t.provider), req.Method, safeRequestURL(req.URL), contentSize(req, body))

	if full {
		if len(body) == 0 {
			log.Printf("[llm] request provider=%s body: <empty>", providerName(t.provider))
		} else {
			log.Printf("[llm] request provider=%s body:\n%s", providerName(t.provider), redactedJSON(body))
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

// safeRequestURL 去掉 URL 里的凭据，并遮蔽常见的密钥类 query 参数。
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

// redactedJSON 把请求体展开成便于阅读的 JSON，并遮蔽密钥类字段。
//
// 解析失败时原样返回：这不是安全检查，而是日志；把请求体改坏比少打一行更糟。
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

// sensitiveKey 只匹配**完整键名**，不做子串匹配。
//
// ★ 子串匹配会把 max_tokens 误伤成 max_[redacted]，那条日志就失去意义了。
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

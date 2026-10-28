package webui

import "testing"

// ★ 这份名单是「SPA 回落会不会吃掉真接口」的唯一防线。
//
//	少了某个前缀，症状不是报错，而是那条接口静默返回 index.html ——
//	前端拿到 HTML 后报的错与真实原因毫无关系。
func TestLooksLikeAPI(t *testing.T) {
	api := []string{
		"/v2", "/v2/health", "/v2/sessions", "/v2/run",
		"/v2/bogus", // 拼错的 /v2 路径同样要挡住，否则回落成 HTML
		"/sessions", "/sessions/abc/chat", "/agents", "/config", "/health", "/runtime",
	}
	for _, p := range api {
		if !looksLikeAPI(p) {
			t.Errorf("looksLikeAPI(%q) = false, want true", p)
		}
	}

	page := []string{"/", "/index.html", "/threads/abc", "/assets/index-x.js", "/threads/a/b", "/v2ui"}
	for _, p := range page {
		if looksLikeAPI(p) {
			t.Errorf("looksLikeAPI(%q) = true, want false (page routes must reach the SPA fallback)", p)
		}
	}
}

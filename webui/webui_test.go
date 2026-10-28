package webui

import "testing"

func TestLooksLikeAPI(t *testing.T) {
	api := []string{
		"/v2", "/v2/health", "/v2/sessions", "/v2/run",
		"/v2/bogus",
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

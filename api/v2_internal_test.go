package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/obs"
	"openheron/storage"
	"openheron/storage/filestore"
)

// captureLog 把统一日志入口的输出收进 buffer。
//
// ★ 收的是 obs 而不是标准 log：业务日志都走 obs.Logger()，
//
//	测试要盯的就是那一份 —— 否则「日志里有 cause」这条断言验的是
//	另一个出口。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := obs.Logger()
	obs.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { obs.SetLogger(prev) })
	return &buf
}

// breakSessionStore 造一个**真的会失败**的会话存储。
//
// ★ 用真的 FileManager 而不是假实现：这条用例要验的是「错误有没有被
//
//	记下来」，而假实现返回的错误链与生产不同，验不到包装有没有被拆掉。
//	手法是把 sessions 目录换成一个同名文件 —— 于是 List 拿到真的 ENOTDIR。
func breakSessionStore(t *testing.T) (storage.SessionManager, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	fm, err := filestore.NewFileManager(dir, filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fm, dir
}

// TestV2InternalErrorCarriesTheCauseToTheLog 是这个文件存在的理由。
//
// ★ 修之前：11 个 5xx 出口都只报一句描述（"list sessions failed"…）并把
//
//	error 丢掉，于是日志里是：
//	    [api] GET /v2/sessions -> 500 internal: list sessions failed
//	后半句在本地目录里查得到，前半句才是有用的 —— 而它被扔了。
//	不知道是文件坏了、目录没权限，还是路径类型不对，而这三种处置完全不同。
func TestV2InternalErrorCarriesTheCauseToTheLog(t *testing.T) {
	srv := newV2Server(t)
	buf := captureLog(t)

	sm, dir := breakSessionStore(t)
	srv.sm = sm

	got := callJSON(t, srv, "GET", "/v2/sessions", nil)
	if got.code != consts.StatusInternalServerError {
		t.Fatalf("code = %d, want 500 (body: %s)", got.code, got.body)
	}

	logged := buf.String()
	if !strings.Contains(logged, dir) {
		t.Fatalf("the log must name the path that failed; got:\n%s", logged)
	}
	if !strings.Contains(logged, "/v2/sessions") {
		t.Fatalf("the log must name the route; got:\n%s", logged)
	}
	// ★ 判据是「日志里出现了 error 的内容」，不是「日志非空」：
	//   后者由 `status >= 500` 就满足了，而那正是缺陷本身。
	if strings.TrimSpace(strings.TrimPrefix(logged, logged[:strings.Index(logged, ": ")+2])) == "" {
		t.Fatalf("the log carries no cause at all: %q", logged)
	}
}

// TestV2FailInternalKeepsTheClientMessageGeneric 守住另一半：客户端**不该**
// 拿到细节。500 的处置是看日志，不是看响应体 —— 而 error 里常带绝对路径，
// 转贴 issue 时就泄露了本地目录结构。
func TestV2FailInternalKeepsTheClientMessageGeneric(t *testing.T) {
	srv := newV2Server(t)
	_ = captureLog(t)

	sm, dir := breakSessionStore(t)
	srv.sm = sm

	got := callJSON(t, srv, "GET", "/v2/sessions", nil)

	var e errBody
	if err := json.Unmarshal([]byte(got.body), &e); err != nil {
		t.Fatal(err)
	}
	if e.Error != "internal error" {
		t.Fatalf("client must see a generic message, got %q", e.Error)
	}
	if e.Code != codeInternal {
		t.Fatalf("code = %q, want %q", e.Code, codeInternal)
	}
	if strings.Contains(got.body, dir) {
		t.Fatalf("the response body leaked a server-side path: %s", got.body)
	}
}

// TestErrBodyNeverSerializesTheCause 守住「原因只在日志里」是**类型系统**给的
// 保证，而不是靠约定。
func TestErrBodyNeverSerializesTheCause(t *testing.T) {
	raw, err := json.Marshal(errBody{
		Error: "internal error",
		Code:  codeInternal,
		cause: errNoSuchDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(errNoSuchDir.Error())) {
		t.Fatalf("the cause leaked into the response body: %s", raw)
	}
	// 顺带钉住字段名集合：多一个导出字段就多一条泄露路径。
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		switch name {
		case "error", "code", "kind", "hint", "retryable":
		default:
			t.Fatalf("unexpected field %q in the error envelope: %s", name, raw)
		}
	}
}

var errNoSuchDir = os.ErrNotExist

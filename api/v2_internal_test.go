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

	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/storage/filestore"
)

func captureLog(t *testing.T, srv *Server) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	srv.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &buf
}

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

// [api] GET /v2/sessions -> 500 internal: list sessions failed
func TestV2InternalErrorCarriesTheCauseToTheLog(t *testing.T) {
	srv := newV2Server(t)
	buf := captureLog(t, srv)

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
	if strings.TrimSpace(strings.TrimPrefix(logged, logged[:strings.Index(logged, ": ")+2])) == "" {
		t.Fatalf("the log carries no cause at all: %q", logged)
	}
}

func TestV2FailInternalKeepsTheClientMessageGeneric(t *testing.T) {
	srv := newV2Server(t)
	_ = captureLog(t, srv)

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

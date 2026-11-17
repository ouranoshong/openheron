package api

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ouranoshong/openheron/agentkit/obs"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/storage/filestore"
)

func newTestServer(t *testing.T) (*Server, *filestore.FileManager) {
	t.Helper()

	fm, err := filestore.NewFileManager(filepath.Join(t.TempDir(), "sessions"), filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// A real Runtime with no AgentBuilder: routing and session handling are
	// under test, not agent execution.
	rt := run.New(run.Config{SessionManager: fm})

	engine := route.NewEngine(config.NewOptions(nil))
	srv := New(rt, fm, obs.Discard())
	srv.h = engine
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })
	return srv, fm
}

// newStaticTestServer mirrors newTestServer but wires a static dir *before*
// registerRoutes, since routes capture the FS at registration time.
// staticDir is t.TempDir that waits out hertz's file cache before removing.
//
// StaticFS keeps the served files open and sweeps them one cache duration
// after their last use, every half duration. The server sets that to one
// second, so two seconds cover the sweep. t.TempDir cannot be used: its
// cleanup runs before this wait, and on Windows the still-open index.html
// would turn a framework cache into a test failure.
func staticDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "openheron-static-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		_ = os.RemoveAll(dir)
	})
	return dir
}

func newStaticTestServer(t *testing.T, staticDir string) *Server {
	t.Helper()

	fm, err := filestore.NewFileManager(filepath.Join(t.TempDir(), "sessions"), filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rt := run.New(run.Config{SessionManager: fm})

	engine := route.NewEngine(config.NewOptions(nil))
	srv := New(rt, fm, obs.Discard())
	srv.h = engine
	srv.SetStaticDir(staticDir)
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })
	return srv
}

type result struct {
	code int
	body string
}

func call(t *testing.T, srv *Server, method, path, body string) result {
	t.Helper()

	engine, ok := srv.h.(*route.Engine)
	if !ok {
		t.Fatal("server is not backed by a route.Engine")
	}

	w := ut.PerformRequest(engine, method, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"})

	code := w.Code
	if code == 0 {
		code = consts.StatusOK
	}
	return result{code: code, body: w.Body.String()}
}

func mustCreate(t *testing.T, srv *Server) storage.Session {
	t.Helper()
	sess, err := srv.sm.Create(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// The id comes straight from the URL and lands in filesystem paths, so it must
// be rejected before it can reach disk.
func TestUnsafeSessionIDsAreRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, id := range []string{"..", "%2e%2e%2f%2e%2e%2fetc", "not-a-uuid", "a%2Fb"} {
		got := call(t, srv, "GET", "/v2/sessions/"+id, "")
		if got.code == consts.StatusOK {
			t.Fatalf("id %q was accepted", id)
		}
	}
}

// `GET /` must serve the front end's index.html.
//
// Regression guard for the Hertz app.FS trap: Static("/", dir) leaves
// IndexNames empty, so a request for the directory `/` is answered 403
// ("cannot access directory without index page") and the page never loads.
func TestStaticServesIndexAtRoot(t *testing.T) {
	dir := staticDir(t)
	const marker = "<h1>openheron-frontend</h1>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"+marker), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newStaticTestServer(t, dir)

	got := call(t, srv, "GET", "/", "")
	if got.code != consts.StatusOK {
		t.Fatalf("GET / want 200, got %d: %s", got.code, got.body)
	}
	if !strings.Contains(got.body, marker) {
		t.Fatalf("GET / did not return index.html: %q", got.body)
	}

	if got := call(t, srv, "GET", "/index.html", ""); got.code != consts.StatusOK || !strings.Contains(got.body, marker) {
		t.Fatalf("GET /index.html want 200 with body, got %d: %q", got.code, got.body)
	}
}

// The static catch-all is registered last and must not shadow the API routes.
func TestStaticDoesNotShadowAPIRoutes(t *testing.T) {
	dir := staticDir(t)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("FRONTEND"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newStaticTestServer(t, dir)

	// /v2/sessions is an API route; it must still return the JSON list, not
	// the static fallback.
	got := call(t, srv, "GET", "/v2/sessions", "")
	if got.code != consts.StatusOK {
		t.Fatalf("GET /v2/sessions want 200, got %d: %s", got.code, got.body)
	}
	if strings.Contains(got.body, "FRONTEND") {
		t.Fatalf("GET /v2/sessions was shadowed by static serving: %q", got.body)
	}
}

// ── /chat SSE framing ────────────────────────────────────────────────────

// noopAgent finishes immediately. a2ui still emits beginRendering + the root
// column up front, which is all this test needs to inspect framing.
type noopAgent struct{}

func (noopAgent) Name(context.Context) string        { return "noop" }
func (noopAgent) Description(context.Context) string { return "noop" }
func (noopAgent) Run(context.Context, *adk.TypedAgentInput[*schema.Message], ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	gen.Close()
	return iter
}

type noopBuilder struct{}

// Contexts reports what each agent's context allows. The fake has no
// models, so there are no windows.
func (noopBuilder) Contexts() map[string]contract.AgentContext { return nil }

func (noopBuilder) Build(context.Context, string, string, contract.TurnKind, string) (
	adk.TypedAgent[*schema.Message], string, error) {
	return noopAgent{}, "", nil
}

// bufExtWriter captures SSE bytes; it stands in for the connection writer, which
// a bare route.Engine (no socket) does not provide.
// bufExtWriter captures what a renderer writes.
//
// ★ It is thread-safe: the renderer writes from its own goroutine while the
//
//	test reads, and a bytes.Buffer is not.
type bufExtWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *bufExtWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// String returns what has been written so far.
func (w *bufExtWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *bufExtWriter) Flush() error    { return nil }
func (w *bufExtWriter) Finalize() error { return nil }

var _ network.ExtWriter = (*bufExtWriter)(nil)

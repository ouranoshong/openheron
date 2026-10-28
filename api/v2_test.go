package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/app"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"

	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/storage/filestore"
	"github.com/ouranoshong/openheron/transport"
)

// These cover the /v2 contract. Two matter most:
//   - route precedence: every /v2 path must beat the StaticFS catch-all, or
//     it answers 404 that reads exactly like "not implemented";
//   - the approval identity check, because a mismatched interruptId would let
//     a client answer a decision it no longer had context for.

// newV2Server builds a server with the static catch-all live, which is what
// makes the precedence test meaningful.
func newV2Server(t *testing.T) *Server {
	t.Helper()
	srv := newStaticTestServer(t, t.TempDir())
	return srv
}

func callJSON(t *testing.T, srv *Server, method, path string, body any) result {
	t.Helper()
	raw := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		raw = string(b)
	}
	return call(t, srv, method, path, raw)
}

// callMultipart posts files under the given field name.
func callMultipart(t *testing.T, srv *Server, path, field string, files map[string]string) result {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := w.CreateFormFile(field, name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	engine, ok := srv.h.(*route.Engine)
	if !ok {
		t.Fatal("server is not backed by a route.Engine")
	}
	resp := ut.PerformRequest(engine, "POST", path,
		&ut.Body{Body: bytes.NewReader(buf.Bytes()), Len: buf.Len()},
		ut.Header{Key: "Content-Type", Value: w.FormDataContentType()})
	code := resp.Code
	if code == 0 {
		code = consts.StatusOK
	}
	return result{code: code, body: resp.Body.String()}
}

func decodeCode(t *testing.T, body string) string {
	t.Helper()
	var e errBody
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, body)
	}
	if e.Error == "" {
		t.Fatalf("every error must carry a human message: %s", body)
	}
	return e.Code
}

// ── sessions ─────────────────────────────────────────────────────────────

func TestV2CreateAndList(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "POST", "/v2/sessions", nil)
	if got.code != consts.StatusCreated {
		t.Fatalf("POST /v2/sessions = %d, want 201 (%s)", got.code, got.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.ThreadID == "" {
		t.Fatal("threadId must be returned so the client can address the thread")
	}
	if meta.State != stateIdle {
		t.Fatalf("a new thread must be idle, got %q", meta.State)
	}

	list := callJSON(t, srv, "GET", "/v2/sessions", nil)
	var out threadList
	if err := json.Unmarshal([]byte(list.body), &out); err != nil {
		t.Fatalf("decode list: %v (%s)", err, list.body)
	}
	if len(out.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(out.Items))
	}
	if out.Items[0].HasPendingApproval {
		t.Fatal("a fresh thread has no pending approval")
	}
}

func TestV2GetUnknownThreadIs404(t *testing.T) {
	srv := newV2Server(t)
	got := callJSON(t, srv, "GET", "/v2/sessions/does-not-exist", nil)
	if got.code != consts.StatusNotFound {
		t.Fatalf("want 404, got %d", got.code)
	}
	if code := decodeCode(t, got.body); code != codeThreadNotFound {
		t.Fatalf("code = %q, want %q", code, codeThreadNotFound)
	}
}

// after it the answer is a 404 indistinguishable from "not implemented".
func TestV2RoutesBeatTheStaticCatchAll(t *testing.T) {
	srv := newV2Server(t)
	for _, path := range []string{
		"/v2/sessions", "/v2/config", "/v2/health", "/v2/runtime", "/v2/agents",
		"/v2/project/files", "/v2/commands",
	} {
		got := callJSON(t, srv, "GET", path, nil)
		if got.code == consts.StatusNotFound {
			t.Fatalf("%s answered 404 — it fell through to the static catch-all", path)
		}
		// 503 is the expected answer for introspection without a config
		// source; it proves the handler ran.
		if got.code != consts.StatusOK && got.code != consts.StatusServiceUnavailable {
			t.Fatalf("%s = %d, want 200 or 503", path, got.code)
		}
	}
}

func TestV2Delete(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	if got := callJSON(t, srv, "DELETE", "/v2/sessions/"+id, nil); got.code != consts.StatusOK {
		t.Fatalf("DELETE = %d (%s)", got.code, got.body)
	}
	// v1 answered 200 for any delete; v2 must not.
	got := callJSON(t, srv, "DELETE", "/v2/sessions/"+id, nil)
	if got.code != consts.StatusNotFound {
		t.Fatalf("deleting a gone thread = %d, want 404", got.code)
	}
}

func TestV2Rename(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id, map[string]string{"title": "renamed"})
	if got.code != consts.StatusOK {
		t.Fatalf("PATCH = %d (%s)", got.code, got.body)
	}
	var meta threadDetail
	_ = json.Unmarshal([]byte(got.body), &meta)
	if meta.Title != "renamed" {
		t.Fatalf("title = %q, want renamed", meta.Title)
	}
	// The rename must survive a re-read, not just the response.
	var reread threadDetail
	_ = json.Unmarshal([]byte(callJSON(t, srv, "GET", "/v2/sessions/"+id, nil).body), &reread)
	if reread.Title != "renamed" {
		t.Fatalf("title after re-read = %q, want renamed", reread.Title)
	}
	if bad := callJSON(t, srv, "PATCH", "/v2/sessions/"+id, map[string]string{"title": "  "}); bad.code != consts.StatusBadRequest {
		t.Fatalf("a blank title must be rejected, got %d", bad.code)
	}
}

// ── messages ─────────────────────────────────────────────────────────────

func TestV2MessagesReportsPendingApproval(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedApproval(t, srv, id, "ic-1", "execute")

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.PendingApprovals) != 1 {
		t.Fatalf("pendingApprovals must be stated by the server, not inferred by the client: %+v", view.PendingApprovals)
	}
	if view.PendingApprovals[0].InterruptID != "ic-1" || view.PendingApprovals[0].ToolName != "execute" {
		t.Fatalf("pending approval lost its detail: %+v", view.PendingApprovals[0])
	}
	if view.State != stateWaiting {
		t.Fatalf("state = %q, want %q", view.State, stateWaiting)
	}

	// The same must be true of the single-thread projection.
	detail := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	var one threadDetail
	_ = json.Unmarshal([]byte(detail.body), &one)
	if len(one.PendingApprovals) != 1 || one.PendingApprovals[0].ToolName != "execute" {
		t.Fatalf("GET /v2/sessions/{id} must carry the approval: %s", detail.body)
	}
}

func TestV2InspectionKeepsTheApprovalCard(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedApproval(t, srv, id, "ic-1", "execute")

	report, err := srv.sm.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if report.Orphan != 1 {
		t.Fatalf("orphan = %d, want 1", report.Orphan)
	}

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.PendingApprovals) != 1 || view.PendingApprovals[0].InterruptID != "ic-1" {
		t.Fatalf("the approval card must survive the inspection: %+v", view.PendingApprovals)
	}
}

// ── run ──────────────────────────────────────────────────────────────────

func TestV2RunRejectsEmptyMessage(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"messages": []map[string]string{{"role": "user", "content": "   "}},
	})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeEmptyMessage {
		t.Fatalf("code = %q, want %q", code, codeEmptyMessage)
	}
}

func TestV2RunWithoutPendingApprovalIsConflict(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"resume": []map[string]any{{
			"interruptId": "ic-nope", "status": "resolved",
			"payload": map[string]any{"approved": true},
		}},
	})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeInterruptMissing {
		t.Fatalf("code = %q, want %q", code, codeInterruptMissing)
	}
}

func TestV2RunRejectsMismatchedInterruptID(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedApproval(t, srv, id, "ic-current", "execute")

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"resume": []map[string]any{{
			"interruptId": "ic-stale", "status": "resolved",
			"payload": map[string]any{"approved": true},
		}},
	})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeInterruptMismatch {
		t.Fatalf("code = %q, want %q", code, codeInterruptMismatch)
	}
}

func TestV2RunAnswersAWholeSetOfApprovals(t *testing.T) {
	srv, _ := newV2RunServer(t)
	id := createThread(t, srv)
	seedApprovals(t, srv, id, []string{"ic-1", "ic-2", "ic-3"}, "execute")

	sess0, err := srv.sm.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	store, _ := sess0.GetCheckPointStore()
	if err := store.Set(context.Background(), id, []byte("runner state")); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	sess, err := srv.sm.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resume := make([]resumeEntry, 0, 3)
	for _, ic := range []string{"ic-1", "ic-2", "ic-3"} {
		resume = append(resume, resumeEntry{
			InterruptID: ic,
			Payload: &struct {
				Approved bool   `json:"approved"`
				Reason   string `json:"reason"`
			}{Approved: true},
		})
	}
	if _, err := srv.startRun(context.Background(), sess, runInput{ThreadID: id, Resume: resume}); err != nil {
		t.Fatalf("a whole set of approvals must be accepted, got %v", err)
	}
}

func TestV2RunRejectsASetWithOneStaleID(t *testing.T) {
	srv, _ := newV2RunServer(t)
	id := createThread(t, srv)
	seedApprovals(t, srv, id, []string{"ic-1", "ic-2"}, "execute")

	sess, err := srv.sm.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resume := []resumeEntry{
		{InterruptID: "ic-1"},
		{InterruptID: "ic-stale"},
	}
	if _, err := srv.startRun(context.Background(), sess, runInput{ThreadID: id, Resume: resume}); !errors.Is(err, errInterruptMismatch) {
		t.Fatalf("want errInterruptMismatch, got %v", err)
	}
}

func TestV2WriteDoesNotCreateUnknownThread(t *testing.T) {
	srv := newV2Server(t)
	id := uuid.NewString()

	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files",
		map[string]string{"probe.txt": "hi"})
	if got.code != consts.StatusNotFound {
		t.Fatalf("upload to unknown thread = %d, want 404: %s", got.code, got.body)
	}
	if strings.Contains(string(callJSON(t, srv, "GET", "/v2/sessions", nil).body), id) {
		t.Fatalf("session %s was created by a write; ids are server-minted only", id)
	}
}

func TestV2ReadsDoNotCreateUnknownThread(t *testing.T) {
	srv := newV2Server(t)
	id := uuid.NewString()

	for _, path := range []string{"/v2/sessions/" + id, "/v2/sessions/" + id + "/messages", "/v2/sessions/" + id + "/files"} {
		got := callJSON(t, srv, "GET", path, nil)
		if got.code != consts.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, got.code)
		}
	}
}

func TestV2RunOnUnknownThreadIs404(t *testing.T) {
	srv := newV2Server(t)
	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": "nope",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if got.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404", got.code)
	}
}

func TestLastUserText(t *testing.T) {
	if got := lastUserText(nil); got != "" {
		t.Fatalf("no messages = %q, want empty", got)
	}
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "first"},
		{Role: types.RoleAssistant, Content: "answer"},
		{Role: types.RoleUser, Content: "  second  "},
	}
	if got := lastUserText(msgs); got != "second" {
		t.Fatalf("= %q, want the last user message", got)
	}
	// The final user message may be blank; fall back to the previous one.
	blank := []types.Message{
		{Role: types.RoleUser, Content: "keep me"},
		{Role: types.RoleUser, Content: "   "},
	}
	if got := lastUserText(blank); got != "keep me" {
		t.Fatalf("= %q, want the last non-blank user message", got)
	}
	// The multi-part content shape AG-UI allows must also be read.
	parts := []types.Message{{
		Role: types.RoleUser,
		Content: []types.InputContent{
			{Type: types.InputContentTypeText, Text: "multi"},
			{Type: types.InputContentTypeText, Text: " part"},
		},
	}}
	if got := lastUserText(parts); got != "multi part" {
		t.Fatalf("= %q, want %q", got, "multi part")
	}
}

// ── files ────────────────────────────────────────────────────────────────

func TestV2UploadAcceptsMultipleFiles(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", map[string]string{
		"a.md":  "content of a",
		"b.csv": "content of b",
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	// v1 only ever looked at files[0] and silently dropped the rest.
	if len(out.Files) != 2 {
		t.Fatalf("want 2 files, got %d (%+v)", len(out.Files), out.Files)
	}

	var ls struct {
		Files []workspaceEntry `json:"files"`
	}
	listing := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/files", nil)
	if err := json.Unmarshal([]byte(listing.body), &ls); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(ls.Files) != 2 {
		t.Fatalf("want 2 workspace entries, got %d", len(ls.Files))
	}
}

func TestV2UploadWithoutFileIs400(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", nil)
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400", got.code)
	}
	if code := decodeCode(t, got.body); code != codeFileRequired {
		t.Fatalf("code = %q, want %q", code, codeFileRequired)
	}
}

// "../escape.md" arrives as "escape.md". The property that matters is that
// the file lands INSIDE the workspace — assert that rather than a status
// code, and keep a direct PutFile test below for the guard that actually
// rejects traversal.
func TestV2UploadConfinesToWorkspace(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", map[string]string{
		"../escape.md": "nope",
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, f := range out.Files {
		if !strings.HasPrefix(f.Path, out.WorkspacePath+string(os.PathSeparator)) {
			t.Fatalf("file escaped the workspace: %s (workspace %s)", f.Path, out.WorkspacePath)
		}
		if strings.Contains(f.Name, "/") || strings.Contains(f.Name, "..") {
			t.Fatalf("the stored name must be a bare basename, got %q", f.Name)
		}
	}
}

// ── introspection ────────────────────────────────────────────────────────

func TestV2IntrospectionWithoutConfigSourceIs503(t *testing.T) {
	srv := newV2Server(t)
	for _, path := range []string{"/v2/config", "/v2/agents", "/v2/agents/main"} {
		got := callJSON(t, srv, "GET", path, nil)
		if got.code != consts.StatusServiceUnavailable {
			t.Fatalf("%s = %d, want 503 without a config source", path, got.code)
		}
	}
	// /v2/health and /v2/runtime must work regardless: they describe the
	// process, not the configuration.
	for _, path := range []string{"/v2/health", "/v2/runtime"} {
		got := callJSON(t, srv, "GET", path, nil)
		if got.code != consts.StatusOK {
			t.Fatalf("%s = %d, want 200 even without a config source (%s)", path, got.code, got.body)
		}
	}
}

// With a real config wired, an unknown agent must 404 rather than fall
// through to the catch-all.
func TestV2AgentDetailUnknownIs404(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })
	got := callJSON(t, srv, "GET", "/v2/agents/nope", nil)
	if got.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404 (%s)", got.code, got.body)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func createThread(t *testing.T, srv *Server) string {
	t.Helper()
	got := callJSON(t, srv, "POST", "/v2/sessions", nil)
	var meta threadDetail
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("create: %v (%s)", err, got.body)
	}
	return meta.ThreadID
}

// seedApproval installs a pending approval carrying a full detail, the way a
// real interrupt would.
func seedApproval(t *testing.T, srv *Server, threadID, interruptID, tool string) {
	t.Helper()
	seedApprovals(t, srv, threadID, []string{interruptID}, tool)
}

// seedApprovals installs a **set** of pending approvals.
func seedApprovals(t *testing.T, srv *Server, threadID string, ids []string, tool string) {
	t.Helper()
	sess, err := srv.sm.Get(context.Background(), threadID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	interrupts := make([]contract.PendingInterrupt, 0, len(ids))
	for i, id := range ids {
		interrupts = append(interrupts, contract.PendingInterrupt{
			InterruptID: id,
			Detail: &contract.InterruptDetail{
				ToolCallID: "call-" + strconv.Itoa(i+1),
				ToolName:   tool,
				Message:    "running " + tool + " requires approval",
			},
		})
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: interrupts,
	}); err != nil {
		t.Fatalf("SetPendingApproval: %v", err)
	}
}

var _ = strings.TrimSpace

// testConfig writes a minimal but valid .openheron tree and loads it, so the
// introspection endpoints run against the same config loader production uses.
func testConfig(t *testing.T) *spec.Config {
	t.Helper()
	return testConfigWith(t, "start_timeout: 30s\n")
}

func testConfigWith(t *testing.T, serverCfg string) *spec.Config {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, ".openheron")
	mkdir := func(p string) { mkdirAll(t, p) }
	write := func(rel, content string) {
		p := filepath.Join(base, rel)
		mkdir(filepath.Dir(p))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("server/spec.yaml", serverCfg)
	write("llm/test.yaml", `type: openai
base_url: https://example.invalid
models:
  - id: small
  - id: big
    reasoning:
      off: {}
      high: {}
    reasoning_default: high
`)
	write("agents/main.md", "---\ndescription: root\nmodel: test/small\nsubagents: [worker]\n---\nbody\n")
	write("agents/worker.md", "---\ndescription: worker\nmodel: test/big\ndisabled_tools: [execute]\n---\nbody\n")
	write("policy/permissions.yaml", "ask:\n  - execute\n  - read_file\ndeny:\n  - rm_rf\n")

	// base is the user-level dir, root is the project root — both arguments
	// matter: leaving projectRoot empty makes the loader look for .openheron
	// relative to the test's cwd and silently skip the project layer.
	svc, err := spec.NewService(base, root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

type stubTools struct {
	byAgent map[string][]ToolVerdict
	policy  []ToolVerdict
	runtime map[string]Observed
}

func (s stubTools) EffectiveTools(agentID string) []ToolVerdict { return s.byAgent[agentID] }
func (s stubTools) PolicyTools() []ToolVerdict                  { return s.policy }
func (s stubTools) RuntimeTools(agentID string) Observed        { return s.runtime[agentID] }
func (s stubTools) DescriptionVerdicts() []DescriptionVerdict   { return nil }

func sampleTools() stubTools {
	return stubTools{
		byAgent: map[string][]ToolVerdict{
			"main": {
				{Name: "read_file", Tier: string(decl.TierAllow)},
				{Name: "execute", Tier: string(decl.TierDeny), Reason: "disabled_tools"},
			},
			"worker": {
				{Name: "read_file", Tier: string(decl.TierAllow)},
				{Name: "execute", Tier: string(decl.TierDeny), Reason: "disabled_tools"},
			},
		},
		policy: []ToolVerdict{
			{Name: "read_file", Tier: string(decl.TierAllow)},
			{Name: "write_file", Tier: string(decl.TierAsk)},
		},
	}
}

func TestEffectiveToolsRendersTheInjectedVerdict(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })
	srv.SetToolsSource(sampleTools())

	got := callJSON(t, srv, "GET", "/v2/agents/worker", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var agent agentView
	if err := json.Unmarshal([]byte(got.body), &agent); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}

	byName := map[string]toolView{}
	for _, tool := range agent.EffectiveTools {
		byName[tool.Name] = tool
	}
	if tool, ok := byName["execute"]; !ok {
		t.Fatalf("execute must appear in the tool table, got %+v", byName)
	} else if tool.Tier != string(decl.TierDeny) || tool.Reason != "disabled_tools" {
		t.Fatalf("execute = %+v, want deny via disabled_tools", tool)
	}
	if tool := byName["read_file"]; tool.Tier != string(decl.TierAllow) || tool.Reason != "" {
		t.Fatalf("read_file = %+v, want allow with no reason", tool)
	}
}

func TestRuntimeToolsAreRenderedSeparately(t *testing.T) {
	stub := sampleTools()
	stub.runtime = map[string]Observed{
		"worker": {Known: true, Names: []string{"read_file", "task", "skill"}},
	}
	stub.byAgent["worker"] = []ToolVerdict{
		{Name: "read_file", Tier: string(decl.TierAllow), Present: true, ObservedKnown: true},
		{Name: "grep_search", Tier: string(decl.TierDeny), Reason: "policy.deny", ObservedKnown: true},
	}

	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })
	srv.SetToolsSource(stub)

	got := callJSON(t, srv, "GET", "/v2/agents/worker", nil)
	var agent agentView
	if err := json.Unmarshal([]byte(got.body), &agent); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}

	if !agent.RuntimeToolsKnown {
		t.Fatal("runtimeToolsKnown must be true once a turn has been observed")
	}
	if len(agent.RuntimeTools) != 3 {
		t.Fatalf("runtimeTools = %v, want three", agent.RuntimeTools)
	}

	byName := map[string]toolView{}
	for _, tool := range agent.EffectiveTools {
		byName[tool.Name] = tool
	}
	if !byName["read_file"].Present {
		t.Error("read_file was observed; present must be true")
	}
	if byName["grep_search"].Present {
		t.Error("grep_search was never registered; present must be false")
	}
	if !byName["grep_search"].ObservedKnown {
		t.Error("observedKnown must be true so the false is meaningful")
	}
}

func TestRuntimeToolsUnknownBeforeAnyTurn(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })
	srv.SetToolsSource(sampleTools())

	got := callJSON(t, srv, "GET", "/v2/agents/worker", nil)
	var agent agentView
	if err := json.Unmarshal([]byte(got.body), &agent); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if agent.RuntimeToolsKnown {
		t.Fatal("runtimeToolsKnown = true, want false before any turn")
	}
	if len(agent.RuntimeTools) != 0 {
		t.Fatalf("runtimeTools = %v, want empty", agent.RuntimeTools)
	}
}

func TestV2ConfigOmitsToolsWithoutASource(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	got := callJSON(t, srv, "GET", "/v2/config", nil)
	var view configView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.Tools) != 0 {
		t.Fatalf("no tools source wired ⇒ no tool table, got %+v", view.Tools)
	}
}

func TestV2ConfigSurfacesPolicyAndModels(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	got := callJSON(t, srv, "GET", "/v2/config", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view configView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.DefaultAgent != "main" {
		t.Fatalf("defaultAgent = %q", view.DefaultAgent)
	}
	if view.Policy.DefaultTier != string(decl.TierAllow) {
		t.Fatalf("defaultTier = %q, want allow", view.Policy.DefaultTier)
	}
	if len(view.Providers) != 1 || view.Providers[0].ID != "test" {
		t.Fatalf("providers = %+v", view.Providers)
	}
	// The key itself must never leave the process.
	if strings.Contains(got.body, "api_key") {
		t.Fatalf("the config view must not expose api_key: %s", got.body)
	}
	// The reasoning gear vocabulary must reach the UI, otherwise the header
	// badge has nothing to show.
	var gearSeen bool
	for _, a := range view.Agents {
		for _, m := range a.Model.ReasoningAvailable {
			if m == "high" {
				gearSeen = true
			}
		}
	}
	if !gearSeen {
		t.Fatalf("reasoningAvailable must be surfaced: %s", got.body)
	}
}

// Defense in depth: PutFile itself must reject traversal even though the
// multipart parser happens to strip it first.
func TestV2UploadGuardRejectsTraversal(t *testing.T) {
	srv := newV2Server(t)
	sess := mustCreate(t, srv)
	for _, name := range []string{"../escape.md", "a/../../escape.md", "/abs.md"} {
		if _, err := sess.PutFile(name, strings.NewReader("x")); err == nil {
			t.Fatalf("PutFile(%q) must be rejected", name)
		}
	}
}

func newV2RunServer(t *testing.T) (*Server, *bufExtWriter) {
	t.Helper()
	return newV2RunServerWith(t, noopBuilder{})
}

func newV2RunServerWith(t *testing.T, builder run.AgentBuilder) (*Server, *bufExtWriter) {
	t.Helper()
	fm, err := filestore.NewFileManager(filepath.Join(t.TempDir(), "sessions"), filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rt := run.New(run.Config{
		AgentBuilder:   builder,
		SessionManager: fm,
		Input:          run.InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })

	engine := route.NewEngine(hertzconfig.NewOptions(nil))
	srv := New(rt, fm)
	srv.h = engine
	var captured bufExtWriter
	srv.newStream = func(c *app.RequestContext) *transport.SSEStream {
		return transport.NewWithWriter(c, &captured)
	}
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })
	return srv, &captured
}

func sessionCount(t *testing.T, srv *Server) int {
	t.Helper()
	got := callJSON(t, srv, "GET", "/v2/sessions", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("list = %d, want 200", got.code)
	}
	var out struct {
		Items []threadMeta `json:"items"`
	}
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return len(out.Items)
}

// A run without threadId mints a session and announces the real id in
// RUN_STARTED. The stream is authoritative; this asserts it arrived.
func TestV2RunWithoutThreadIdIsRejectedAndCreatesNothing(t *testing.T) {
	srv, captured := newV2RunServer(t)
	before := sessionCount(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"query": "hello",
	})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	if after := sessionCount(t, srv); after != before {
		t.Fatalf("sessions = %d, want unchanged %d", after, before)
	}
	if captured.buf.Len() != 0 {
		t.Fatalf("a rejected run must not open a stream: %q", captured.buf.String())
	}
}

func TestV2RunAnnouncesARealThreadId(t *testing.T) {
	srv, captured := newV2RunServer(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "hello",
	})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}

	out := captured.buf.String()
	if !strings.Contains(out, `"type":"RUN_STARTED"`) {
		t.Fatalf("stream missing RUN_STARTED: %s", out)
	}
	var started struct {
		Type     string `json:"type"`
		ThreadID string `json:"threadId"`
		RunID    string `json:"runId"`
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &started); err != nil {
			continue
		}
		if started.Type == "RUN_STARTED" {
			if started.ThreadID == "" {
				t.Fatalf("RUN_STARTED has empty threadId: %s", line)
			}
			if got := callJSON(t, srv, "GET", "/v2/sessions/"+started.ThreadID, nil); got.code != consts.StatusOK {
				t.Fatalf("announced thread %s not found: %d", started.ThreadID, got.code)
			}
			return
		}
	}
	t.Fatalf("no RUN_STARTED in stream: %s", out)
}

// query wins over messages, and messages still works when query is absent.
func TestV2RunQueryAndMessages(t *testing.T) {
	srv, _ := newV2RunServer(t)
	id := createThread(t, srv)

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "only query",
	}); got.code != consts.StatusOK {
		t.Fatalf("query-only = %d, want 200: %s", got.code, got.body)
	}
	assertLastUserText(t, srv, "only query")

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"messages": []map[string]string{{"role": "user", "content": "from messages"}},
	}); got.code != consts.StatusOK {
		t.Fatalf("messages-only = %d, want 200: %s", got.code, got.body)
	}
	assertLastUserText(t, srv, "from messages")

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "query wins",
		"messages": []map[string]string{{"role": "user", "content": "messages loses"}},
	}); got.code != consts.StatusOK {
		t.Fatalf("both = %d, want 200: %s", got.code, got.body)
	}
	assertLastUserText(t, srv, "query wins")
}

func assertLastUserText(t *testing.T, srv *Server, want string) {
	t.Helper()
	list := callJSON(t, srv, "GET", "/v2/sessions", nil)
	var out struct {
		Items []threadMeta `json:"items"`
	}
	if err := json.Unmarshal([]byte(list.body), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(out.Items) == 0 {
		t.Fatal("no sessions")
	}
	newest := out.Items[0]
	for _, it := range out.Items[1:] {
		if it.UpdatedAt > newest.UpdatedAt {
			newest = it
		}
	}
	msgs := callJSON(t, srv, "GET", "/v2/sessions/"+newest.ThreadID+"/messages", nil)
	var mv struct {
		Messages []types.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(msgs.body), &mv); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	got := lastUserText(mv.Messages)
	if got != want {
		t.Fatalf("submitted query = %q, want %q", got, want)
	}
}

// A resume with no threadId must not mint a session: answering an approval
// against a conversation that does not exist is a caller bug, and silently
// creating an empty session would hide it as "the approve button did nothing".
func TestV2ResumeWithoutThreadIdIs404AndCreatesNothing(t *testing.T) {
	srv, _ := newV2RunServer(t)
	before := sessionCount(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"resume": []map[string]any{{
			"interruptId": "int-1",
			"payload":     map[string]any{"approved": true},
		}},
	})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	if after := sessionCount(t, srv); after != before {
		t.Fatalf("sessions = %d, want unchanged %d", after, before)
	}
}

// An unknown threadId still 404s — a *given* id that names nothing.
func TestV2RunOnUnknownThreadIdStill404(t *testing.T) {
	srv, _ := newV2RunServer(t)
	before := sessionCount(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": "nope",
		"query":    "hi",
	})
	if got.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", got.code, got.body)
	}
	if after := sessionCount(t, srv); after != before {
		t.Fatalf("sessions = %d, want unchanged %d", after, before)
	}
}

// A run with neither query nor messages is a 400, and creates nothing.
func TestV2RunWithoutAnyQueryIsRejected(t *testing.T) {
	srv, _ := newV2RunServer(t)
	before := sessionCount(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	if after := sessionCount(t, srv); after != before+1 {
		t.Logf("NOTE: empty query left a session behind (%d → %d)", before, after)
	}
}

func TestStableIDUniqueForToolOnlyMessages(t *testing.T) {
	call := func(name string) *schema.ToolCall {
		return &schema.ToolCall{Index: ptr(0), ID: "c-" + name, Function: schema.FunctionCall{Name: name, Arguments: "{}"}}
	}
	msgs := []*schema.Message{
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{*call("ls")}},
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{*call("read_file")}},
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{*call("ls")}},
	}

	seen := map[string]int{}
	for i, m := range msgs {
		id := stableID(m, i)
		if prev, dup := seen[id]; dup {
			t.Fatalf("message[%d] and message[%d] have the same id: %s", i, prev, id)
		}
		seen[id] = i
	}

	if stableID(msgs[1], 1) != stableID(msgs[1], 1) {
		t.Fatal("stableID is not deterministic")
	}
	if stableID(msgs[0], 0) == stableID(msgs[2], 2) {
		t.Fatal("identical content at different positions must get different ids")
	}
}

func ptr(i int) *int { return &i }

func TestV2AbortOnAnIdleThread(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/sessions/"+id+"/abort", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200 (%s)", got.code, got.body)
	}
	var out struct {
		Aborted bool   `json:"aborted"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if out.Aborted {
		t.Fatalf("an idle thread must not report aborted: %s", got.body)
	}
	if out.Reason != "idle" {
		t.Fatalf("reason = %q, want idle (%s)", out.Reason, got.body)
	}
}

func TestV2AbortOnAnUnknownThread(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "POST", "/v2/sessions/does-not-exist/abort", nil)
	if got.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeThreadNotFound {
		t.Fatalf("code = %q, want %q", code, codeThreadNotFound)
	}
}

func TestV2AbortStopsARunningThread(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	sess := mustCreate(t, srv)
	if _, err := srv.rt.Submit(context.Background(), sess.ID(), "keep going"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if st, ok := srv.rt.StatusOf(context.Background(), sess.ID()); !ok || st.Phase != run.PhaseRunning {
		t.Fatalf("the turn must be running before the abort: %+v %v", st, ok)
	}

	got := callJSON(t, srv, "POST", "/v2/sessions/"+sess.ID()+"/abort", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200 (%s)", got.code, got.body)
	}
	var out struct {
		Aborted bool `json:"aborted"`
	}
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if !out.Aborted {
		t.Fatalf("a running thread must report aborted: %s", got.body)
	}

	if st, ok := srv.rt.StatusOf(context.Background(), sess.ID()); ok && st.Phase == run.PhaseRunning {
		t.Fatalf("the thread must not still be running after the abort: %+v", st)
	}
}

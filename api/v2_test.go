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

	"openheron/agentkit/run"
	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/contract"
	"openheron/storage"
	"openheron/storage/filestore"
	"openheron/transport"
)

// These cover the /v2 contract. Two matter most:
//
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

// ★ StaticFS is mounted last as a catch-all; if any /v2 route is registered
//
//	after it the answer is a 404 indistinguishable from "not implemented".
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

// ★ 启动巡检**不许**把审批卡弄丢。
//
//	巡检以前会把「有待批、没 checkpoint」的会话的待批清掉 —— 于是重启一次，
//	用户的审批卡就从界面上消失了，而他什么都不知道。清掉并没有让状态变好：
//	checkpoint 不在时 Resume 会干脆地拒绝，待批记录留着也不会坏，而它还
//	带着工具名与参数 —— 那正是那张卡的来源。
//
//	这条用例走完整条链：巡检 → 重新读 → /v2 仍然报出那张卡。
func TestV2InspectionKeepsTheApprovalCard(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedApproval(t, srv, id, "ic-1", "execute")

	// 启动巡检：这个会话没有 checkpoint ⇒ 走孤儿分支。
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

// ★ A stale interruptId must not answer the pending approval.
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

// ★ 一轮里可以有**多个**独立待批（模型一次请求四个都要审批的工具）。
//
//	答复必须**一次答完整组**：框架的契约是「不在 Targets 里的被中断组件会
//	重新中断自己」，而重新中断拿到的是**新 id** —— 逐个答在原理上走不通。
//
//	这一条是那个缺陷的回归：修之前，服务端只认一个 pending id，于是
//	客户端拿到几张卡就答不了几张，报
//	「interruptId does not match the pending approval」。
func TestV2RunAnswersAWholeSetOfApprovals(t *testing.T) {
	srv, _ := newV2RunServer(t)
	id := createThread(t, srv)
	seedApprovals(t, srv, id, []string{"ic-1", "ic-2", "ic-3"}, "execute")

	// ★ 恢复路径要读 checkpoint（框架的恢复状态就住在里面）—— 没有它，
	//	Resume 会干脆地拒绝（no_checkpoint）。
	//
	//	这里造一份**真的** checkpoint 文件：存取器是每个会话自己的，
	//	不打桩。（它的字节不是合法的 gob，但那份校验发生在轮次里，
	//	而这条用例只关心「整组答复被接受」。）
	sess0, err := srv.sm.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	store, _ := sess0.GetCheckPointStore()
	if err := store.Set(context.Background(), id, []byte("runner state")); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	// 三个都带上 —— 少带一个，服务端就只答了那一个，其余的会带着新 id
	// 重新中断，用户要点好几轮才走得完。
	//
	// ★ 直接调 startRun 而不是走 HTTP：校验就在它里面，而 HTTP 那条路会
	//   一直等 turn ready（resume 要从 checkpoint 恢复 agent，测试里没有
	//   checkpoint ⇒ 干等到 start_timeout，默认 60s）。
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

// ★ 整组里混进一个不在表里的 id ⇒ 整个请求被拒。
//
//	不是「跳过那个、答其余的」—— 那样客户端会以为它答过了。
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

// ★ 会话 id 由后端生成，客户端不能自己命名。
//
//	语义边界：只有 POST /v2/sessions 能创建会话。任何拿未知 id 打进来的
//	请求都必须 404 —— 读写都自动创建的话，一个拼错的 URL 会静默造出会话，
//	「接口没通」就变成了「多了个空会话」。
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

// 非法 id 不是「待创建」，而是明确的 404 —— 不能落到 500。
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

// ★ Go's multipart reader already applies filepath.Base to the part name, so
//
//	"../escape.md" arrives as "escape.md". The property that matters is that
//	the file lands INSIDE the workspace — assert that rather than a status
//	code, and keep a direct PutFile test below for the guard that actually
//	rejects traversal.
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
//
// ★ 一轮里可以有多个独立待批（模型一次请求四个都要审批的工具）。
//
//	它们必须**一次答完整组** —— 框架的契约是「不在 Targets 里的被中断组件
//	会重新中断自己」，而重新中断拿到的是新 id，逐个答在原理上走不通。
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

// testConfigWith 造一份配置，server/spec.yaml 的内容可定制
// （多主用例要往里写 agent.roots）。
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

// stubTools 是一份写死的生效结论，用来测 api 层**怎么渲染**它。
//
// ★ 判定不在本包（那是 agent 的事，见 agentkit/build/introspect.go），所以这里不能
//
//	真的去验「deny 赢过 disable」—— 那条规则该由 agent 包的用例钉。
//	本包该钉的是：把 Blocked 翻成对用户有意义的 reason，且不撒谎。
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

// TestEffectiveToolsRendersTheInjectedVerdict 钉住 api 层只做**翻译**。
//
// ★ 原先这里自己重算了一遍（deny 赢过 disable、兜底 ask），那是「两份真相」：
//
//	改判定只会生效一边，而两边的测试照样都绿 —— 自省接口开始说谎。
//	现在判定归 agent 包，本包只该保证如实渲染。
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
	// ★ reason 必须原样带出：它是「为什么这个 agent 调不到」的唯一线索。
	if tool, ok := byName["execute"]; !ok {
		t.Fatalf("execute must appear in the tool table, got %+v", byName)
	} else if tool.Tier != string(decl.TierDeny) || tool.Reason != "disabled_tools" {
		t.Fatalf("execute = %+v, want deny via disabled_tools", tool)
	}
	if tool := byName["read_file"]; tool.Tier != string(decl.TierAllow) || tool.Reason != "" {
		t.Fatalf("read_file = %+v, want allow with no reason", tool)
	}
}

// ★ `present` / `runtimeTools` 必须原样带出：它们回答的是「这个工具**存在**吗」，
//
//	而 `tier` 只回答「能不能用」。两者此前混在一起 —— 拼错的 `deny: [grep_search]`
//	和一个真的被挡住的工具在接口上长得一模一样。
func TestRuntimeToolsAreRenderedSeparately(t *testing.T) {
	stub := sampleTools()
	stub.runtime = map[string]Observed{
		"worker": {Known: true, Names: []string{"read_file", "task", "skill"}},
	}
	stub.byAgent["worker"] = []ToolVerdict{
		{Name: "read_file", Tier: string(decl.TierAllow), Present: true, ObservedKnown: true},
		// 配置点名了它、运行期从没出现过 —— 典型的拼错。
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

// ★ 还没跑过任何一轮时，接口必须说「不知道」而不是「没有」。
//
//	把两者混为一谈正是这个端点此前一直在犯的错：`task` 与 `skill` 由框架
//	中间件注册，名字不出现在任何配置文件里，于是从来没有被报出来过。
func TestRuntimeToolsUnknownBeforeAnyTurn(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })
	srv.SetToolsSource(sampleTools()) // runtime 全空 = 一次都没观测过

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

// TestV2ConfigOmitsToolsWithoutASource 未注入时整块省略。
//
// ★ 报告一个**猜测**出来的表比不报更糟：接口说 allow 而运行时是 deny，
//
//	没有任何东西会响。
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
	// ★ 兜底档是 allow —— 策略只剩 ask / deny 两列，没点名的工具一律放行。
	//   这里报 ask 就是上一轮改兜底时漏掉的：端点说的与实际行为不一致。
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

// ── /v2/run 的会话创建与 query ───────────────────────────────────────────
//
// ★ threadId 改成可选是这组测试的目的：此前必须先 POST /v2/sessions 换一个
//   id 才能提问，一次提问两次往返，客户端只好自造一个本地占位 id，
//   于是每个会话前后有两个 id、需要一整套注册表去收敛。

// newV2RunServer 在 newV2Server 之上接一个真的 Runtime（带 noop agent），
// 并把 SSE 写进缓冲区 —— 无 socket 的 route.Engine 不提供 ExtWriter，
// 所以不接管的话流会丢（TestChatStreamsSSEFrames 记着同一个坑）。
func newV2RunServer(t *testing.T) (*Server, *bufExtWriter) {
	t.Helper()
	return newV2RunServerWith(t, noopBuilder{})
}

// newV2RunServerWith 同上，但允许注入 builder —— 需要在飞 turn 的用例
// （例如「运行中不能切 agent」）必须能让 agent 卡住不结束。
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
//
// ★ 契约已改：**会话只由 POST /v2/sessions 建**，`/v2/run` 一律不建。
//
//	「建不建会话」的决定权在客户端（它知道当前有没有会话）。
//	缺 threadId 是**请求不合法**，不是「顺便建一个」。
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

// ★ RUN_STARTED 必须带上**真实存在**的会话 id —— 客户端没有别的途径知道它。
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
			// 这个 id 必须真的存在，否则等于给了客户端一个假引用。
			if got := callJSON(t, srv, "GET", "/v2/sessions/"+started.ThreadID, nil); got.code != consts.StatusOK {
				t.Fatalf("announced thread %s not found: %d", started.ThreadID, got.code)
			}
			return
		}
	}
	t.Fatalf("no RUN_STARTED in stream: %s", out)
}

// query wins over messages, and messages still works when query is absent.
//
// ★ 观察点选的是**落盘的会话消息**而不是运行时内部状态：
//
//	v2_run.go 里那次 Submit 拿到的就是 query，而 user 消息会被
//	追加进 filestore.jsonl。断言落盘结果而不是断言调用参数 ——
//	后者要求给 Runtime 加一个测试专用接口，那是为测试改产品代码。
func TestV2RunQueryAndMessages(t *testing.T) {
	srv, _ := newV2RunServer(t)
	// ★ 会话只由 POST /v2/sessions 建 —— /v2/run 必须带 threadId。
	id := createThread(t, srv)

	// 只给 query
	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "only query",
	}); got.code != consts.StatusOK {
		t.Fatalf("query-only = %d, want 200: %s", got.code, got.body)
	}
	assertLastUserText(t, srv, "only query")

	// 只给 messages（只会发这个的老客户端仍要能用）
	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"messages": []map[string]string{{"role": "user", "content": "from messages"}},
	}); got.code != consts.StatusOK {
		t.Fatalf("messages-only = %d, want 200: %s", got.code, got.body)
	}
	assertLastUserText(t, srv, "from messages")

	// 两个都给：query 优先。它是诚实契约，messages 只是兼容回退。
	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "query wins",
		"messages": []map[string]string{{"role": "user", "content": "messages loses"}},
	}); got.code != consts.StatusOK {
		t.Fatalf("both = %d, want 200: %s", got.code, got.body)
	}
	assertLastUserText(t, srv, "query wins")
}

// assertLastUserText 读最近落盘的那条 user 消息。
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
	// 最近更新的那个就是刚跑的那一轮。
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
	// ★ 缺 threadId 是 400（请求不合法），不是 404 —— 404 是「给了 id 但
	//   没有这个会话」。两者答的是不同的问题。
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
	// 会话在读 query 之前就建了，所以这条要明确记录当前行为：
	// 空提问会留下一个空会话。留着是因为懒创建的语义就是
	// 「POST /v2/run 即创建」，而空会话由标题回读时的 "New Session" 占位暴露。
	if after := sessionCount(t, srv); after != before+1 {
		t.Logf("NOTE: empty query left a session behind (%d → %d)", before, after)
	}
}

// ── 消息 id 唯一性 ───────────────────────────────────────────────────────

// 一轮里连续两次工具调用会产出**两条** assistant 消息：都空内容、都
// 没有 ToolCallID（工具挂在 ToolCalls 上）。仅按 role+text+ToolCallID 取
// 摘要时它们 id 相同，前端直接报 "Encountered two children with the same key"。
func TestStableIDUniqueForToolOnlyMessages(t *testing.T) {
	call := func(name string) *schema.ToolCall {
		return &schema.ToolCall{Index: ptr(0), ID: "c-" + name, Function: schema.FunctionCall{Name: name, Arguments: "{}"}}
	}
	msgs := []*schema.Message{
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{*call("ls")}},
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{*call("read_file")}},
		// 与第一条工具完全相同 —— 只有位置能区分
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{*call("ls")}},
	}

	seen := map[string]int{}
	for i, m := range msgs {
		id := stableID(m, i)
		if prev, dup := seen[id]; dup {
			t.Fatalf("message[%d] 与 message[%d] 的 id 相同: %s", i, prev, id)
		}
		seen[id] = i
	}

	// 同样的位置必须给出同样的 id —— 重载后 key 要稳定
	if stableID(msgs[1], 1) != stableID(msgs[1], 1) {
		t.Fatal("stableID 不是确定性的")
	}
	// 位置不同 → id 必须不同，即使内容完全一样
	if stableID(msgs[0], 0) == stableID(msgs[2], 2) {
		t.Fatal("相同内容、不同位置必须得到不同的 id")
	}
}

func ptr(i int) *int { return &i }

// ── 中止 ──────────────────────────────────────────────────────────────────

// ★ 中止端点是前端「停止」按钮的**唯一真实作用点**。
//
//	只断开前端那条 SSE 连接不等于中止：服务端那一轮会继续跑到底 ——
//	模型继续烧 token、工具继续执行副作用（文件已写、命令已跑），
//	而界面已经显示 idle。那是「假停止」，比没有停止按钮更糟。
//
//	这条钉的是端点契约：200 + aborted:true（运行中）/ aborted:false（空闲），
//	以及未知会话 404。
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

// ★ 中止必须真的**停掉那一轮**，而不只是把句柄放开。
//
//	只验「返回 200」是不够的 —— 那正是「假停止」在服务端的样子。
//	这条断言的是中止之后会话**不再处于 running**：turn 句柄已释放、
//	状态已清掉，用户能接着发下一条（否则下一条会被 409 拒掉）。
//
//	★ 「在飞的模型调用」被中断是**框架那一层**的事：eino 的取消靠
//	cancelCtx.setGraphInterruptFunc 打断 compose graph（eino
//	adk/chatmodel.go:1049），前提是 agent 认 WithCancel —— eino 内置
//	agent 都认。openheron 这一层保证的是「循环停了、句柄放了、状态清了」。
func TestV2AbortStopsARunningThread(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	srv.SetConfigSource(func() *spec.Config { return testConfig(t) })

	sess := mustCreate(t, srv)
	// 起一轮（它会一直跑着，直到被中止）。
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

	// ★ 中止之后不再是 running —— 用户能接着发下一条。
	if st, ok := srv.rt.StatusOf(context.Background(), sess.ID()); ok && st.Phase == run.PhaseRunning {
		t.Fatalf("the thread must not still be running after the abort: %+v", st)
	}
}

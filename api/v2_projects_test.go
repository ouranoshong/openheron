package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/projects"
)

// These cover the /v2/projects contract. Three things matter most:
//   - the shape: one list, one scope, and the session membership is **not**
//     repeated in the items — it is the sessions endpoint's answer;
//   - the 404: "no such project" and "no such session" are different mistakes,
//     so they carry different codes;
//   - the registry-less server: the endpoints answer 503, not an empty list,
//     because "this build does not know about projects" is not "no projects".

// newProjectsServer wires a registry into a v2 server.
//
// ★ The table lives in its own temporary directory: the project table is a
//
//	user-level file, and a test that writes into the shared data directory
//	would leave its projects behind for the next test to read.
func newProjectsServer(t *testing.T) (*Server, *projects.Registry) {
	t.Helper()
	srv := newV2Server(t)
	reg, err := projects.Open(filepath.Join(t.TempDir(), projects.FileName))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	srv.SetProjectsRegistry(reg)
	return srv, reg
}

func decodeProjectList(t *testing.T, body string) projectList {
	t.Helper()
	var out projectList
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode project list: %v (%s)", err, body)
	}
	return out
}

func decodeProjectView(t *testing.T, body string) projectView {
	t.Helper()
	var out projectView
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode project: %v (%s)", err, body)
	}
	return out
}

func TestV2ProjectsWithoutTheRegistryIs503(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "GET", "/v2/projects", nil)
	if got.code != consts.StatusServiceUnavailable {
		t.Fatalf("GET /v2/projects = %d, want 503 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeInternal {
		t.Fatalf("code = %q, want %q", code, codeInternal)
	}

	post := callJSON(t, srv, "POST", "/v2/projects", map[string]any{"name": "x", "root": t.TempDir()})
	if post.code != consts.StatusServiceUnavailable {
		t.Fatalf("POST /v2/projects = %d, want 503 (%s)", post.code, post.body)
	}
}

func TestV2ProjectLifecycle(t *testing.T) {
	srv, _ := newProjectsServer(t)
	root := filepath.Join(t.TempDir(), "work")

	created := callJSON(t, srv, "POST", "/v2/projects", map[string]any{"name": "work", "root": root})
	if created.code != consts.StatusCreated {
		t.Fatalf("POST /v2/projects = %d, want 201 (%s)", created.code, created.body)
	}
	p := decodeProjectView(t, created.body)
	if p.ID == "" {
		t.Fatal("the created project must carry an id")
	}
	if p.Name != "work" {
		t.Fatalf("name = %q, want %q", p.Name, "work")
	}
	if p.Root != root {
		t.Fatalf("root = %q, want %q", p.Root, root)
	}
	if p.SessionCount != 0 {
		t.Fatalf("a fresh project holds no session, got %d", p.SessionCount)
	}

	list := callJSON(t, srv, "GET", "/v2/projects", nil)
	if list.code != consts.StatusOK {
		t.Fatalf("GET /v2/projects = %d (%s)", list.code, list.body)
	}
	out := decodeProjectList(t, list.body)
	if len(out.Items) != 1 {
		t.Fatalf("want 1 project, got %d", len(out.Items))
	}
	if out.Items[0].ID != p.ID {
		t.Fatalf("listed id = %q, want %q", out.Items[0].ID, p.ID)
	}
	// ★ The multi-project mode has no scope: the process serves every project.
	if out.Scope != nil {
		t.Fatalf("scope = %+v, want null in the multi-project mode", out.Scope)
	}

	patched := callJSON(t, srv, "PATCH", "/v2/projects/"+p.ID, map[string]any{"name": "renamed"})
	if patched.code != consts.StatusOK {
		t.Fatalf("PATCH /v2/projects = %d (%s)", patched.code, patched.body)
	}
	got := decodeProjectView(t, patched.body)
	if got.Name != "renamed" {
		t.Fatalf("name = %q, want %q", got.Name, "renamed")
	}
	if got.ID != p.ID {
		t.Fatalf("a patch must not move the id: %q -> %q", p.ID, got.ID)
	}

	deleted := callJSON(t, srv, "DELETE", "/v2/projects/"+p.ID, nil)
	if deleted.code != consts.StatusOK {
		t.Fatalf("DELETE /v2/projects = %d (%s)", deleted.code, deleted.body)
	}

	after := callJSON(t, srv, "GET", "/v2/projects", nil)
	out = decodeProjectList(t, after.body)
	if len(out.Items) != 0 {
		t.Fatalf("the project must be gone, got %d", len(out.Items))
	}
}

func TestV2ProjectScopeIsTheFocusedProject(t *testing.T) {
	srv, reg := newProjectsServer(t)
	root := t.TempDir()
	p, err := reg.Ensure(context.Background(), root)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	srv.SetProjectScope(p.ID)

	list := callJSON(t, srv, "GET", "/v2/projects", nil)
	out := decodeProjectList(t, list.body)
	if out.Scope == nil {
		t.Fatal("the single-project mode must report its scope")
	}
	if out.Scope.ProjectID != p.ID {
		t.Fatalf("scope id = %q, want %q", out.Scope.ProjectID, p.ID)
	}
	if out.Scope.Root != p.Root {
		t.Fatalf("scope root = %q, want %q", out.Scope.Root, p.Root)
	}
}

func TestV2UnknownProjectIs404(t *testing.T) {
	srv, _ := newProjectsServer(t)

	patched := callJSON(t, srv, "PATCH", "/v2/projects/nope", map[string]any{"name": "x"})
	if patched.code != consts.StatusNotFound {
		t.Fatalf("PATCH /v2/projects/nope = %d, want 404 (%s)", patched.code, patched.body)
	}
	if code := decodeCode(t, patched.body); code != codeProjectNotFound {
		t.Fatalf("code = %q, want %q", code, codeProjectNotFound)
	}

	deleted := callJSON(t, srv, "DELETE", "/v2/projects/nope", nil)
	if deleted.code != consts.StatusNotFound {
		t.Fatalf("DELETE /v2/projects/nope = %d, want 404 (%s)", deleted.code, deleted.body)
	}
	if code := decodeCode(t, deleted.body); code != codeProjectNotFound {
		t.Fatalf("code = %q, want %q", code, codeProjectNotFound)
	}
}

func TestV2CreateProjectWithoutRootIs400(t *testing.T) {
	srv, _ := newProjectsServer(t)

	got := callJSON(t, srv, "POST", "/v2/projects", map[string]any{"name": "work"})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("POST /v2/projects = %d, want 400 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeInvalidBody {
		t.Fatalf("code = %q, want %q", code, codeInvalidBody)
	}
}

// ── the session relation ──────────────────────────────────────────────────

func TestV2SessionCarriesItsProject(t *testing.T) {
	srv, reg := newProjectsServer(t)
	root := t.TempDir()
	p, err := reg.Ensure(context.Background(), root)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	created := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"projectId": p.ID})
	if created.code != consts.StatusCreated {
		t.Fatalf("POST /v2/sessions = %d, want 201 (%s)", created.code, created.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(created.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, created.body)
	}
	if meta.ProjectID != p.ID {
		t.Fatalf("projectId = %q, want %q", meta.ProjectID, p.ID)
	}

	list := callJSON(t, srv, "GET", "/v2/sessions", nil)
	var out threadList
	if err := json.Unmarshal([]byte(list.body), &out); err != nil {
		t.Fatalf("decode list: %v (%s)", err, list.body)
	}
	if len(out.Items) != 1 {
		t.Fatalf("want 1 session, got %d", len(out.Items))
	}
	if out.Items[0].ProjectID != p.ID {
		t.Fatalf("listed projectId = %q, want %q", out.Items[0].ProjectID, p.ID)
	}

	one := callJSON(t, srv, "GET", "/v2/sessions/"+meta.ThreadID, nil)
	if one.code != consts.StatusOK {
		t.Fatalf("GET /v2/sessions = %d (%s)", one.code, one.body)
	}
	var detail threadDetail
	if err := json.Unmarshal([]byte(one.body), &detail); err != nil {
		t.Fatalf("decode detail: %v (%s)", err, one.body)
	}
	if detail.ProjectID != p.ID {
		t.Fatalf("detail projectId = %q, want %q", detail.ProjectID, p.ID)
	}
}

func TestV2SessionWithoutAProjectHasNoProjectId(t *testing.T) {
	srv, _ := newProjectsServer(t)

	created := callJSON(t, srv, "POST", "/v2/sessions", nil)
	if created.code != consts.StatusCreated {
		t.Fatalf("POST /v2/sessions = %d, want 201 (%s)", created.code, created.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(created.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, created.body)
	}
	// ★ The field is always emitted: an empty string says "no project", and
	//   a missing field would say "this build does not know about projects".
	if meta.ProjectID != "" {
		t.Fatalf("projectId = %q, want empty", meta.ProjectID)
	}
	if !strings.Contains(created.body, `"projectId"`) {
		t.Fatalf("the field must be emitted even when empty: %s", created.body)
	}
}

func TestV2CreateSessionInAnUnknownProjectIs404(t *testing.T) {
	srv, _ := newProjectsServer(t)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"projectId": "nope"})
	if got.code != consts.StatusNotFound {
		t.Fatalf("POST /v2/sessions = %d, want 404 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeProjectNotFound {
		t.Fatalf("code = %q, want %q", code, codeProjectNotFound)
	}
	// ★ The session must not exist: a request that names no project cannot
	//   leave a session behind that belongs to nobody.
	list := callJSON(t, srv, "GET", "/v2/sessions", nil)
	var out threadList
	if err := json.Unmarshal([]byte(list.body), &out); err != nil {
		t.Fatalf("decode list: %v (%s)", err, list.body)
	}
	if len(out.Items) != 0 {
		t.Fatalf("want no session, got %d", len(out.Items))
	}
}

func TestV2DeleteSessionRemovesItFromTheTable(t *testing.T) {
	srv, reg := newProjectsServer(t)
	p, err := reg.Ensure(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	created := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"projectId": p.ID})
	if created.code != consts.StatusCreated {
		t.Fatalf("POST /v2/sessions = %d (%s)", created.code, created.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(created.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, created.body)
	}

	deleted := callJSON(t, srv, "DELETE", "/v2/sessions/"+meta.ThreadID, nil)
	if deleted.code != consts.StatusOK {
		t.Fatalf("DELETE /v2/sessions = %d (%s)", deleted.code, deleted.body)
	}

	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, ok := snap.ProjectOf(meta.ThreadID); ok {
		t.Fatal("the deleted session must not be listed in the project any more")
	}
}

// ── the @-mention picker ─────────────────────────────────────────────────

// ★ The picker lists the files of **one** project, and the query says
//
//	which: the session on screen decides. ★ The files come from the
//	project's own directory, so a project that moved is listed under its
//	new one.
func TestProjectFilesListTheProjectTheQueryNames(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	reg, err := projects.Open(filepath.Join(t.TempDir(), projects.FileName))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	srv.SetProjectsRegistry(reg)

	root := filepath.Join(t.TempDir(), "beta")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := reg.Create(context.Background(), "beta", root)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got := callJSON(t, srv, "GET", "/v2/project/files?project="+p.ID, nil)
	if got.code != consts.StatusOK {
		t.Fatalf("GET /v2/project/files = %d (%s)", got.code, got.body)
	}
	var out projectFileList
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	found := false
	for _, f := range out.Files {
		if f.Path == "beta.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the project's own file must be listed: %s", got.body)
	}
}

func TestProjectFilesOfAnUnknownProjectIs404(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	reg, err := projects.Open(filepath.Join(t.TempDir(), projects.FileName))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	srv.SetProjectsRegistry(reg)

	got := callJSON(t, srv, "GET", "/v2/project/files?project=nope", nil)
	if got.code != consts.StatusNotFound {
		t.Fatalf("GET /v2/project/files = %d, want 404 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeProjectNotFound {
		t.Fatalf("code = %q, want %q", code, codeProjectNotFound)
	}
}

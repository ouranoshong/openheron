package api

import (
	"context"
	"encoding/json"
	"path/filepath"
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

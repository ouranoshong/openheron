package projects

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

func TestTheTableIsCreatedOnTheFirstWrite(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()

	p, err := reg.Create(ctx, "openheron", filepath.Join(t.TempDir(), "openheron"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" || p.Name != "openheron" {
		t.Fatalf("the project must carry an id and a name, got %+v", p)
	}
	if _, err := os.Stat(reg.Path()); err != nil {
		t.Fatalf("the table must be on disk: %v", err)
	}
	// ★ The sessions array is written even when it is empty: an empty array
	// says "this project has no sessions", a missing field says "this file
	// is old".
	b, err := os.ReadFile(reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"sessions": []`) {
		t.Fatalf("the sessions array must be written empty:\n%s", b)
	}
}

func TestTheTableIsReadFresh(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "api")

	if _, err := reg.Create(ctx, "api", root); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// ★ The table is a user-level file, and the user may edit it. A read
	// that answered from a cache would not see the edit.
	raw, err := os.ReadFile(reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	var table Table
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	table.Projects[0].Name = "renamed by hand"
	edited, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg.Path(), edited, 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "renamed by hand" {
		t.Fatalf("the table must be read fresh, got %+v", list)
	}
}

func TestASessionBelongsToTheProjectThatHoldsIt(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "api")

	p, err := reg.Create(ctx, "api", root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg.Attach(ctx, "session-1", p.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got, ok := snap.ProjectOf("session-1")
	if !ok {
		t.Fatal("the session must belong to the project that holds it")
	}
	if got.ID != p.ID {
		t.Fatalf("the session belongs to %q, want %q", got.ID, p.ID)
	}
	if _, ok := snap.ProjectOf("session-2"); ok {
		t.Fatal("a session nobody holds belongs to no project")
	}
}

func TestASessionIsNotMovedBetweenProjects(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()

	first, err := reg.Create(ctx, "first", filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := reg.Create(ctx, "second", filepath.Join(t.TempDir(), "second"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg.Attach(ctx, "session-1", first.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// ★ Moving a session is not a thing this table does: its transcript, its
	// workspace and its checkpoint belong to the project it was born in.
	if err := reg.Attach(ctx, "session-1", second.ID); err == nil {
		t.Fatal("moving a session must be refused")
	}
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := snap.ProjectOf("session-1")
	if got.ID != first.ID {
		t.Fatalf("the session must stay in %q, got %q", first.ID, got.ID)
	}
}

func TestAttachingTwiceIsHarmless(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	p, err := reg.Create(ctx, "api", filepath.Join(t.TempDir(), "api"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := reg.Attach(ctx, "session-1", p.ID); err != nil {
			t.Fatalf("Attach %d: %v", i, err)
		}
	}
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := snap.Get(p.ID)
	if len(got.Sessions) != 1 {
		t.Fatalf("the session must be listed once, got %v", got.Sessions)
	}
}

func TestDeleteLeavesTheSessionsAlone(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	p, err := reg.Create(ctx, "api", filepath.Join(t.TempDir(), "api"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg.Attach(ctx, "session-1", p.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := reg.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Get(p.ID); ok {
		t.Fatal("the project must be gone")
	}
	// ★ The session is not in any list, so it belongs to no project — and
	// it still exists: a project is a label on a directory.
	if _, ok := snap.ProjectOf("session-1"); ok {
		t.Fatal("the session must belong to no project")
	}
}

func TestTheProjectIdIsUnique(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()

	first, err := reg.Create(ctx, "", filepath.Join(t.TempDir(), "api"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := reg.Create(ctx, "", filepath.Join(t.TempDir(), "api"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("two projects must not share an id: %q", first.ID)
	}
	if first.Name != "api" {
		t.Fatalf("the name defaults to the directory's name, got %q", first.Name)
	}
}

func TestCreateIsIdempotentForOneRoot(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "api")

	first, err := reg.Create(ctx, "api", root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := reg.Create(ctx, "api", root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("the same root must be one project, got %q and %q", first.ID, second.ID)
	}
}

func TestEnsureAddsTheRootItWasGiven(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "work")

	p, err := reg.Ensure(ctx, root)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if p.Root != filepath.Clean(root) {
		t.Fatalf("the root must be absolute and clean, got %q", p.Root)
	}
	again, err := reg.Ensure(ctx, root)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if again.ID != p.ID {
		t.Fatalf("Ensure must be idempotent, got %q and %q", p.ID, again.ID)
	}
}

func TestUpdateChangesTheProject(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	p, err := reg.Create(ctx, "api", filepath.Join(t.TempDir(), "api"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	name := "renamed"
	order := 7
	got, err := reg.Update(ctx, p.ID, Patch{Name: &name, Order: &order})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != name || got.Order != order {
		t.Fatalf("the patch must be applied, got %+v", got)
	}
	if got.ID != p.ID {
		t.Fatalf("the id must not move, got %q", got.ID)
	}
}

func TestUpdateOfAnUnknownProjectFails(t *testing.T) {
	reg := newRegistry(t)
	name := "x"
	if _, err := reg.Update(context.Background(), "nope", Patch{Name: &name}); err == nil {
		t.Fatal("an unknown project must be an error")
	}
}

func TestTheProjectsComeBackInOrder(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	second, err := reg.Create(ctx, "second", filepath.Join(t.TempDir(), "second"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := reg.Create(ctx, "first", filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reg.Update(ctx, first.ID, Patch{Order: intPtr(-1)}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("the order must be kept, got %+v", list)
	}
}

func TestDetachRemovesTheSession(t *testing.T) {
	reg := newRegistry(t)
	ctx := context.Background()
	p, err := reg.Create(ctx, "api", filepath.Join(t.TempDir(), "api"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg.Attach(ctx, "session-1", p.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := reg.Detach(ctx, "session-1"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.ProjectOf("session-1"); ok {
		t.Fatal("the session must belong to no project")
	}
}

func TestTheLocationFollowsTheEnvironment(t *testing.T) {
	named := filepath.Join(t.TempDir(), "elsewhere.json")
	t.Setenv(FileEnv, named)
	if got := Location(t.TempDir()); got != named {
		t.Fatalf("the environment must win, got %q", got)
	}
	t.Setenv(FileEnv, "")
	want := filepath.Join("data", FileName)
	if got := Location("data"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func intPtr(v int) *int { return &v }

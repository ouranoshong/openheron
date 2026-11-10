package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/fsx"
)

// writeSkillFile writes one skill: a folder under the skills directory, with
// a SKILL.md whose frontmatter names it.
//
// The folder and the frontmatter name are deliberately allowed to differ —
// that is the case the folder-name allow-list has to survive.
func writeSkillFile(t *testing.T, dir, folder, name string) {
	t.Helper()
	d := filepath.Join(dir, folder)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", d, err)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: the %s skill\n---\n\nDo %s things.\n", name, name, name)
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write the skill: %v", err)
	}
}

func testSkillBackend(t *testing.T, dir string) skill.Backend {
	t.Helper()
	backend, err := fsx.New(fsx.Config{Root: dir})
	if err != nil {
		t.Fatalf("fsx.New: %v", err)
	}
	b, err := skill.NewBackendFromFilesystem(context.Background(), &skill.BackendFromFilesystemConfig{
		Backend: backend,
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("NewBackendFromFilesystem: %v", err)
	}
	return b
}

func skillNames(t *testing.T, b skill.Backend) []string {
	t.Helper()
	matters, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	out := make([]string, 0, len(matters))
	for _, m := range matters {
		out = append(out, m.Name)
	}
	return out
}

func scopeOfNames(names ...string) []string {
	return names
}

// ★ The default is **none**, like every other list on an agent: an agent
//
//	that says nothing about skills gets no skill.
func TestAnAgentWithoutASkillListGetsNoSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "alpha")
	writeSkillFile(t, dir, "beta", "beta")

	scoped, err := newScopedSkillBackend(context.Background(),
		testSkillBackend(t, dir), skillScopeOf(nil), nil)
	if err != nil {
		t.Fatalf("newScopedSkillBackend: %v", err)
	}
	if got := len(skillNames(t, scoped)); got != 0 {
		t.Fatalf("the agent sees %d skills, want none (%v)", got, skillNames(t, scoped))
	}
}

func TestTheSkillListLimitsWhatTheAgentSees(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "alpha")
	writeSkillFile(t, dir, "beta", "beta")

	scoped, err := newScopedSkillBackend(context.Background(),
		testSkillBackend(t, dir), skillScopeOf(scopeOfNames("alpha")), nil)
	if err != nil {
		t.Fatalf("newScopedSkillBackend: %v", err)
	}
	names := skillNames(t, scoped)
	if len(names) != 1 || names[0] != "alpha" {
		t.Fatalf("the agent sees %v, want only alpha", names)
	}
}

func TestTheSkillListAlsoRefusesToLoadASkillThatIsNotAllowed(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "alpha")
	writeSkillFile(t, dir, "beta", "beta")

	scoped, err := newScopedSkillBackend(context.Background(),
		testSkillBackend(t, dir), skillScopeOf(scopeOfNames("alpha")), nil)
	if err != nil {
		t.Fatalf("newScopedSkillBackend: %v", err)
	}
	if _, err := scoped.Get(context.Background(), "beta"); err == nil {
		t.Fatal("a skill outside the list must not be loadable")
	}
	if _, err := scoped.Get(context.Background(), "alpha"); err != nil {
		t.Fatalf("the listed skill must be loadable: %v", err)
	}
}

func TestTheSkillListAcceptsTheFolderName(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "code-tools", "code-tools-v2")

	scoped, err := newScopedSkillBackend(context.Background(),
		testSkillBackend(t, dir), skillScopeOf(scopeOfNames("code-tools")), nil)
	if err != nil {
		t.Fatalf("newScopedSkillBackend: %v", err)
	}
	names := skillNames(t, scoped)
	if len(names) != 1 || names[0] != "code-tools-v2" {
		t.Fatalf("the folder name must select the skill, got %v", names)
	}
}

func TestAnEmptySkillListMeansNoSkillAtAll(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "alpha")

	scoped, err := newScopedSkillBackend(context.Background(),
		testSkillBackend(t, dir), skillScopeOf(scopeOfNames()), nil)
	if err != nil {
		t.Fatalf("newScopedSkillBackend: %v", err)
	}
	if got := skillNames(t, scoped); len(got) != 0 {
		t.Fatalf("an empty list must mean no skill, got %v", got)
	}
}

func TestASkillThatIsNotThereIsReported(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "alpha")

	scoped, err := newScopedSkillBackend(context.Background(),
		testSkillBackend(t, dir), skillScopeOf(scopeOfNames("alpha", "nope")), nil)
	if err != nil {
		t.Fatalf("newScopedSkillBackend: %v", err)
	}
	missing := scoped.missingSkills()
	if len(missing) != 1 || missing[0] != "nope" {
		t.Fatalf("missing = %v, want [nope]", missing)
	}
}

// The list the model sees is rendered from the backend, so the scope has to
// reach the tool description — that is where the model learns what it may use.
func TestTheScopeReachesTheSkillToolDescription(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "alpha")
	writeSkillFile(t, dir, "beta", "beta")
	t.Setenv("EINO_EXT_SKILLS_DIR", dir)

	backend, err := fsx.New(fsx.Config{Root: dir})
	if err != nil {
		t.Fatalf("fsx.New: %v", err)
	}
	mw, err := skillChain(context.Background(), backend, nil, skillScopeOf(scopeOfNames("alpha")), nil)
	if err != nil {
		t.Fatalf("skillChain: %v", err)
	}
	if mw == nil {
		t.Fatal("the skill middleware must be built when a skills directory exists")
	}
	_, runCtx, err := mw.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatalf("BeforeAgent: %v", err)
	}
	var info *schema.ToolInfo
	for _, tl := range runCtx.Tools {
		if ti, err := tl.Info(context.Background()); err == nil && ti.Name == "skill" {
			info = ti
			break
		}
	}
	if info == nil {
		t.Fatalf("the skill tool must be registered, tools = %v", runCtx.Tools)
	}
	if !strings.Contains(info.Desc, "alpha") {
		t.Fatalf("the listed skill must be advertised:\n%s", info.Desc)
	}
	if strings.Contains(info.Desc, "beta") {
		t.Fatalf("the skill outside the list must not be advertised:\n%s", info.Desc)
	}
}

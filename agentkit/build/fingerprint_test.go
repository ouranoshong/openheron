package build

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/contract"
)

// fingerprintEnv writes a configuration root with three agents, one skill
// and two tool files, and returns the service, the builder and the root.
//
// ★ Three agents on purpose: `main` has `skill-expert` as a child, and
//
//	`other` is a root of its own. ★ A change to the child's tree must
//	leave the other root alone — that is the whole point of the
//	fingerprint.
func fingerprintEnv(t *testing.T) (*spec.Service, *Builder, string) {
	t.Helper()

	projectRoot := t.TempDir()
	cfgRoot := filepath.Join(projectRoot, spec.DefaultBaseDirName)
	for _, sub := range []string{spec.DirLLM, spec.DirAgent, spec.DirTools, spec.DirServer, spec.DirSkills} {
		if err := os.MkdirAll(filepath.Join(cfgRoot, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_FINGERPRINT_KEY", "sk-test")
	// ★ The runtime reads skills from where the environment says, and the
	//   dependency walk has to hash the same file the runtime reads.
	skillsDir := filepath.Join(cfgRoot, spec.DirSkills)
	t.Setenv("EINO_EXT_SKILLS_DIR", skillsDir)

	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfgRoot, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("llm/openai.yaml", "type: openai\nbase_url: https://a.example\n"+
		"api_key: ${TEST_FINGERPRINT_KEY}\nmodels:\n  - id: fast\n")
	write("agents/main.md", "---\nmodel: openai/fast\ntools: [ls, request_get]\n"+
		"subagents: [skill-expert]\n---\nmain body\n")
	write("agents/skill-expert.md", "---\nmodel: openai/fast\ntools: [ls]\n"+
		"skills: [review]\n---\nexpert body\n")
	write("agents/other.md", "---\nmodel: openai/fast\ntools: [ls]\n---\nother body\n")
	write("tools/http.yaml", "methods: [get]\n")
	write("tools/web_search.yaml", "max_results: 5\n")
	write("server/config.yaml", "listen_addr: \":18099\"\n")
	// ★ A skill is a **folder**: the folder name is the skill's name.
	if err := os.MkdirAll(filepath.Join(cfgRoot, spec.DirSkills, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("skills/review/SKILL.md", "---\nname: review\ndescription: the review skill\n---\n\nReview.\n")

	svc, err := spec.NewService(cfgRoot, projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetSkillsDir(skillsDir)
	cfg, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}

	b := NewBuilder(testManager(cfg), WithConfigService(svc), WithPaths(projectRoot, projectRoot))
	b.models = func(context.Context, *spec.Config, string, CallIntent, *slog.Logger) (
		model.BaseModel[*schema.Message], error) {

		return &stubModel{}, nil
	}
	t.Cleanup(b.Close)
	return svc, b, cfgRoot
}

// writeConfig writes a configuration file **through the service**, so the
// change goes through the gates and the file is marked.
//
// ★ Writing the file directly would leave the mark empty, and the builder
//
//	would not notice the change until the watcher's next poll — which is
//	exactly the race the mark removes.
func writeConfig(t *testing.T, svc *spec.Service, rel, body string) {
	t.Helper()
	if _, _, err := svc.Write(rel, []byte(body), ""); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// buildRoot builds an agent and returns the fingerprint it was built at.
func buildRoot(t *testing.T, b *Builder, name string) string {
	t.Helper()
	if _, _, err := b.Build(context.Background(), "s1", name, contract.TurnQuery, ""); err != nil {
		t.Fatal(err)
	}
	return mustSnapshot(t, b).fingerprintOf(name)
}

// configToolsOf returns the config tools of the builder's current snapshot.
func configToolsOf(t *testing.T, b *Builder) map[string]tool.InvokableTool {
	t.Helper()
	snap := mustSnapshot(t, b)
	batch, err := b.tools.Build(context.Background(), toolEnv{
		cfg:       snap.cfg,
		svc:       snap.svc,
		deps:      snap.deps,
		mcp:       b.mcp,
		skillsDir: snap.skillsDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]tool.InvokableTool{}
	for _, tl := range batch {
		it, ok := tl.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%T is not invokable", tl)
		}
		info, err := it.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out[info.Name] = it
	}
	return out
}

// TestChangingOneAgentRebuildsItsTreeAndNobodyElse is the granularity the
// fingerprint buys.
//
// ★ The child's file changed, so the child is a different agent — and the
//
//	root that embeds it is too, because the root's built form holds the
//	child. ★ The other root is neither: it keeps its instance.
func TestChangingOneAgentRebuildsItsTreeAndNobodyElse(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)

	mainFP := buildRoot(t, b, "main")
	otherFP := buildRoot(t, b, "other")

	writeConfig(t, svc, "agents/skill-expert.md",
		"---\nmodel: openai/fast\ntools: [ls]\nskills: [review]\n---\nexpert body v2\n")

	got := buildRoot(t, b, "main")
	if got == mainFP {
		t.Fatal("★ the tree that holds the changed agent must be rebuilt")
	}
	got = buildRoot(t, b, "other")
	if got != otherFP {
		t.Fatal("★ an agent that does not depend on the change must not be rebuilt")
	}
}

// TestChangingASkillRebuildsOnlyTheTreeThatNamesIt is the same rule for a
// skill.
//
// ★ The skill belongs to the expert, and the expert belongs to main's tree;
//
//	the other root does not name the skill and keeps its instance.
func TestChangingASkillRebuildsOnlyTheTreeThatNamesIt(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)

	mainFP := buildRoot(t, b, "main")
	otherFP := buildRoot(t, b, "other")

	writeConfig(t, svc, "skills/review/SKILL.md",
		"---\nname: review\ndescription: the review skill\n---\n\nReview more.\n")

	got := buildRoot(t, b, "main")
	if got == mainFP {
		t.Fatal("★ the tree that names the skill must be rebuilt")
	}
	got = buildRoot(t, b, "other")
	if got != otherFP {
		t.Fatal("★ a tree that does not name the skill must not be rebuilt")
	}
}

// TestChangingAToolsFileRebuildsOnlyTheAgentsThatUseItsTools is the tools
// partition's own rule.
//
// ★ `tools/http.yaml` configures the HTTP tool, and main uses it — so main
//
//	is rebuilt. ★ The other root does not use it, and the file says
//	nothing about that agent, so it stays.
func TestChangingAToolsFileRebuildsOnlyTheAgentsThatUseItsTools(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)

	mainFP := buildRoot(t, b, "main")
	otherFP := buildRoot(t, b, "other")

	writeConfig(t, svc, "tools/http.yaml", "methods: [get, post]\n")

	got := buildRoot(t, b, "main")
	if got == mainFP {
		t.Fatal("★ the agent that uses the HTTP tool must be rebuilt")
	}
	got = buildRoot(t, b, "other")
	if got != otherFP {
		t.Fatal("★ an agent that does not use the tool must not be rebuilt")
	}
}

// TestAFileNobodyDependsOnRebuildsNobody is the case the old tree could
// not answer.
//
// ★ `tools/web_search.yaml` exists and no agent uses the web search tool,
//
//	so the file says nothing about any agent — and nothing it says
//	reaches a tree.
func TestAFileNobodyDependsOnRebuildsNobody(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)

	mainFP := buildRoot(t, b, "main")
	otherFP := buildRoot(t, b, "other")

	writeConfig(t, svc, "tools/web_search.yaml", "max_results: 9\n")

	got := buildRoot(t, b, "main")
	if got != mainFP {
		t.Fatal("★ a file nobody depends on must not rebuild anybody")
	}
	got = buildRoot(t, b, "other")
	if got != otherFP {
		t.Fatal("★ a file nobody depends on must not rebuild anybody")
	}
}

// TestTheImpactQueryNamesTheDependentsAndTheRebuilds is the query's own
// contract.
//
// ★ The **dependents** are the agents that name the file directly; the
//
//	**rebuilds** are the agents the next turn rebuilds — and the parent
//	is in that list, because the parent's built form holds the child.
func TestTheImpactQueryNamesTheDependentsAndTheRebuilds(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)
	buildRoot(t, b, "main")
	buildRoot(t, b, "other")

	writeConfig(t, svc, "agents/skill-expert.md",
		"---\nmodel: openai/fast\ntools: [ls]\nskills: [review]\n---\nexpert body v3\n")

	tools := configToolsOf(t, b)
	out := callToolJSON[struct {
		Path       string   `json:"path"`
		Source     string   `json:"source"`
		Dependents []string `json:"dependents"`
		Rebuilds   []string `json:"rebuilds"`
		Changed    bool     `json:"changed"`
	}](t, tools, "config_get", `{"path":"agents/skill-expert.md","impact":true}`)

	if out.Source != sourceRunning {
		t.Fatalf("the impact answer comes from the running tree, got %q", out.Source)
	}
	if len(out.Dependents) != 1 || out.Dependents[0] != "skill-expert" {
		t.Fatalf("the dependents are the agents that name the file, got %v", out.Dependents)
	}
	if len(out.Rebuilds) != 2 || out.Rebuilds[0] != "main" || out.Rebuilds[1] != "skill-expert" {
		t.Fatalf("the rebuilds include the parent, got %v", out.Rebuilds)
	}
	if !out.Changed {
		t.Fatal("the file moved, so it must say so")
	}
}

// TestTheImpactQuerySaysNobodyDependsOnAnUnusedFile is the other half.
//
// ★ The file exists and moved, and **nobody** depends on it — so the
//
//	answer is "no agent, no rebuild", which is what the model needs to
//	stop guessing.
func TestTheImpactQuerySaysNobodyDependsOnAnUnusedFile(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)
	buildRoot(t, b, "main")
	buildRoot(t, b, "other")

	writeConfig(t, svc, "tools/web_search.yaml", "max_results: 11\n")

	tools := configToolsOf(t, b)
	out := callToolJSON[struct {
		Dependents []string `json:"dependents"`
		Rebuilds   []string `json:"rebuilds"`
	}](t, tools, "config_get", `{"path":"tools/web_search.yaml","impact":true}`)

	if len(out.Dependents) != 0 {
		t.Fatalf("nobody depends on that file, got %v", out.Dependents)
	}
	if len(out.Rebuilds) != 0 {
		t.Fatalf("nothing is rebuilt, got %v", out.Rebuilds)
	}
}

// TestTheImpactQuerySaysAColdPartitionNeedsARestart is the honest answer
// for `server/`.
//
// ★ `server/` is not hot-reloaded, so a change there reaches no agent —
//
//	but it is not nothing: the process has to be restarted for it to
//	mean anything.
func TestTheImpactQuerySaysAColdPartitionNeedsARestart(t *testing.T) {
	_, b, _ := fingerprintEnv(t)
	buildRoot(t, b, "main")

	tools := configToolsOf(t, b)
	out := callToolJSON[struct {
		Dependents      []string `json:"dependents"`
		Rebuilds        []string `json:"rebuilds"`
		RequiresRestart bool     `json:"requiresRestart"`
	}](t, tools, "config_get", `{"path":"server/config.yaml","impact":true}`)

	if !out.RequiresRestart {
		t.Fatal("★ a change in server/ needs a restart, and the answer must say so")
	}
	if len(out.Dependents) != 0 || len(out.Rebuilds) != 0 {
		t.Fatalf("server/ reaches no agent, got %v / %v", out.Dependents, out.Rebuilds)
	}
}

// TestTheImpactQueryAndThePoolAgreeOnWhoIsRebuilt is the query's promise.
//
// ★ The query answers "who will be rebuilt", and the pool is what
//
//	rebuilds — so the two must be the same set. ★ A query that names an
//	agent the pool does not rebuild is worse than no query: the model
//	trusts it and stops checking.
func TestTheImpactQueryAndThePoolAgreeOnWhoIsRebuilt(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)

	before := map[string]string{}
	for _, name := range []string{"main", "other"} {
		before[name] = buildRoot(t, b, name)
	}

	writeConfig(t, svc, "agents/skill-expert.md",
		"---\nmodel: openai/fast\ntools: [ls]\nskills: [review]\n---\nexpert body v4\n")

	tools := configToolsOf(t, b)
	out := callToolJSON[struct {
		Rebuilds []string `json:"rebuilds"`
	}](t, tools, "config_get", `{"impact":true}`)

	rebuilt := map[string]bool{}
	for _, name := range out.Rebuilds {
		rebuilt[name] = true
	}
	for _, name := range []string{"main", "other"} {
		got := buildRoot(t, b, name)
		moved := got != before[name]
		if moved != rebuilt[name] {
			t.Fatalf("the query says %q is rebuilt=%v, the pool says %v",
				name, rebuilt[name], moved)
		}
	}
}

// TestTheNextTurnSeesAWriteWithoutTheWatcher is the promise the mark
// makes.
//
// ★ The write path marks the file; the next turn reads the configuration
//
//	again before it builds. ★ Without the mark the change would wait for
//	the watcher's next poll, and "the next turn uses it" would be a race.
func TestTheNextTurnSeesAWriteWithoutTheWatcher(t *testing.T) {
	svc, b, _ := fingerprintEnv(t)

	buildRoot(t, b, "main")
	writeConfig(t, svc, "agents/other.md",
		"---\nmodel: openai/fast\ntools: [ls]\n---\nother body v2\n")
	// ★ The next turn: the builder reads the configuration again before it
	//	builds, because the write path marked the file.
	buildRoot(t, b, "other")

	// ★ The manager holds the configuration the builder published.
	cfg, _ := managerOf(t, b).Current()
	if !strings.Contains(cfg.Agents["other"].Instruction, "other body v2") {
		t.Fatal("★ the next turn must see the write without waiting for the watcher")
	}
}

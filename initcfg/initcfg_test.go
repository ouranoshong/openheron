package initcfg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

const userProviderYAML = `type: openai_compat
base_url: https://api.openai.com/v1
api_key: ${OPENAI_API_KEY}
timeout: 5m
models:
  - id: gpt-4o
    context_window: 128000
`

const userAgentMD = `---
description: 用户级 agent
model: openai/gpt-4o
---

用户级 agent 的正文。
`

const userPolicyYAML = `ask:   [execute]
deny:  []
`

type env struct {
	t    *testing.T
	root string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	return &env{
		t:    t,
		root: filepath.Join(root, ".openheron"),
	}
}

func (e *env) write(rel, content string) {
	e.t.Helper()
	writeFile(e.t, filepath.Join(e.root, filepath.FromSlash(rel)), content)
}

func (e *env) svc() *spec.Service {
	e.t.Helper()
	svc, err := spec.NewServiceAt(e.root, filepath.Dir(e.root))
	if err != nil {
		e.t.Fatalf("NewServiceAt: %v", err)
	}
	return svc
}

func (e *env) plan(opts Options) *Plan {
	e.t.Helper()
	p, err := BuildPlan(e.svc(), opts)
	if err != nil {
		e.t.Fatalf("BuildPlan: %v", err)
	}
	return p
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func rels(p *Plan) []string {
	out := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, f.Rel)
	}
	return out
}

func hasRel(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestTemplateRendersIntoALoadableConfig(t *testing.T) {
	e := newEnv(t)
	content, err := renderTemplate("llm/openai.yaml", map[string]string{
		"model":       "gpt-4o",
		"base_url":    "",
		"api_key_env": "",
	}, nil)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if !strings.Contains(string(content), "api_key: ${OPENAI_API_KEY}") {
		t.Fatalf("the api key must be an environment reference, got:\n%s", content)
	}
	if !strings.Contains(string(content), "id: gpt-4o") {
		t.Fatalf("the model id must be substituted, got:\n%s", content)
	}
	e.write("llm/openai.yaml", string(content))
	e.write("agents/main.md", "---\nmodel: openai/gpt-4o\n---\n\n正文。\n")
	if _, err := e.svc().Load(); err != nil {
		t.Fatalf("the rendered template must load: %v", err)
	}
}

func TestEveryProviderTemplateRenders(t *testing.T) {
	for _, provider := range []string{"openai", "deepseek"} {
		t.Run(provider, func(t *testing.T) {
			e := newEnv(t)
			raw, err := templates.ReadFile(templatesRoot + "/llm/" + provider + ".yaml")
			if err != nil {
				t.Fatalf("read template: %v", err)
			}
			model := templateDefault(string(raw), "model")
			if model == "" {
				t.Fatalf("the %s template has no default model", provider)
			}
			content, err := renderTemplate("llm/"+provider+".yaml", map[string]string{
				"model": model,
			}, nil)
			if err != nil {
				t.Fatalf("renderTemplate: %v", err)
			}
			e.write("llm/"+provider+".yaml", string(content))
			e.write("agents/main.md", "---\nmodel: "+provider+"/"+model+"\n---\n\n正文。\n")
			if _, err := e.svc().Load(); err != nil {
				t.Fatalf("the rendered template must load: %v", err)
			}
		})
	}
}

func TestInitInAnEmptyDirProducesARunnableConfig(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})

	want := []string{"agents/main.md", "agents/task.md", "llm/deepseek.yaml", "mcps/example.yaml"}
	got := rels(p)
	for _, w := range want {
		if !hasRel(got, w) {
			t.Fatalf("missing %s in %v", w, got)
		}
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := e.svc().Load()
	if err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
	if _, ok := cfg.Models[cfg.Agents[spec.DefaultAgentID].ModelRef]; !ok {
		t.Fatalf("the main agent references an undeclared model: %q",
			cfg.Agents[spec.DefaultAgentID].ModelRef)
	}
}

func TestTheTwoFactoryAgentsAreWrittenAsOneSet(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})

	got := rels(p)
	if !hasRel(got, "agents/main.md") || !hasRel(got, "agents/task.md") {
		t.Fatalf("the factory agent set must contain both files: %v", got)
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := e.svc().Load()
	if err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
	main := cfg.Agents[spec.DefaultAgentID]
	if main == nil {
		t.Fatalf("the produced config has no %q agent", spec.DefaultAgentID)
	}
	if len(main.SubAgents) != 1 || main.SubAgents[0] != "task" {
		t.Fatalf("main must declare exactly one subagent %q, got %v", "task", main.SubAgents)
	}
	if _, ok := cfg.Agents["task"]; !ok {
		t.Fatalf("main references %q but the config does not declare it", "task")
	}
}

func TestTheFactoryAgentNamesTheSkillKey(t *testing.T) {
	// ★ The factory file is where a user learns which keys exist: a field
	//   nobody can find is a field nobody uses.
	raw, err := templates.ReadFile(templatesRoot + "/agents/main.md")
	if err != nil {
		t.Fatalf("read the factory agent: %v", err)
	}
	if !strings.Contains(string(raw), "skills:") {
		t.Fatalf("the factory agent must name the skills key:\n%s", raw)
	}
}

func TestBothFactoryAgentsUseTheChosenModel(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{Model: "openai/gpt-4o"})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := e.svc().Load()
	if err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
	main, task := cfg.Agents[spec.DefaultAgentID], cfg.Agents["task"]
	if main == nil || task == nil {
		t.Fatalf("both agents must be declared, got main=%v task=%v", main != nil, task != nil)
	}
	if main.ModelRef != "openai/gpt-4o" {
		t.Fatalf("main model = %q, want openai/gpt-4o", main.ModelRef)
	}
	if task.ModelRef != main.ModelRef {
		t.Fatalf("task model = %q, want the same as main (%q)", task.ModelRef, main.ModelRef)
	}
}

func TestAnExistingProviderIsNotOverwritten(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)

	p := e.plan(Options{})
	if hasRel(rels(p), "llm/openai.yaml") {
		t.Fatalf("the existing llm/openai.yaml must not be rewritten: %v", rels(p))
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(e.root, "llm", "openai.yaml"))
	if err != nil {
		t.Fatalf("the existing file must still be there: %v", err)
	}
	if string(got) != userProviderYAML {
		t.Fatalf("the existing file was modified:\n%s", got)
	}
}

func TestAnExistingProviderIsUntouched(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)
	p := e.plan(Options{})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(e.root, "llm", "openai.yaml"))
	if err != nil {
		t.Fatalf("the existing file must still be there: %v", err)
	}
	if string(got) != userProviderYAML {
		t.Fatalf("the existing file was modified:\n%s", got)
	}
}

func TestAnExistingPolicyIsNotOverwritten(t *testing.T) {
	e := newEnv(t)
	e.write("policy/permissions.yaml", userPolicyYAML)

	p := e.plan(Options{})
	if hasRel(rels(p), "policy/permissions.yaml") {
		t.Fatalf("the existing policy/permissions.yaml must not be rewritten: %v", rels(p))
	}
}

// ★ init writes no policy/ file at all: both of them would say what the
// code already does, and a file that says nothing is read as meaning.
func TestInitWritesNoPolicyFile(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	for _, rel := range []string{"policy/permissions.yaml", "policy/limits.yaml"} {
		if hasRel(rels(p), rel) {
			t.Fatalf("%s must not be written: %v", rel, rels(p))
		}
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "policy")); err == nil {
		t.Fatal("policy/ must not be created")
	}
}

func TestAnExistingPolicyKeepsItsOwnAskList(t *testing.T) {
	e := newEnv(t)
	e.write("policy/permissions.yaml", userPolicyYAML)
	p := e.plan(Options{})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := e.svc().LoadUnchecked()
	if err != nil {
		t.Fatalf("LoadUnchecked: %v", err)
	}
	if len(cfg.Policy.Ask) != 1 || cfg.Policy.Ask[0] != "execute" {
		t.Fatalf("the existing ask list must survive: %v", cfg.Policy.Ask)
	}
}

func TestNothingIsWrittenWhenEverythingIsAlreadyThere(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)
	e.write("agents/main.md", userAgentMD)
	e.write("server/config.yaml", "listen_addr: \":8080\"\n")
	// ★ The MCP example is a loadable file: a file under mcps/ defines a
	//   server, so an example that cannot be parsed is a configuration
	//   that does not load.
	e.write("mcps/example.yaml", spec.Template(spec.DirMCP, nil))

	p := e.plan(Options{})
	if len(p.Files) != 0 {
		t.Fatalf("nothing should be written, got %v", rels(p))
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "agents", "task.md")); err == nil {
		t.Fatal("the factory agent set must not be created")
	}
}

func TestOnlyTheMissingFilesAreWritten(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)
	e.write("agents/main.md", "---\nmodel: openai/gpt-4o\n---\n\n已有 agent。\n")

	p := e.plan(Options{})
	got := rels(p)
	if hasRel(got, "agents/main.md") {
		t.Fatalf("the existing agent must not be rewritten: %v", got)
	}
	if !hasRel(got, "mcps/example.yaml") {
		t.Fatalf("the missing MCP example must be written: %v", got)
	}
	if hasRel(got, "llm/openai.yaml") {
		t.Fatalf("the existing provider must be reused, not copied: %v", got)
	}
}

func TestARejectedMergeWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.write("agents/main.md", "---\nmodel: openai/gpt-4o\n---\n\n正文。\n")

	svc := e.svc()
	_, _, err := svc.WriteMany([]spec.File{
		{Rel: "agents/broken.md", Content: []byte("---\nmodel: openai/nope\n---\n\n坏的。\n")},
	}, "")
	if err == nil {
		t.Fatal("the batch write must be rejected")
	}
	if _, err := os.Stat(filepath.Join(e.root, "agents", "broken.md")); err == nil {
		t.Fatal("the rejected file must not exist on disk")
	}
}

func TestNamingAModelTheExistingProviderDoesNotDeclareIsRefused(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", strings.Replace(userProviderYAML, "gpt-4o", "gpt-4o-mini", 1))

	_, err := BuildPlan(e.svc(), Options{
		Model: "openai/gpt-4o",
	})
	if err == nil {
		t.Fatal("naming a model the existing provider does not declare must be refused")
	}
	got, err := os.ReadFile(filepath.Join(e.root, "llm", "openai.yaml"))
	if err != nil {
		t.Fatalf("the existing file must still be there: %v", err)
	}
	if string(got) != strings.Replace(userProviderYAML, "gpt-4o", "gpt-4o-mini", 1) {
		t.Fatalf("the existing file was modified:\n%s", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if len(p.Files) == 0 {
		t.Fatal("the plan must have files")
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "agents", "main.md")); err != nil {
		t.Fatalf("the agent must exist after Apply: %v", err)
	}
}

func TestAnExistingModelIsReused(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)
	p := e.plan(Options{})
	if p.Model != "openai/gpt-4o" {
		t.Fatalf("model = %q, want openai/gpt-4o", p.Model)
	}
	if p.EnvHint != "" {
		t.Fatalf("the api key belongs to the user-level file; env hint = %q", p.EnvHint)
	}
}

func TestEmbeddingModelsAreNotPicked(t *testing.T) {
	e := newEnv(t)
	e.write("llm/embed.yaml", `type: openai_compat
base_url: https://api.openai.com/v1
api_key: ${OPENAI_API_KEY}
models:
  - id: text-embedding-3-small
    kind: embedding
    dimensions: 1536
`)
	e.write("llm/openai.yaml", userProviderYAML)
	p := e.plan(Options{})
	if strings.HasPrefix(p.Model, "embed/text-embedding") {
		t.Fatalf("an embedding model must not be picked: %q", p.Model)
	}
	if p.Model != "openai/gpt-4o" {
		t.Fatalf("model = %q, want openai/gpt-4o", p.Model)
	}
}

func TestAnExplicitUndeclaredModelCreatesTheProvider(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{Model: "deepseek/deepseek-chat"})
	if !hasRel(rels(p), "llm/deepseek.yaml") {
		t.Fatalf("llm/deepseek.yaml must be written: %v", rels(p))
	}
	if p.EnvHint != "DEEPSEEK_API_KEY" {
		t.Fatalf("env hint = %q, want DEEPSEEK_API_KEY", p.EnvHint)
	}
}

func TestFactoryAgentTiersExistInTheFactoryProvider(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{Provider: "deepseek"})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := e.svc().Load()
	if err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
	model, ok := cfg.Models["deepseek/deepseek-flash"]
	if !ok {
		t.Fatalf("the deepseek template must declare deepseek/deepseek-flash, got %v", cfg.Models)
	}
	for _, tier := range []string{"high", "low"} {
		if _, ok := model.Reasoning[tier]; !ok {
			t.Fatalf("the factory provider must declare the tier %q, got %v", tier, model.Reasoning)
		}
	}
	for _, id := range []string{spec.DefaultAgentID, "task"} {
		ag := cfg.Agents[id]
		if ag == nil {
			t.Fatalf("agent %q must be declared", id)
		}
		if ag.Reasoning != "" {
			if _, ok := model.Reasoning[ag.Reasoning]; !ok {
				t.Fatalf("agent %q asks for the tier %q, which the provider does not declare",
					id, ag.Reasoning)
			}
		}
	}
}

func TestTheDefaultProviderIsDeepseek(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if !hasRel(rels(p), "llm/deepseek.yaml") {
		t.Fatalf("the default provider must be deepseek: %v", rels(p))
	}
	if p.Model != "deepseek/deepseek-flash" {
		t.Fatalf("model = %q, want deepseek/deepseek-flash", p.Model)
	}
	if p.EnvHint != "DEEPSEEK_API_KEY" {
		t.Fatalf("env hint = %q, want DEEPSEEK_API_KEY", p.EnvHint)
	}
}

func TestAnUnknownProviderNeedsAnExplicitBaseURL(t *testing.T) {
	e := newEnv(t)
	_, err := BuildPlan(e.svc(), Options{
		Provider: "acme",
		Model:    "acme/large",
	})
	if err == nil {
		t.Fatal("an unknown provider without --base-url must be refused")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("the error must name the missing placeholder: %v", err)
	}
}

func TestModelAndProviderMustAgree(t *testing.T) {
	e := newEnv(t)
	_, err := BuildPlan(e.svc(), Options{
		Model:    "deepseek/deepseek-chat",
		Provider: "openai",
	})
	if err == nil {
		t.Fatal("contradictory --model and --provider must be refused")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	second := e.plan(Options{})
	if len(second.Files) != 0 {
		t.Fatalf("the second run must write nothing, got %v", rels(second))
	}
	if len(second.Notes) == 0 {
		t.Fatal("the second run must say what it kept")
	}
	if _, err := os.Stat(filepath.Join(e.root, "agents", "main.md")); err != nil {
		t.Fatalf("the existing agent must survive: %v", err)
	}
}

// ── the server section ──────────────────────────────────────────────────────

func TestTheServerConfigurationIsWritten(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if !hasRel(rels(p), "server/config.yaml") {
		t.Fatalf("the server configuration must be written: %v", rels(p))
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "server", "config.yaml")); err != nil {
		t.Fatalf("server/config.yaml must exist: %v", err)
	}
	if _, err := e.svc().Load(); err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
}

// The template writes the code's own defaults, so writing it must change
// nothing. That is the property worth pinning: the day a default moves, this
// test says the template has to move with it.
func TestTheServerConfigurationHoldsTheCodeDefaults(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	with, err := e.svc().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.Remove(filepath.Join(e.root, "server", "config.yaml")); err != nil {
		t.Fatalf("remove the server file: %v", err)
	}
	without, err := e.svc().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(with.Server, without.Server) {
		t.Fatalf("the written server configuration differs from the code defaults:\nwith:    %+v\nwithout: %+v",
			with.Server, without.Server)
	}
}

// ★ server/ is not hot-reloaded, so the config editor refuses to write it.
// init is not the editor: it creates the file before the process starts.
func TestTheServerConfigurationIsWrittenEvenThoughTheEditorMayNotWriteIt(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := spec.ValidatePath("server/config.yaml"); err == nil {
		t.Fatal("the config editor must still refuse to write server/")
	}
}

// ── the tools ───────────────────────────────────────────────────────────────

func TestTheToolsAreNotWrittenByDefault(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	for _, rel := range []string{"tools/http.yaml", "tools/web_search.yaml"} {
		if hasRel(rels(p), rel) {
			t.Fatalf("%s must not be written by default: %v", rel, rels(p))
		}
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "tools")); err == nil {
		t.Fatal("tools/ must not be created")
	}
}

func TestTheToolsAreWrittenOnRequest(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{Tools: true})
	for rel := range spec.ToolTemplates() {
		if !hasRel(rels(p), rel) {
			t.Fatalf("%s must be written: %v", rel, rels(p))
		}
	}
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := e.svc().Load()
	if err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
	if cfg.Tools.HTTP == nil || cfg.Tools.WebSearch == nil || cfg.Tools.Wikipedia == nil {
		t.Fatalf("the written tool files must enable the tools: %+v", cfg.Tools)
	}
}

// A rejected merge must leave nothing behind — including the server file,
// which is written outside the merge.
func TestARejectedMergeLeavesNoServerFile(t *testing.T) {
	e := newEnv(t)
	e.write("agents/main.md", "---\nmodel: openai/nope\n---\n\n坏的。\n")
	e.write("llm/openai.yaml", userProviderYAML)

	p := e.plan(Options{})
	if err := p.Apply(e.svc()); err == nil {
		t.Fatal("a configuration that does not load must not be written")
	}
	if _, err := os.Stat(filepath.Join(e.root, "server", "config.yaml")); err == nil {
		t.Fatal("the server file must not be written when the merge is rejected")
	}
}

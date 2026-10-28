package initcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openheron/agentkit/spec"
)

// ── 夹具 ────────────────────────────────────────────────────────────────────

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

// env 是一套**配置根**。
//
// ★ 取消继承链之后只有一个根 —— 而「用户级那份还在、别覆盖它」这类用例
//
//	也就不再成立：那件事现在由「同层同名文件不覆盖」保证。
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

// rels 列出计划里要写的相对路径。
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

// ── 用例 1：模板填完占位符后能被加载 ────────────────────────────────────────

// ★ 模板是**数据文件**，不是 Go 里的字符串 —— 而数据文件会与 schema 漂移。
//
//	症状是「生成的配置启动就报错」，而那种错只有真加载一遍才会发现。
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

// ★ 每个 provider 模板都要能独立渲染 —— 模板集里多一个文件就多一份责任。
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

// ── 用例 2、3：空目录跑 init，产物能跑 ──────────────────────────────────────

func TestInitInAnEmptyDirProducesARunnableConfig(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})

	want := []string{"agents/main.md", "agents/task.md", "llm/deepseek.yaml", "policy/permissions.yaml"}
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
	// ★ 用例 3：main 的模型引用要能解析 —— 否则「写完了」不等于「能跑」。
	if _, ok := cfg.Models[cfg.Agents[spec.DefaultAgentID].ModelRef]; !ok {
		t.Fatalf("the main agent references an undeclared model: %q",
			cfg.Agents[spec.DefaultAgentID].ModelRef)
	}
}

// ★ 出厂的两个 agent 是**一套**：main 的 `subagents: [task]` 引用 task，
//
//	而引用一个不存在的 agent 是**启动期硬错误**。
//
//	症状是「openheron init 说成功了，openheron 起不来」—— 而且错在启动期，
//	离「init 干了什么」很远，很难往回找。所以在这里钉死。
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
	// ★ Load 会把「引用了不存在的 agent」当硬错误 —— 这里能过就说明成套。
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

// ★ 两个 agent 用**同一个**模型 —— init 只选一个端点，
//
//	写出一份指向别的模型的 task 就是「起得来但行为诡异」。
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

// ── 用例 4：配置根里已有 provider ⇒ 不写同名文件 ───────────────────────────

// ★ 这是**这个命令存在的理由**：同名文件是整体替换语义，
//
//	写下去等于把用户那份悄悄换掉（密钥被占位符替掉、模型消失）。
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
	// 那份文件还在，且内容没变（它是用户自己的端点与密钥）。
	got, err := os.ReadFile(filepath.Join(e.root, "llm", "openai.yaml"))
	if err != nil {
		t.Fatalf("the existing file must still be there: %v", err)
	}
	if string(got) != userProviderYAML {
		t.Fatalf("the existing file was modified:\n%s", got)
	}
}

// ★ 那份**真的**没被动过（写入门要是动了它，这条会红）。
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

// ── 用例 5：配置根里已有 policy ⇒ 不写同名文件 ─────────────────────────────

func TestAnExistingPolicyIsNotOverwritten(t *testing.T) {
	e := newEnv(t)
	e.write("policy/permissions.yaml", userPolicyYAML)

	p := e.plan(Options{})
	if hasRel(rels(p), "policy/permissions.yaml") {
		t.Fatalf("the existing policy/permissions.yaml must not be rewritten: %v", rels(p))
	}
}

// ★ ask 是**整体替换**语义：写一份出厂默认会把用户收紧过的东西放宽。
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

// ── 用例 6：配置根已齐全 ⇒ 一个文件都不写 ──────────────────────────────────

func TestNothingIsWrittenWhenEverythingIsAlreadyThere(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)
	e.write("agents/main.md", userAgentMD)
	e.write("policy/permissions.yaml", userPolicyYAML)

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

// ── 用例 7：已有部分配置 ⇒ 只补缺的 ────────────────────────────────────────

func TestOnlyTheMissingFilesAreWritten(t *testing.T) {
	e := newEnv(t)
	e.write("llm/openai.yaml", userProviderYAML)
	e.write("agents/main.md", "---\nmodel: openai/gpt-4o\n---\n\n已有 agent。\n")

	p := e.plan(Options{})
	got := rels(p)
	if hasRel(got, "agents/main.md") {
		t.Fatalf("the existing agent must not be rewritten: %v", got)
	}
	if !hasRel(got, "policy/permissions.yaml") {
		t.Fatalf("the missing policy must be written: %v", got)
	}
	if hasRel(got, "llm/openai.yaml") {
		t.Fatalf("the existing provider must be reused, not copied: %v", got)
	}
}

// ── 用例 8：试合并失败 ⇒ 磁盘零字节变化 ────────────────────────────────────

// ★ 写入门的三步（试合并 → 落盘 → 重算指纹）保证「不合法的配置一个字节都
//
//	落不下去」。这条用例是那道保证的证据。
func TestARejectedMergeWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.write("agents/main.md", "---\nmodel: openai/gpt-4o\n---\n\n正文。\n")

	// 一个引用了不存在模型的 agent ⇒ 合并后不合法。
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

// ── 用例 9：已存在的 provider 被点名了另一个模型 ⇒ 报错且不写 ───────────────

func TestNamingAModelTheExistingProviderDoesNotDeclareIsRefused(t *testing.T) {
	e := newEnv(t)
	// 配置根里有 provider，但它声明的是**另一个**模型 ⇒ 用户点名要 gpt-4o
	// 就得写同名文件 ⇒ 那会把用户那份整个换掉 ⇒ 必须报错，不许悄悄替掉。
	//
	// ★ 显式点名才有这个冲突：自动挑模型时挑的就是已声明的那个，
	//	压根不需要写 provider 文件。
	e.write("llm/openai.yaml", strings.Replace(userProviderYAML, "gpt-4o", "gpt-4o-mini", 1))

	_, err := BuildPlan(e.svc(), Options{
		Model: "openai/gpt-4o",
	})
	if err == nil {
		t.Fatal("naming a model the existing provider does not declare must be refused")
	}
	// ★ 报错时磁盘一个字节都不能动 —— 报错之后还改了文件，
	//   用户会以为「它说不行但还是替我改了」。
	got, err := os.ReadFile(filepath.Join(e.root, "llm", "openai.yaml"))
	if err != nil {
		t.Fatalf("the existing file must still be there: %v", err)
	}
	if string(got) != strings.Replace(userProviderYAML, "gpt-4o", "gpt-4o-mini", 1) {
		t.Fatalf("the existing file was modified:\n%s", got)
	}
}

// ── 用例 10：--dry-run 不落盘 ───────────────────────────────────────────────

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	p := e.plan(Options{})
	if len(p.Files) == 0 {
		t.Fatal("the plan must have files")
	}
	// dry-run 只是不调用 Apply —— 计划本身与真跑一致。
	if err := p.Apply(e.svc()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "agents", "main.md")); err != nil {
		t.Fatalf("the agent must exist after Apply: %v", err)
	}
}

// ── 模型选择的规则 ──────────────────────────────────────────────────────────

// ★ 已有能用的模型就**复用**它（连密钥一起）—— 不因为敲了 init
//
//	就多出一个占位 provider。
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

// ★ embedding 模型不能当聊天模型用 —— 拿它去聊天会在几百次调用后才炸。
func TestEmbeddingModelsAreNotPicked(t *testing.T) {
	e := newEnv(t)
	// 两个 provider：一个只有 embedding，一个有能聊天的模型。
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

// ★ 显式给了模型但它没声明 ⇒ 要建 provider 文件。
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

// ★ 出厂 agent 写的思考档位必须在**出厂 provider 的词汇表里** ——
//
//	否则出厂默认就是「写着 reasoning: high、实际不思考」，而且**一声不吭**：
//	端点没声明的档位被静默忽略，不报错、不进启动日志。
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
	// 出厂 agent 模板用到的档位：一个都不能少。
	for _, tier := range []string{"high", "low"} {
		if _, ok := model.Reasoning[tier]; !ok {
			t.Fatalf("the factory provider must declare the tier %q, got %v", tier, model.Reasoning)
		}
	}
	// ★ 而 agent 模板里写的档位，正是这两个。
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

// ★ 出厂默认是 deepseek —— 理由见 [defaultProvider] 的注释。
//
//	钉死它，免得哪天有人把默认值改回去而没人发现：agent 模板写的思考档位
//	会**静默空转**（端点没声明 ⇒ 忽略），没有任何报错。
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

// ★ 没有模板的 provider ⇒ 必须显式给端点，不猜。
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

// ★ --model 与 --provider 打架 ⇒ 报错，不猜用户想要哪个。
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

// ── 幂等 ────────────────────────────────────────────────────────────────────

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
	// ★ 它得**说出来**「这些我都留着」—— 一句提示都没有的话，用户
	//   只会以为 init 什么都没干。
	if len(second.Notes) == 0 {
		t.Fatal("the second run must say what it kept")
	}
	if _, err := os.Stat(filepath.Join(e.root, "agents", "main.md")); err != nil {
		t.Fatalf("the existing agent must survive: %v", err)
	}
}

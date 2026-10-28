package spec

import (
	"openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fixture 造一套**配置根**。
//
// ★ 只有一个根：取消继承链之后，「用户级 + 项目级」这套 fixture 没有了 ——
//
//	而那正是下面两条跨级用例存在的原因。
type fixture struct {
	root string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{root: filepath.Join(root, ".openheron")}
	for _, sub := range []string{DirLLM, DirAgent, DirServer, DirPolicy, DirTools} {
		if err := os.MkdirAll(filepath.Join(f.root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_LLM_KEY", "sk-test")
	return f
}

func (f *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) load(t *testing.T) (*Config, error) {
	t.Helper()
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	return s.Load()
}

const minimalLLM = `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
  - id: strong
`

const minimalAgent = `
---
model: openai/fast
---
你是助手。
`

func (f *fixture) seedMinimal(t *testing.T) {
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/main.md", minimalAgent)
}

// ── 缺配置不是错误 ───────────────────────────────────────────────────────

func TestMissingConfigDirectoryIsNotAnError(t *testing.T) {
	root := t.TempDir()
	s, err := NewService(filepath.Join(root, "nope"), root)
	if err != nil {
		t.Fatal(err)
	}
	// 默认 agent 缺失才是错误（见 TestDefaultAgentIsRequired）。
	if _, err := s.Load(); err == nil {
		t.Fatal("缺 agents/main.md 时应报错")
	} else if !strings.Contains(err.Error(), "main.md") {
		t.Fatalf("the error must name the missing file, got %q", err)
	}
}

// ── 默认值 ───────────────────────────────────────────────────────────────

func TestDefaultsSurviveWhenFileOmitsKeys(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// 只写 listen_addr，其余应保留默认值 —— 这是「预填充结构体逐层 decode」的
	// 直接体现：不预填就会变成零值。
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ListenAddr != ":9999" {
		t.Fatalf("listen_addr was not overridden: %q", cfg.Server.ListenAddr)
	}
	if cfg.Server.Retry.MaxRetries != 5 {
		t.Fatalf("an undeclared max_retries must stay at the default 5, got %d", cfg.Server.Retry.MaxRetries)
	}
	if cfg.Server.Watch.Debounce.Duration().Milliseconds() != 200 {
		t.Fatalf("an undeclared debounce must stay at the default 200ms, got %v", cfg.Server.Watch.Debounce)
	}
}

// ── 单根：同名文件只有一份 ──────────────────────────────────────────────

// ★ 取消继承链之后，「同名文件整体替换」这条规则**不再有对象** ——
//
//	同一层里一个文件名只有一个文件，替换无从谈起。
//	这条用例守着的是「那份唯一的文件说了算」：文件里写什么，配置就是什么。
func TestTheOnlyFileInTheRootIsTheOneThatCounts(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/only\n---\n")
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://only.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: only
`)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.Models); got != 1 {
		t.Fatalf("a same-named file must replace wholesale, got %d models: %v", got, keysOf(cfg.Models))
	}
	if _, ok := cfg.Models["openai/only"]; !ok {
		t.Fatalf("the declared models did not take effect: %v", keysOf(cfg.Models))
	}
	if cfg.Providers["openai"].BaseURL != "https://only.example.com" {
		t.Fatalf("the base_url did not take effect: %q", cfg.Providers["openai"].BaseURL)
	}
}

// ── deny 与 ask（一份 permissions.yaml 说了算）────────────────────────────

func TestDenyAndAskComeFromTheSameFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "deny: [execute]\nask: [write_file]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Policy.TierOf("execute"); got != decl.TierDeny {
		t.Fatalf("execute must be denied, got %q", got)
	}
	if got := cfg.Policy.TierOf("write_file"); got != decl.TierAsk {
		t.Fatalf("write_file must be in the ask tier, got %q", got)
	}
}

func TestUnlistedToolDefaultsToAllow(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// 出厂策略把有后果的点名进 ask ⇒ 只读工具落到兜底（放行）。
	p := defaultPolicy()
	for _, tool := range []string{"ls", "read_file", "glob", "grep", "write_todos", "task"} {
		if got := p.TierOf(tool); got != decl.TierAllow {
			t.Errorf("the built-in read-only tool %s must default to allow, got %q", tool, got)
		}
	}
	for _, tool := range []string{"write_file", "edit_file", "execute"} {
		if got := p.TierOf(tool); got != decl.TierAsk {
			t.Errorf("the mutating tool %s must default to ask, got %q", tool, got)
		}
	}
	if got := p.TierOf("brand_new_tool"); got != decl.TierAllow {
		t.Fatalf("* an unlisted tool must fall back to allow, got %q", got)
	}
}

// ── 取值展开 ─────────────────────────────────────────────────────────────

// 环境变量未设 ⇒ **不报错**，而是 resolved=false（提醒），由调用方记 deferred。
func TestExpandEnvUnsetIsNonFatal(t *testing.T) {
	os.Unsetenv("OPENHERON_TEST_ABSENT")
	got, resolved, err := expandValue("${OPENHERON_TEST_ABSENT}")
	if err != nil {
		t.Fatalf("an unset env var must not be a load error (it only fails at request time): %v", err)
	}
	if resolved || got != "" {
		t.Fatalf("unset must yield an empty value with resolved=false, got %q / %v", got, resolved)
	}
}

func TestExpandEnvSetResolves(t *testing.T) {
	t.Setenv("OPENHERON_TEST_PRESENT", "  sk-ok  ")
	got, resolved, err := expandValue("${OPENHERON_TEST_PRESENT}")
	if err != nil || !resolved || got != "sk-ok" {
		t.Fatalf("set must yield the trimmed value with resolved=true: %q / %v / %v", got, resolved, err)
	}
}

func TestExpandAtFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "key")
	if err := os.WriteFile(p, []byte("  sk-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, resolved, err := expandValue("@file:" + p)
	if err != nil || !resolved {
		t.Fatal(err)
	}
	if got != "sk-from-file" {
		t.Fatalf("must be read and trimmed, got %q", got)
	}
	// ★ 文件缺失仍是硬错误：那是路径写错，不是「暂时没注入」。
	if _, _, err := expandValue("@file:" + filepath.Join(dir, "missing")); err == nil {
		t.Fatal("密钥文件不存在应报错（与缺 env 不同）")
	}
}

func TestExpandLiteralPassesThrough(t *testing.T) {
	got, resolved, err := expandValue("  sk-literal  ")
	if err != nil || !resolved || got != "sk-literal" {
		t.Fatalf("a literal must pass through trimmed, got %q / %v / %v", got, resolved, err)
	}
}

// ── decl.Duration ─────────────────────────────────────────────────────────────

func TestDurationRequiresUnit(t *testing.T) {
	var d decl.Duration
	if err := d.UnmarshalYAML(&yamlNode{Kind: yaml.ScalarNode, Value: "60", Tag: "!!int"}); err == nil {
		t.Fatal("★ 缺单位的时长必须报错（否则 60 会被当成 60 纳秒）")
	}
	if err := d.UnmarshalYAML(&yamlNode{Kind: yaml.ScalarNode, Value: "60s", Tag: "!!str"}); err != nil {
		t.Fatal(err)
	}
	if d.Duration().Seconds() != 60 {
		t.Fatalf("60s did not parse: %v", d)
	}
}

func TestTimeoutMissingUnitIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: ${TEST_LLM_KEY}\ntimeout: 300\nmodels: [{id: fast}]\n")
	if _, err := f.load(t); err == nil {
		t.Fatal("timeout 缺单位应在加载期报错")
	}
}

// ── 严格模式 ─────────────────────────────────────────────────────────────

func TestUnknownFieldIsRejectedWithLineNumber(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: ${TEST_LLM_KEY}\nmax_retry: 3\nmodels: [{id: fast}]\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("未知字段必须报错")
	}
	if !strings.Contains(err.Error(), "max_retry") {
		t.Fatalf("the error must name the unknown field: %v", err)
	}
}

// provider 的 api_key 引用未设置的环境变量 ⇒ **不阻塞启动**，只记一条提醒；
// 真正失败点移到调用该端点时（空 key → 401 → auth 错误）。
func TestProviderMissingKeyIsReminderNotFatal(t *testing.T) {
	f := newFixture(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\n---\n")
	f.write(t, "llm/openai.yaml",
		"type: openai_compat\nbase_url: https://x\napi_key: ${OPENHERON_TEST_NOPE}\nmodels: [{id: fast}]\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a missing api_key must not block startup: %v", err)
	}
	if cfg.Providers["openai"].APIKey != "" {
		t.Fatalf("an unresolved api_key must be empty, got %q", cfg.Providers["openai"].APIKey)
	}
	var found bool
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "OPENHERON_TEST_NOPE") && strings.Contains(d, "401 Unauthorized") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a notice naming the variable and its consequence must be recorded, got %v", cfg.Deferred())
	}
}

// ── 引用解析与校验 ───────────────────────────────────────────────────────

func TestAgentWithoutModelIsRejected(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/main.md", "---\ndescription: 主\n---\n正文\n")
	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("* a missing model must be rejected (a sub-agent silently inheriting the root model is a cost incident): %v", err)
	}
}

func TestModelRefWithoutProviderPrefixIsRejected(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", minimalLLM)
	f.write(t, "agents/main.md", "---\nmodel: fast\n---\n")
	if _, err := f.load(t); err == nil {
		t.Fatal("无 provider 前缀的引用应在启动期报错")
	}
}

func TestUnknownModelRefDoesNotFallBack(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/nonexistent\n---\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("★ 引用未声明的模型必须报错，不能静默退化到默认模型")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("the error must name the model: %v", err)
	}
}

func TestSubagentGraphCycleIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/a.md", "---\ndescription: A\nmodel: openai/fast\nsubagents: [b]\n---\n")
	f.write(t, "agents/b.md", "---\ndescription: B\nmodel: openai/fast\nsubagents: [a]\n---\n")
	// 引用链之外也要能发现环。
	f.write(t, "agents/main.md", "---\ndescription: 主\nmodel: openai/fast\nsubagents: [a]\n---\n")

	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a subagents cycle must be rejected: %v", err)
	}
}

func TestUnknownSubagentIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", "---\ndescription: 主\nmodel: openai/fast\nsubagents: [ghost]\n---\n")
	if _, err := f.load(t); err == nil {
		t.Fatal("引用不存在的 subagent 应报错")
	}
}

// ── frontmatter ──────────────────────────────────────────────────────────

func TestFrontmatterAndBodySplit(t *testing.T) {
	fm, body, err := splitFrontmatter([]byte("---\nmodel: a/b\ndescription: x\n---\n你是助手。\n\n第二段。\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fm), "model: a/b") {
		t.Fatalf("frontmatter was split wrong: %q", fm)
	}
	if !strings.Contains(body, "你是助手。") || !strings.Contains(body, "第二段。") {
		t.Fatalf("the body must be preserved verbatim (including paragraphs after a blank line): %q", body)
	}
}

func TestFrontmatterUnterminatedIsError(t *testing.T) {
	if _, _, err := splitFrontmatter([]byte("---\nmodel: a/b\n")); err == nil {
		t.Fatal("缺结束分隔符应报错")
	}
}

func TestAgentBodyBecomesInstruction(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents["main"].Instruction; !strings.Contains(got, "你是助手") {
		t.Fatalf("the body must become Instruction: %q", got)
	}
}

// ── deferred ─────────────────────────────────────────────────────────────

func TestDeferredIsCollected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// ★ 触发源是「密钥取不到」而不是 limits.yaml —— 后者在 2026-10-21
	//   之前是「认识但未实现」的例子，现在它**真的实现了**，不该再进 deferred。
	f.write(t, "llm/openai.yaml",
		strings.Replace(minimalLLM, "${TEST_LLM_KEY}", "${OPENHERON_TEST_NEVER_SET_KEY}", 1))

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Deferred()) == 0 {
		t.Fatal("★ 认识但未实现的键必须进 deferred（否则等于静默忽略）")
	}
}

// ── policy/limits.yaml ───────────────────────────────────────────────────

// limitsGlobal 抬高 server/config.yaml 的全局基线。
//
// ★ 限制**只能收紧**，所以要测「收紧生效」必须先把基线抬上去 ——
//
//	否则基线 1 已经是最严的值，limits 写什么都收紧不动。
func limitsGlobal(t *testing.T, f *fixture, depth, retries int) {
	t.Helper()
	f.write(t, "server/config.yaml",
		"agent:\n  max_depth: "+itoa(depth)+"\nretry:\n  max_retries: "+itoa(retries)+"\n")
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestLimitsTightenTheGlobalBaseline(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	limitsGlobal(t, f, 3, 4)
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 2\nretry:\n  max_retries: 1\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MaxDepth(); got != 2 {
		t.Fatalf("MaxDepth = %d, want 2 (limits.yaml tightens the global 3)", got)
	}
	if got := cfg.MaxRetries(); got != 1 {
		t.Fatalf("MaxRetries = %d, want 1 (limits.yaml tightens the global 4)", got)
	}
	// ★ 实现了的东西**不该**再进 deferred —— 那会让用户以为它没生效。
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "limits.yaml") {
			t.Fatalf("limits.yaml is implemented now, it must not be deferred: %q", d)
		}
	}
}

func TestLimitsTightenButNeverLoosen(t *testing.T) {
	// ★ 合并语义：取**更严的**。项目级只能收紧，不能放松。
	//   允许放松的话，「在项目里放一份 limits.yaml 写个大数」就是提权路径。
	f := newFixture(t)
	f.seedMinimal(t)
	limitsGlobal(t, f, 9, 9)
	// 用户级放宽（3 / 4），项目级收紧（1 / 2）⇒ 生效 1 / 2。
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 3\nretry:\n  max_retries: 4\n")
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 1\nretry:\n  max_retries: 2\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MaxDepth(); got != 1 {
		t.Fatalf("MaxDepth = %d, want 1 (the stricter of the two levels)", got)
	}
	if got := cfg.MaxRetries(); got != 2 {
		t.Fatalf("MaxRetries = %d, want 2 (the stricter of the two levels)", got)
	}
}

func TestLimitsCannotLoosenTheGlobalBaseline(t *testing.T) {
	// 全局基线在 server/config.yaml。limits.yaml 写更大的值**不能**把基线放松。
	f := newFixture(t)
	f.seedMinimal(t)
	limitsGlobal(t, f, 1, 5)
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 5\nretry:\n  max_retries: 99\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MaxDepth(); got != 1 {
		t.Fatalf("MaxDepth = %d, want 1 (the global baseline must not be loosened)", got)
	}
	if got := cfg.MaxRetries(); got != 5 {
		t.Fatalf("MaxRetries = %d, want 5 (the global baseline must not be loosened)", got)
	}
}

func TestLooserLimitIsReportedAsDeferred(t *testing.T) {
	// ★ 「配了不生效」必须**可见**：声明一个比全局基线更宽松的值是空操作，
	//   它必须进 deferred，启动时打印 —— 不能悄悄躺在那儿。
	f := newFixture(t)
	f.seedMinimal(t)
	limitsGlobal(t, f, 1, 5)
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 5\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "max_depth") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a looser-than-baseline limit must be reported in deferred, got %v", cfg.Deferred())
	}
}

func TestLimitsZeroMeansInherit(t *testing.T) {
	// 0 = 不设限，沿用全局值。**不是**「上限为 0」。
	f := newFixture(t)
	f.seedMinimal(t)
	limitsGlobal(t, f, 2, 7)
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 0\nretry:\n  max_retries: 0\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MaxDepth(); got != 2 {
		t.Fatalf("MaxDepth = %d, want 2 (0 = inherit the global)", got)
	}
	if got := cfg.MaxRetries(); got != 7 {
		t.Fatalf("MaxRetries = %d, want 7 (0 = inherit the global)", got)
	}
}

func TestLimitsRejectsUnknownKeys(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/limits.yaml", "agent:\n  max_tokens: 100\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("未知键必须报错（严格解析），而不是静默忽略")
	}
}

func TestLimitsDepthBelowTopologyFailsValidation(t *testing.T) {
	// ★ 限制收紧到与声明的拓扑冲突 ⇒ **启动期报错**，不静默截断。
	//   同一个道理在热更新时也成立：重载失败、旧配置继续服务。
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\nsubagents: [grandchild]\n---\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")
	limitsGlobal(t, f, 3, 5)
	// 收紧到 1，而 grandchild 在第 2 层 ⇒ 越界。
	f.write(t, "policy/limits.yaml", "agent:\n  max_depth: 1\n")

	if _, err := f.load(t); err == nil {
		t.Fatal("声明的深度超过收紧后的上限时必须报错")
	}
}

// ── 相对路径基准 ─────────────────────────────────────────────────────────

func TestRelativePathsResolveAgainstProjectDir(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	a := s.resolvePath("state")
	b := filepath.Join(f.root, "state")
	if a != b {
		t.Fatalf("a relative path must resolve against project_dir: %q != %q", a, b)
	}
	abs := s.resolvePath("/var/lib/openheron")
	if abs != "/var/lib/openheron" {
		t.Fatalf("an absolute path must be returned as-is: %q", abs)
	}
}

// ProjectRoot 是 .openheron 外面那层；ResolveDir 相对它解析（static_dir 用）。
func TestProjectRootAndResolveDir(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(f.root)
	if s.ProjectRoot() != root {
		t.Fatalf("ProjectRoot must be the parent of .openheron, want %q, got %q", root, s.ProjectRoot())
	}
	if s.Root() == s.ProjectRoot() {
		t.Fatal("Root（配置根）与 ProjectRoot（项目根）必须不同")
	}
	if got := s.ResolveDir("static"); got != filepath.Join(root, "static") {
		t.Fatalf("a relative path must resolve against the project root: %q != %q", got, filepath.Join(root, "static"))
	}
	if got := s.ResolveDir("/abs/static"); got != "/abs/static" {
		t.Fatalf("an absolute path must be returned as-is: %q", got)
	}
	if got := s.ResolveDir("  "); got != "" {
		t.Fatalf("an empty string must be returned as-is (callers use that to disable): %q", got)
	}
}

// data_dir 相对**配置目录**解析（与 static_dir 的项目根基准不同）。
//
// ★ 断言的正是「默认值不能悄悄搬家」：state 必须仍是 .openheron/state，
//
//	否则 .gitignore 与测试隔离（.openheron-test）同时失效。
func TestDataDirResolvesAgainstConfigDir(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	def, err := s.DataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if def != filepath.Join(f.root, DirState) {
		t.Fatalf("the default data dir must be <projectDir>/state, got %q", def)
	}
	if fi, err := os.Stat(def); err != nil || !fi.IsDir() {
		t.Fatalf("DataDir must create the directory: %v", err)
	}

	for _, rel := range []string{DirState, "var/data"} {
		got, err := s.DataDir(rel)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(f.root, rel); got != want {
			t.Fatalf("a relative data_dir must resolve against the config dir: %q != %q", got, want)
		}
	}

	abs := filepath.Join(t.TempDir(), "elsewhere")
	if got, err := s.DataDir(abs); err != nil || got != abs {
		t.Fatalf("an absolute data_dir must be returned as-is: %q (%v)", got, err)
	}
}

// ── 根探测 ───────────────────────────────────────────────────────────────

func TestRootProbingWalksUpwards(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	deep := filepath.Join(filepath.Dir(f.root), "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	// 从子目录出发，仍应命中那份配置。
	t.Setenv(ConfigDirEnv, "")
	s, err := NewService("", deep)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != f.root {
		t.Fatalf("walking up from a subdirectory must find the config root, got %q (want %q)", s.Root(), f.root)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("it must load after walking up: %v", err)
	}
}

func keysOf(m map[string]*decl.Model) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── 子 agent 拓扑与深度 ─────────────────────────────────────────────────

func TestSubagentGraphIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: 只读检索\nmodel: openai/fast\ndisabled_tools: [execute]\n---\n只读。\n")
	f.write(t, "agents/main.md",
		"---\nmodel: openai/strong\nsubagents: [researcher]\n---\n主。\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("a valid sub-agent declaration must not be rejected: %v", err)
	}
	r := cfg.Agents["researcher"]
	if r == nil || len(r.DisabledTools) != 1 {
		t.Fatalf("the sub-agent declaration was not loaded: %+v", r)
	}
}

// ★ 声明的深度超过上限 ⇒ **启动期报错**，而不是运行时静默截断。
//
// 「配了三层但实际只跑两层」是最难查的一类问题。
func TestDeclaredDepthOverLimitIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\nsubagents: [grandchild]\n---\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")
	// 默认 max_depth=1 ⇒ researcher(1) 已到顶。

	_, err := f.load(t)
	if err == nil {
		t.Fatal("★ 深度超限应在启动期报错")
	}
	if !strings.Contains(err.Error(), "max_depth") {
		t.Fatalf("the error must point at the tunable knob: %v", err)
	}
}

func TestRaisingMaxDepthAcceptsDeeperGraph(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\nsubagents: [grandchild]\n---\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")

	if _, err := f.load(t); err != nil {
		t.Fatalf("it must load after raising max_depth: %v", err)
	}
}

// 声明了却没人引用的 agent **合法** —— 它照样能当会话的 agent
// （装配按它自己的配置来），所以「没人引用」不意味着「用不上」。
func TestAgentNobodyReferencesIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/orphan.md", "---\ndescription: 孤儿\nmodel: openai/fast\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatalf("an agent nobody references must be accepted: %v", err)
	}
	if _, ok := cfg.Agents["orphan"]; !ok {
		t.Fatal("the orphan agent must be declared")
	}
}

func TestSubAgentSpecsMirrorTopology(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")
	f.write(t, "agents/researcher.md",
		"---\ndescription: R\nmodel: openai/fast\ndisabled_tools: [execute]\nreasoning: low\ntemperature: 0.3\nmax_tokens: 128\nsubagents: [grandchild]\n---\nR 正文\n")
	f.write(t, "agents/grandchild.md", "---\ndescription: G\nmodel: openai/fast\n---\nG 正文\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [researcher]\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	// ★ 规格按 agent 展开：main 的**下挂**是 specs[main].Sub。
	all, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	specs := all[cfg.DefaultAgent()].Sub
	if len(specs) != 1 || specs[0].ID != "researcher" {
		t.Fatalf("the top level of the spec tree is wrong: %+v", specs)
	}
	if len(specs[0].DisabledTools) != 1 || specs[0].DisabledTools[0] != "execute" {
		t.Fatalf("disabled_tools was not carried over: %+v", specs[0].DisabledTools)
	}
	if specs[0].Reasoning != "low" {
		t.Fatalf("the reasoning gear name was not carried over: %q", specs[0].Reasoning)
	}
	// ★ 采样参数必须与档位一起搬：漏搬不会编译失败，只会让
	//   「agents/*.md 写了 temperature 却不生效」重新出现。
	if specs[0].Temperature == nil || *specs[0].Temperature != 0.3 {
		t.Fatalf("temperature was not carried over: %v", specs[0].Temperature)
	}
	if specs[0].MaxTokens == nil || *specs[0].MaxTokens != 128 {
		t.Fatalf("max_tokens was not carried over: %v", specs[0].MaxTokens)
	}
	if len(specs[0].Sub) != 1 || specs[0].Sub[0].ID != "grandchild" {
		t.Fatalf("the nested spec is wrong: %+v", specs[0].Sub)
	}
}

// 每个**声明了的** agent 都能当会话的 agent —— 嵌套多深都行：
// 装配按它自己的配置来（它自己的下属、它自己的工具表）。
func TestEveryDeclaredAgentCanBeTheSessionAgent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "agent:\n  max_depth: 2\n")
	f.write(t, "agents/worker.md",
		"---\ndescription: W\nmodel: openai/fast\nsubagents: [leaf]\n---\n")
	f.write(t, "agents/leaf.md", "---\ndescription: L\nmodel: openai/fast\n---\n")
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\nsubagents: [worker]\n---\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.AgentSpecs()
	if err != nil {
		t.Fatal(err)
	}
	// 嵌套的 leaf 也有自己的一棵树 —— 它能当会话入口。
	for _, id := range []string{"main", "worker", "leaf"} {
		if _, ok := specs[id]; !ok {
			t.Fatalf("%q must have a spec of its own, got %v", id, cfg.AgentNames())
		}
	}
	// leaf 是叶子：它没有下属。
	if got := specs["leaf"].Sub; len(got) != 0 {
		t.Fatalf("leaf's tree = %+v, want none", got)
	}
}

// reasoning 词汇表与 reasoning_default 是模型侧的一对：前者给形状，后者给默认档。
func TestModelReasoningVocabularyAndDefault(t *testing.T) {
	f := newFixture(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\n---\n")
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
    reasoning:
      off:  {}
      low:  { reasoning_effort: low }
      high: { reasoning_effort: high }
    reasoning_default: low
`)
	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Models["openai/fast"]
	if got, ok := m.LevelFields("high"); !ok || got["reasoning_effort"] != "high" {
		t.Fatalf("the gear shape did not parse correctly: %#v ok=%v", got, ok)
	}
	if m.ReasoningDefault != "low" {
		t.Fatalf("reasoning_default did not parse: %q", m.ReasoningDefault)
	}
}

// 旧名 thinking / effort 必须被严格模式拒掉，不能再悄悄生效。
func TestLegacyThinkingAndEffortAreRejected(t *testing.T) {
	f := newFixture(t)
	f.write(t, "agents/main.md", "---\nmodel: openai/fast\n---\n")
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
    thinking:
      high: { reasoning_effort: high }
    effort: high
`)
	_, err := f.load(t)
	if err == nil || !strings.Contains(err.Error(), "thinking") {
		t.Fatalf("the legacy thinking field must be rejected in strict mode: %v", err)
	}
}

package spec

import (
	"openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件覆盖 [Service.loadWith] —— 配置写入路径的地基。
//
// ★ 每条用例都断言**磁盘没被动过**。试合并的全部价值就在于「验证时不碰盘」：
//
//	一旦它写了盘，「先验证再落盘」就退化成「写两次」，坏配置留在盘上，
//	只能手改恢复。

// serviceOf 造一个指向 fixture 的 Service。
func (f *fixture) service(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// read 读回磁盘上的真实内容。
func (f *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// exists 报告磁盘上是否真的有这个文件。
func (f *fixture) exists(t *testing.T, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(f.root, rel))
	return err == nil
}

// ── 等价性 ────────────────────────────────────────────────────────────────

// TestLoadWithNilEqualsLoad 是整个重构的护栏。
//
// loadWith 是 Load 的唯一实现，而它多了一段「叠加改动」的分支。nil 必须与
// 「改动之前」逐字等价 —— 任何不等价都是回归，而回归会表现为**所有**现有
// 配置行为的变化，而不是某一条用例的红。
func TestLoadWithNilEqualsLoad(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")
	f.write(t, "policy/permissions.yaml", "ask: [read_file, write_file]\n")

	s := f.service(t)

	plain, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	withNil, err := s.loadWith(nil)
	if err != nil {
		t.Fatal(err)
	}

	if withNil.Version != plain.Version {
		t.Fatalf("version = %q, want %q", withNil.Version, plain.Version)
	}
	if withNil.Server.ListenAddr != plain.Server.ListenAddr {
		t.Fatalf("listen_addr = %q, want %q", withNil.Server.ListenAddr, plain.Server.ListenAddr)
	}
	if strings.Join(withNil.Policy.Ask, ",") != strings.Join(plain.Policy.Ask, ",") {
		t.Fatalf("ask = %v, want %v", withNil.Policy.Ask, plain.Policy.Ask)
	}
	if len(withNil.Agents) != len(plain.Agents) {
		t.Fatalf("agents = %d, want %d", len(withNil.Agents), len(plain.Agents))
	}
}

// ── 替换已有文件 ──────────────────────────────────────────────────────────

func TestLoadWithReplacesExistingContent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "ask: [read_file]\n")

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel: "policy/permissions.yaml",
		// ★ ask 是**整体替换**，所以这里只写 deny 也会把 ask 清空 ——
		//   试合并必须如实反映这一点，否则界面显示的合并结果与真落盘后不符。
		Content: []byte("deny: [execute]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policy.Ask) != 0 {
		t.Fatalf("ask = %v, want empty (wholesale replace)", cfg.Policy.Ask)
	}
	if got := cfg.Policy.Deny; len(got) != 1 || got[0] != "execute" {
		t.Fatalf("deny = %v, want [execute]", got)
	}
	// ★ 磁盘必须原封不动。
	if got := f.read(t, "policy/permissions.yaml"); !strings.Contains(got, "read_file") {
		t.Fatalf("disk was modified: %q", got)
	}
}

// ── 新建磁盘上不存在的文件 ────────────────────────────────────────────────

func TestLoadWithAddsANewFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "llm/second.yaml",
		Content: []byte("type: openai\nbase_url: https://b.example.com\nmodels:\n  - id: x\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Models["second/x"]; !ok {
		t.Fatalf("second/x not declared; models = %v", keys(cfg.Models))
	}
	// ★ 磁盘上不能凭空多出一个文件。
	if f.exists(t, "llm/second.yaml") {
		t.Fatal("trial merge created a file on disk")
	}
}

// ── 删除 ──────────────────────────────────────────────────────────────────

// TestLoadWithDeleteRemovesTheFile 覆盖 DELETE 的试合并。
//
// ★ 这条最容易漏，而它的破坏性与坏写完全一样：删掉唯一声明某 provider 的
//
//	文件，而有 agent 引用它的模型 —— 合并后的 validate 当场失败。
func TestLoadWithDeleteCatchesDanglingModelRef(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t) // agents/main.md 引用 openai/fast

	s := f.service(t)
	_, err := s.loadWith(&mutation{Rel: "llm/openai.yaml", Delete: true})

	if err == nil {
		t.Fatal("deleting the only provider that main references must fail the trial merge")
	}
	if !strings.Contains(err.Error(), "openai/fast") {
		t.Fatalf("the error must name the dangling reference, got %q", err)
	}
	if f.exists(t, "llm/openai.yaml") != true {
		t.Fatal("a failed trial merge must not delete anything on disk")
	}
}

func TestLoadWithDeleteSucceedsWhenNothingDangles(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/unused.yaml", minimalLLM)

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{Rel: "llm/unused.yaml", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["unused"]; ok {
		t.Fatal("unused should be gone after the delete mutation")
	}
	if f.exists(t, "llm/unused.yaml") != true {
		t.Fatal("trial merge must not delete the file")
	}
}

// ── 跨文件语义 ────────────────────────────────────────────────────────────

// TestLoadWithRejectsUnresolvableModelRef 是这个机制存在的理由。
//
// 单文件解析完全合法 —— `model: openai/nope` 是一句语法正确的 yaml。
// 只有合并后的 validate 才拦得住它。
//
// ★ 断言里**不含行号**：跨文件引用错误拿不到行号（引用是解码后才有的，
// 偏移量没有跟着走下来）。行号只在单文件错误上可得 —— 见下一条。
// 文档 §7.7 的示例写的 `agents/researcher.md:3` 是理想形态，实现只到路径。
func TestLoadWithRejectsUnresolvableModelRef(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	_, err := s.loadWith(&mutation{
		Rel:     "agents/main.md",
		Content: []byte("---\nmodel: openai/nope\n---\nbody\n"),
	})
	if err == nil {
		t.Fatal("an unresolvable model ref must fail the trial merge")
	}
	if !strings.Contains(err.Error(), "agents/main.md") {
		t.Fatalf("the error must name the file to edit, got %q", err)
	}
	if !strings.Contains(err.Error(), "openai/nope") {
		t.Fatalf("the error must name the ref, got %q", err)
	}
}

func TestLoadWithRejectsUnknownYAMLFieldWithLineNumber(t *testing.T) {
	// ★ 行号是这套东西的 UX 核心：用户要知道改哪一行。
	//   yaml.v3 的 KnownFields 报错自带行号，而 loadDir 只包了一层相对路径，
	//   所以行号能透出来 —— 这条用例守住它。
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	_, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("allow: [read_file]\nmax_retry: 3\n"),
	})
	if err == nil {
		t.Fatal("an unknown field must be rejected")
	}
	if !strings.Contains(err.Error(), "max_retry") {
		t.Fatalf("the error must name the field, got %q", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("the error must carry a line number, got %q", err)
	}
	if !strings.Contains(err.Error(), "policy/permissions.yaml") {
		t.Fatalf("the error must name the file by its relative path, got %q", err)
	}
	if strings.Contains(err.Error(), f.root) || strings.Contains(err.Error(), f.root) {
		t.Fatalf("errors must not leak absolute paths: %q", err)
	}
}

// ── 分区与层级 ────────────────────────────────────────────────────────────

func TestTheCandidateOnlyAffectsItsOwnFile(t *testing.T) {
	// ★ 试合并的改动若串到别的文件，那条「合并之后仍成立」的保证就是假的 ——
	//   而写入门正是靠它来拦住「改一个文件却弄坏另一个」。
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "ask: [on_disk]\n")
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("ask: [candidate]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 候选只替换它自己那个文件。
	if len(cfg.Policy.Ask) != 1 || cfg.Policy.Ask[0] != "candidate" {
		t.Fatalf("ask = %v, want [candidate]", cfg.Policy.Ask)
	}
	// 别的文件仍是磁盘上的那份。
	if cfg.Server.ListenAddr != ":9999" {
		t.Fatalf("an unrelated file must not be affected: listen_addr = %q", cfg.Server.ListenAddr)
	}
}

func TestMutationOnServerCategoryIsSeenByTheMerge(t *testing.T) {
	// server/ 不热更新，但**要参与合并** —— 否则「改 max_depth 后子 agent
	// 声明超深」这类错误要到重启才暴露，而那时坏配置已经在盘上。
	f := newFixture(t)
	f.seedMinimal(t)

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "server/config.yaml",
		Content: []byte("agent:\n  max_depth: 2\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxDepth() != 2 {
		t.Fatalf("max_depth = %d, want 2", cfg.MaxDepth())
	}
}

// ── 目录不存在时仍能新建 ──────────────────────────────────────────────────

// TestLoadWithCreatesIntoAMissingDirectory 覆盖「新项目第一次建配置」。
//
// 此时磁盘上连 llm/ 都没有，而 loadDir 过去对 ErrNotExist 直接返回 ——
// 那条早退必须让位于「待落盘的新文件」。
func TestLoadWithCreatesIntoAMissingDirectory(t *testing.T) {
	root := t.TempDir()
	cfgRoot := filepath.Join(root, ".openheron")
	if err := os.MkdirAll(filepath.Join(cfgRoot, DirLLM), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfgRoot, DirAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: cfgRoot}
	f.seedMinimal(t)
	t.Setenv("TEST_LLM_KEY", "sk-test")

	// policy/ 目录还不存在。
	if _, err := os.Stat(filepath.Join(cfgRoot, DirPolicy)); err == nil {
		t.Fatal("precondition: policy/ must not exist yet")
	}

	s := f.service(t)
	cfg, err := s.loadWith(&mutation{
		Rel:     "policy/permissions.yaml",
		Content: []byte("ask: [execute]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policy.Ask) != 1 || cfg.Policy.Ask[0] != "execute" {
		t.Fatalf("ask = %v, want [execute]", cfg.Policy.Ask)
	}
	if _, err := os.Stat(filepath.Join(cfgRoot, DirPolicy)); err == nil {
		t.Fatal("trial merge created a directory on disk")
	}
}

// ── Categories 是唯一出处 ─────────────────────────────────────────────────

// TestCategoriesCoverExactlyWhatTheLoaderReads 钉住 Categories 与加载器的一致性。
//
// 两份清单必然漂：Categories 加了分区而 loaderFor 没接 ⇒ panic；
// loaderFor 接了而 Categories 没列 ⇒ 那个目录永远读不到。
// 这里比的是「Categories 里的每个分区都有一个 loader」，由 loaderFor 的
// default 分支 panic 兜住加载方向；本用例钉住反方向。
func TestCategoriesCoverExactlyWhatTheLoaderReads(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Categories {
		if seen[c.Dir] {
			t.Fatalf("duplicate category %q", c.Dir)
		}
		seen[c.Dir] = true

		cfg := &Config{
			Providers: map[string]*decl.Provider{},
			Models:    map[string]*decl.Model{},
			Agents:    map[string]*decl.Agent{},
			Server:    defaultServer(),
			Policy:    defaultPolicy(),
		}
		// 只验证它能返回 loader，不真的加载 —— 加载语义由各自的用例负责。
		if cfg.loaderFor(c) == nil {
			t.Fatalf("category %q has no loader", c.Dir)
		}
	}
	// skills 刻意不在其中（见 Categories 的说明）。
	if seen[DirSkills] {
		t.Fatal("skills must stay out of Categories: it is a directory tree, not flat entity files")
	}
}

func TestCategoryHotIsDerivedFromHotDirs(t *testing.T) {
	// ★ Hot 必须由 HotDirs 推得。写死一份映射表的话，某天把某个目录加进
	//   HotDirs 而忘了同步表，就会出现「保存成功但不生效」且无人察觉。
	for _, c := range Categories {
		inHotDirs := false
		for _, d := range HotDirs {
			if d == c.Dir {
				inHotDirs = true
			}
		}
		if c.Hot() != inHotDirs {
			t.Fatalf("category %q: Hot() = %v, HotDirs says %v", c.Dir, c.Hot(), inHotDirs)
		}
	}
	serverCat, _ := CategoryByDir(DirServer)
	if serverCat.Hot() {
		t.Fatal("server must not be hot: changing it requires a restart")
	}
}

func TestRootIsTheOnlyConfigDirectory(t *testing.T) {
	f := newFixture(t)
	s := f.service(t)

	if got := s.Root(); got != f.root {
		t.Fatalf("root = %q, want %q", got, f.root)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

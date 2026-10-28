package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── 路径校验 ──────────────────────────────────────────────────────────────

// writeErrKind 报 err 属于哪一类写入失败。
//
// ★ 这里自己判而不用包外导出的助手：调用方（api）走的是 `*config.WriteError`
//
//	类型断言，测试也该走同一条 —— 断言的形状要贴近真实用法。
func writeErrKind(err error) WriteErrorKind {
	var we *WriteError
	if !errors.As(err, &we) {
		return ""
	}
	return we.Kind
}

func TestValidatePathRejectsEscapes(t *testing.T) {
	// ★ sessionID 直落文件路径这件事本仓库已经吃过一次亏（architecture.md
	//   §5.6）。配置路径来自 HTTP 参数，威胁模型完全一样。
	bad := []string{
		"",
		"   ",
		"../secrets.yaml",
		"policy/../../etc/passwd",
		"policy/../llm/x.yaml",
		"/etc/passwd",
		"/policy/permissions.yaml",
		"state/anything.yaml",
		"nope/x.yaml",
		"skills/foo/SKILL.md",
	}
	for _, rel := range bad {
		if _, err := ValidatePath(rel); err == nil {
			t.Errorf("ValidatePath(%q) = nil error, want rejection", rel)
		} else if writeErrKind(err) != ErrPathInvalid {
			t.Errorf("ValidatePath(%q) kind = %v, want ErrPathInvalid", rel, err)
		}
	}
}

func TestValidatePathRejectsNonEditableSection(t *testing.T) {
	// server/ 不热更新 —— 写成「保存成功但不生效」比拒绝更糟。
	_, err := ValidatePath("server/config.yaml")
	if err == nil {
		t.Fatal("server/config.yaml must not be writable")
	}
	if writeErrKind(err) != ErrPathInvalid {
		t.Fatalf("kind = %v, want ErrPathInvalid", err)
	}
	if !strings.Contains(err.Error(), "restart") {
		t.Fatalf("the error must explain why, got %q", err)
	}
}

func TestValidatePathEnforcesPolicyFileNameWhitelist(t *testing.T) {
	// ★ 加载器对 policy/ 的未知文件名是**报错**的。写入门若不守同一条，
	//   用户会写进去一个「保存成功但下次加载即报错」的文件。
	if _, err := ValidatePath("policy/permissions.yaml"); err != nil {
		t.Fatalf("permissions.yaml must be valid: %v", err)
	}
	// ★ limits.yaml 现在是**真的**限制规则（不再是 deferred），所以写入门
	//   必须放行它 —— 否则用户能写但不能存，与「policy/ 里的文件都可编辑」
	//   这条前提矛盾。
	if _, err := ValidatePath("policy/limits.yaml"); err != nil {
		t.Fatalf("limits.yaml must be valid: %v", err)
	}
	if _, err := ValidatePath("policy/whatever.yaml"); err == nil {
		t.Fatal("an unknown policy file must be rejected")
	}
}

func TestValidatePathAcceptsTheEditableSections(t *testing.T) {
	good := []string{
		"policy/permissions.yaml",
		"llm/deepseek.yaml",
		"agents/main.md",
		"agents/my-agent.md",
	}
	for _, rel := range good {
		if _, err := ValidatePath(rel); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", rel, err)
		}
	}
}

func TestValidatePathRejectsDotfiles(t *testing.T) {
	// 隐藏文件在本仓库的两级目录里没有语义，而它们会被 loadDir 读到
	// （它只按扩展名过滤，不排除点文件）—— 那会让一个「看不见的文件」
	// 参与合并。
	for _, rel := range []string{"policy/.hidden.yaml", "llm/.env"} {
		if _, err := ValidatePath(rel); err == nil {
			t.Errorf("ValidatePath(%q) = nil error, want rejection", rel)
		}
	}
}

// ── revision ──────────────────────────────────────────────────────────────

func TestRevisionIsStableAndContentAddressed(t *testing.T) {
	a := Revision([]byte("ask: [ls]\n"))
	b := Revision([]byte("ask: [ls]\n"))
	c := Revision([]byte("ask: [ls, glob]\n"))

	if a == "" || len(a) != revisionLen {
		t.Fatalf("revision %q has the wrong length", a)
	}
	if a != b {
		t.Fatalf("same content gave different revisions: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("different content must give different revisions")
	}
}

// ── 明文密钥 ──────────────────────────────────────────────────────────────

func TestWriteRejectsLiteralSecret(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	_, _, err := s.Write("llm/openai.yaml",
		[]byte("type: openai\nbase_url: https://x.example\napi_key: sk-realsecret\nmodels:\n  - id: fast\n"), "")

	if err == nil {
		t.Fatal("a literal api_key must be rejected")
	}
	if writeErrKind(err) != ErrSecretLiteral {
		t.Fatalf("kind = %v, want ErrSecretLiteral", err)
	}
	// ★ 被拒的东西不许落盘 —— 否则「拒绝」只是界面上的说法。
	if !strings.Contains(f.read(t, "llm/openai.yaml"), "${TEST_LLM_KEY}") {
		t.Fatal("the original file was overwritten by a rejected write")
	}
}

func TestWriteAcceptsSecretReferences(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// @file: 要指向一个真存在的文件 —— 读不到它是**硬错误**（区别于 ${ENV}
	// 未设只提醒），所以这条得给一个真的文件。
	secretFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(secretFile, []byte("sk-from-file"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ label, key string }{
		{"env ref", "${TEST_LLM_KEY}"},
		// ★ `@` 是 YAML 保留指示符，不加引号连解析都过不去 ——
		//   所以带引号的写法才是这个引用形态的**正确**写法。
		{"at-file ref", `"@file:` + secretFile + `"`},
		{"no auth", ""},
	}
	for _, c := range cases {
		content := "type: openai\nbase_url: https://x.example\n" +
			"api_key: " + c.key + "\nmodels:\n  - id: fast\n"
		if _, _, err := s.Write("llm/openai.yaml", []byte(content), ""); err != nil {
			t.Errorf("%s (api_key: %s) was rejected: %v", c.label, c.key, err)
		}
	}
}

func TestCheckSecretOnlyGuardsTheTopLevelAPIKey(t *testing.T) {
	// ★ 禁得太宽会误伤正当内容，而误报会让人把整个校验关掉。
	//   这里要证明：别处出现 api_key 字样不受影响。
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	content := `# a comment mentioning api_key: sk-not-a-real-leak
type: openai
base_url: https://x.example
extra:
  api_key: this-is-a-vendor-field
  other: plain-value
models:
  - id: fast
`
	if _, _, err := s.Write("llm/openai.yaml", []byte(content), ""); err != nil {
		t.Fatalf("indented/commented api_key must not be treated as the credential: %v", err)
	}
}

// ── 写入 ──────────────────────────────────────────────────────────────────

func TestWriteLandsAndReportsPostWriteRevision(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	content := []byte("ask: [read_file, write_file]\n")
	rev, cfg, err := s.Write("policy/permissions.yaml", content, "")
	if err != nil {
		t.Fatal(err)
	}

	if rev != Revision(content) {
		t.Fatalf("rev = %q, want %q", rev, Revision(content))
	}
	if got := f.read(t, "policy/permissions.yaml"); got != string(content) {
		t.Fatalf("on disk = %q, want %q", got, content)
	}
	if len(cfg.Policy.Ask) != 2 || cfg.Policy.Ask[0] != "read_file" {
		t.Fatalf("the returned config must reflect the write: %v", cfg.Policy.Ask)
	}
}

func TestWriteLeavesNoTempFile(t *testing.T) {
	// 残留 .tmp 不会被加载器读到（它只收 .yaml/.md），但会让人以为
	// 「保存过一次」。
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if _, _, err := s.Write("policy/permissions.yaml", []byte("ask: [execute]\n"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "policy", "permissions.yaml.tmp")); err == nil {
		t.Fatal("a temp file was left behind")
	}
}

func TestWriteCreatesFirstConfigInAnEmptyProject(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// 配置根压根不存在 —— 新项目的第一次配置就是这个形状。
	// 删掉配置根本身 —— newFixture 会建好各分区的空目录，
	// 而「新项目的第一次配置」是连目录都没有的那个形状。
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.root); err == nil {
		t.Fatal("precondition: the config root must not exist yet")
	}
	// ★ 三个文件必须**一起**写：分开写必然试合并失败 —— 写 agent 时磁盘上
	//   还没有 provider，写 policy 时还没有 main agent。
	if _, _, err := s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(minimalLLM)},
		{Rel: "agents/main.md", Content: []byte("---\nmodel: openai/fast\n---\n你是助手。\n")},
		{Rel: "policy/permissions.yaml", Content: []byte("ask: [execute]\n")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, "policy/permissions.yaml"); !strings.Contains(got, "execute") {
		t.Fatalf("content = %q", got)
	}
	if !f.exists(t, "agents/main.md") {
		t.Fatal("the main agent must have been written too")
	}
}

// ── 乐观并发 ──────────────────────────────────────────────────────────────

func TestWriteRejectsStaleRevision(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "policy/permissions.yaml", "ask: [execute]\n")
	s := f.service(t)

	stale := Revision([]byte("ask: [execute]\n"))
	// 模拟「用户手改了磁盘」或「另一个窗口先保存了」。
	f.write(t, "policy/permissions.yaml", "ask: [execute, write_file]\n")

	_, _, err := s.Write("policy/permissions.yaml", []byte("ask: [ls]\n"), stale)
	if err == nil {
		t.Fatal("a stale baseRev must be rejected")
	}
	if writeErrKind(err) != ErrConflict {
		t.Fatalf("kind = %v, want ErrConflict", err)
	}
	// ★ 磁盘上必须还是别人那一版。
	if got := f.read(t, "policy/permissions.yaml"); !strings.Contains(got, "write_file") {
		t.Fatalf("a rejected write must not overwrite: %q", got)
	}
}

func TestWriteAcceptsCurrentRevision(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	original := "ask: [execute]\n"
	f.write(t, "policy/permissions.yaml", original)
	s := f.service(t)

	if _, _, err := s.Write("policy/permissions.yaml",
		[]byte("ask: [execute, write_file]\n"), Revision([]byte(original))); err != nil {
		t.Fatalf("a matching baseRev must be accepted: %v", err)
	}
}

func TestWriteOnMissingFileTreatsEmptyRevisionAsCreate(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// 空 rev = 文件不存在 = 新建。界面不该被迫先查一次「在不在」。
	if _, _, err := s.Write("policy/permissions.yaml", []byte("ask: [execute]\n"), ""); err != nil {
		t.Fatal(err)
	}
}

// ── 删除 ──────────────────────────────────────────────────────────────────

// TestDeleteRejectsDanglingReference 是 DELETE 试合并存在的理由。
func TestDeleteRejectsDanglingReference(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t) // agents/main.md 引用 openai/fast
	s := f.service(t)

	_, err := s.Delete("llm/openai.yaml")
	if err == nil {
		t.Fatal("deleting the only referenced provider must be rejected")
	}
	if writeErrKind(err) != ErrMergeRejected {
		t.Fatalf("kind = %v, want ErrMergeRejected", err)
	}
	if f.exists(t, "llm/openai.yaml") != true {
		t.Fatal("a rejected delete must not remove the file")
	}
}

func TestDeleteLands(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/unused.yaml", minimalLLM)
	s := f.service(t)

	cfg, err := s.Delete("llm/unused.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["unused"]; ok {
		t.Fatal("the returned config must reflect the delete")
	}
	if f.exists(t, "llm/unused.yaml") {
		t.Fatal("the file is still on disk")
	}
}

func TestDeleteMissingFileIsAnError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if _, err := s.Delete("llm/never-existed.yaml"); err == nil {
		t.Fatal("deleting a file that is not there must be an error")
	}
}

// ── 读取 ──────────────────────────────────────────────────────────────────

func TestReadFileReportsMissingAsEmptyNotError(t *testing.T) {
	// ★ 界面要能区分「这一级还没有这个文件」与「读不出来」。
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	content, rev, err := s.ReadFile("llm/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if content == nil || rev == "" {
		t.Fatalf("an existing file must return content and a revision (rev=%q)", rev)
	}

	content, rev, err = s.ReadFile("policy/permissions.yaml")
	if err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if content != nil || rev != "" {
		t.Fatalf("a missing file must return nil/empty, got %q/%q", content, rev)
	}
}

func TestReadFileRejectsBadPaths(t *testing.T) {
	f := newFixture(t)
	s := f.service(t)

	if _, _, err := s.ReadFile("../escape.yaml"); err == nil {
		t.Fatal("ReadFile must apply the same path validation as Write")
	}
	if _, _, err := s.ReadFile("server/config.yaml"); err == nil {
		t.Fatal("ReadFile must reject non-editable sections too, so the UI cannot show a read path that has no write path")
	}
}

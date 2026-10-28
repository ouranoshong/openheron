package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openheron/agentkit/spec"
)

// initEnv 造一个空环境，返回目标目录。
//
// ★ 必须把 HOME 指到临时目录 —— 否则测试会读到开发者自己家目录里的
//
//	~/.openheron（那里可能有真的 provider 与密钥），用例的结果就不确定了。
//	★ 也必须清掉 OPENHERON_CONFIG_DIR：它会**盖掉**目标目录，
//	用例就会写到别处去。
func initEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv(spec.ConfigDirEnv, "")
	return filepath.Join(root, "project")
}

// ── --json：机器可读输出 ────────────────────────────────────────────────────

func TestInitJSONOutputIsValidJSON(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "--json"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	var out struct {
		Dir     string   `json:"dir"`
		Model   string   `json:"model"`
		Created []string `json:"created"`
		Notes   []string `json:"notes"`
		Env     string   `json:"env"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("the output must be valid JSON: %v\n%s", err, buf.String())
	}
	if out.Dir == "" || out.Model == "" {
		t.Fatalf("dir and model must be reported: %+v", out)
	}
	// ★ 出厂是**两个** agent + provider + policy = 四个文件。
	//   agent 必须成套：main 的 `subagents: [task]` 引用 task，
	//   少一个就是启动期硬错误（initcfg 的用例 23 钉住这条）。
	if len(out.Created) != 4 {
		t.Fatalf("created = %v, want four files", out.Created)
	}
	for _, want := range []string{".openheron/agents/main.md", ".openheron/agents/task.md"} {
		found := false
		for _, c := range out.Created {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("created = %v, want it to contain %s", out.Created, want)
		}
	}
	if out.Env != "DEEPSEEK_API_KEY" {
		t.Fatalf("env = %q, want DEEPSEEK_API_KEY", out.Env)
	}
}

// ── --dry-run：只报告，不落盘 ──────────────────────────────────────────────

func TestInitDryRunWritesNothing(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "--dry-run"}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	if !strings.Contains(buf.String(), "dry run") {
		t.Fatalf("the output must say it was a dry run:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".openheron")); err == nil {
		t.Fatal("a dry run must not create anything")
	}
}

// ── 参数错误 ────────────────────────────────────────────────────────────────

func TestInitRejectsAnUnknownFlag(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--dir", dir, "--nope"}, &buf)
	if err == nil {
		t.Fatal("an unknown flag must be an error")
	}
}

func TestInitRejectsAMalformedModel(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	err := initCmd([]string{"--dir", dir, "--model", "gpt-4o"}, &buf)
	if err == nil {
		t.Fatal("a model reference without a provider must be an error")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("the error must say what is wrong: %v", err)
	}
}

func TestInitRejectsStrayArguments(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir, "extra"}, &buf); err == nil {
		t.Fatal("a stray argument must be an error")
	}
}

// ── --help ──────────────────────────────────────────────────────────────────

func TestInitHelpPrintsTheUsage(t *testing.T) {
	var buf bytes.Buffer
	if err := initCmd([]string{"--help"}, &buf); err != nil {
		t.Fatalf("--help must not be an error: %v", err)
	}
	if !strings.Contains(buf.String(), "openheron init") {
		t.Fatalf("the usage must name the command:\n%s", buf.String())
	}
}

// ── 产物落在指定目录 ────────────────────────────────────────────────────────

func TestInitWritesIntoTheGivenDirectory(t *testing.T) {
	dir := initEnv(t)
	var buf bytes.Buffer
	if err := initCmd([]string{"--dir", dir}, &buf); err != nil {
		t.Fatalf("initCmd: %v", err)
	}
	for _, rel := range []string{
		".openheron/agents/main.md",
		".openheron/agents/task.md",
		".openheron/llm/deepseek.yaml",
		".openheron/policy/permissions.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
	}
	// ★ 产物必须**自己就能加载** —— 写完了不等于能跑。
	//
	//	配置根就是那份产物所在的地方：取消继承链之后，没有第二层可读。
	svc, err := spec.NewServiceAt(filepath.Join(dir, ".openheron"), dir)
	if err != nil {
		t.Fatalf("NewServiceAt: %v", err)
	}
	if _, err := svc.Load(); err != nil {
		t.Fatalf("the produced config must load: %v", err)
	}
}

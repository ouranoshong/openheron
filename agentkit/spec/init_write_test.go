package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件覆盖两条为 `openheron init` 而加的入口：[NewServiceAt] 与 [Service.WriteMany]。
//
// ★ 两条都只服务一件事：**把「最小可运行配置」写进一个指定的目录**，
//
//	而那个目录不许被上溯探测改掉、写进去的那批文件必须是**一起**校验的。

// ── NewServiceAt ───────────────────────────────────────────────────────────

// ★ 上溯探测是**运行期**的规则：cd 到子目录时仍命中项目级配置。
//
//	写配置时不能这样 —— 落点必须由调用方指定。
func TestNewServiceAtDoesNotProbeUpwards(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor", ".openheron")
	if err := os.MkdirAll(ancestor, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "ancestor", "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	// 走探测的那条路：会找到祖先的 .openheron。
	t.Setenv(ConfigDirEnv, "")
	probed, err := NewService("", child)
	if err != nil {
		t.Fatal(err)
	}
	if probed.Root() != ancestor {
		t.Fatalf("probing must find the ancestor, got %q", probed.Root())
	}

	// 指定的那条路：不去祖先那儿。
	fixed, err := NewServiceAt(filepath.Join(child, ".openheron"), child)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Root() != filepath.Join(child, ".openheron") {
		t.Fatalf("root = %q, want the given one", fixed.Root())
	}
}

// ★ 显式给出的配置根**就是**配置根：里面直接有 llm/ agents/，不自动拼 .openheron。
func TestNewServiceAtTakesTheConfigRootAsIs(t *testing.T) {
	base := t.TempDir()
	s, err := NewServiceAt(base, base)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != base {
		t.Fatalf("root = %q, want the given one", s.Root())
	}
}

// ★ 配置根为空 ⇒ 加载时跳过它，而不是报错。
func TestAnAbsentConfigRootLoadsNothing(t *testing.T) {
	s, err := NewServiceAt("", filepath.Join(t.TempDir(), ".openheron"))
	if err != nil {
		t.Fatal(err)
	}
	// ★ 用 LoadUnchecked：空配置的 [Service.Load] 会因为「没有 main agent」
	//	而校验失败 —— 那是**校验**的结论，与「配置根不存在」无关。
	cfg, err := s.LoadUnchecked()
	if err != nil {
		t.Fatalf("LoadUnchecked: %v", err)
	}
	if len(cfg.Providers) != 0 || len(cfg.Agents) != 0 {
		t.Fatalf("nothing must be loaded from an absent config root: %+v", cfg)
	}
}

// ── WriteMany ──────────────────────────────────────────────────────────────

const batchProvider = `type: openai_compat
base_url: https://api.openai.com/v1
api_key: ${TEST_LLM_KEY}
models:
  - id: gpt-4o
`

const batchAgent = `---
description: 批量写的 agent
model: openai/gpt-4o
---

正文。
`

// ★ 批量写存在的理由：**互相依赖**的文件逐个写会永远过不了校验。
//
//	先写 agent 则 provider 还没声明，先写 provider 则 agent 还不存在。
func TestWriteManyWritesMutuallyDependentFilesAtOnce(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	rev, _, err := s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(batchProvider)},
		{Rel: "agents/main.md", Content: []byte(batchAgent)},
	}, "")
	if err != nil {
		t.Fatalf("WriteMany: %v", err)
	}
	if rev == "" {
		t.Fatal("the revision must be reported")
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("the written config must load: %v", err)
	}
	if _, ok := cfg.Agents["main"]; !ok {
		t.Fatal("the agent must be there")
	}
	if _, ok := cfg.Models["openai/gpt-4o"]; !ok {
		t.Fatal("the model must be there")
	}
}

// ★ 试合并不过 ⇒ **磁盘一个字节都不动** —— 这条是写入门全部的价值。
func TestWriteManyLeavesTheDiskAloneWhenTheMergeIsRejected(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.root)

	// agent 引用了一个没声明的模型 ⇒ 合并后不合法。
	_, _, err = s.WriteMany([]File{
		{Rel: "agents/main.md", Content: []byte("---\nmodel: openai/nope\n---\n\n坏的。\n")},
		{Rel: "llm/openai.yaml", Content: []byte(batchProvider)},
	}, "")
	if err == nil {
		t.Fatal("the batch must be rejected")
	}
	if got := snapshot(t, f.root); got != before {
		t.Fatalf("the disk must be untouched:\nbefore %s\nafter  %s", before, got)
	}
}

// ★ 明文密钥照旧被拒 —— 批量写没有绕开写入门。
func TestWriteManyRejectsALiteralSecret(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(
			"type: openai_compat\nbase_url: https://x\napi_key: sk-literal\nmodels:\n  - id: gpt-4o\n")},
	}, "")
	if err == nil {
		t.Fatal("a literal api_key must be rejected")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("the error must name the key: %v", err)
	}
}

// ★ 批量写里一个文件非法 ⇒ **整批**都不写（不是「写一半」）。
func TestWriteManyIsAllOrNothing(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.root)

	_, _, err = s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(batchProvider)},
		{Rel: "agents/main.md", Content: []byte("---\nmodel: openai/missing\n---\n\n坏的。\n")},
	}, "")
	if err == nil {
		t.Fatal("the batch must be rejected")
	}
	if got := snapshot(t, f.root); got != before {
		t.Fatalf("the disk must be untouched:\nbefore %s\nafter  %s", before, got)
	}
}

// ★ 空批次是**错误**，不是「成功地什么都没做」——
//
//	后者会让调用方以为配置已经就绪。
func TestWriteManyRejectsAnEmptyBatch(t *testing.T) {
	f := newFixture(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WriteMany(nil, ""); err == nil {
		t.Fatal("an empty batch must be an error")
	}
}

// snapshot 列出目录下的文件与内容（用来断言「磁盘没被动过」）。
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b.WriteString(rel + "\n" + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return b.String()
}

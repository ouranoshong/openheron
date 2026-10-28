package spec

import (
	"openheron/agentkit/spec/decl"
	"path/filepath"
	"strings"
	"testing"
)

// embeddingLLM 声明一个带 kind: embedding 的模型。
const embeddingLLM = `type: openai_compat
base_url: http://127.0.0.1:8089/v1
api_key: ""
models:
  - id: Qwen3-Embedding-0.6B-GGUF
    kind: embedding
    dimensions: 1024
`

func kbFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/local.yaml", embeddingLLM)
	return f
}

// load 走真实的 Service —— kb 的几条规则里有好几条依赖「项目根」这个概念
// （语料按项目根解析），绕开 Service 的测试验不到那部分。
func load(t *testing.T, f *fixture) (*Config, error) {
	t.Helper()
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s.Load()
}

func loadErr(t *testing.T, f *fixture) error {
	t.Helper()
	_, err := load(t, f)
	return err
}

func TestKBAbsentIsNotAnError(t *testing.T) {
	f := kbFixture(t)
	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("no tools/kb.yaml must not be an error: %v", err)
	}
	if cfg.KB.Corpus != "" {
		t.Errorf("corpus = %q, want empty (knowledge base off)", cfg.KB.Corpus)
	}
	// 缺省值要真的在：batch_size / top_k 是「配了就生效」的字段，缺省为零
	// 会让「配了」与「没配」行为一样。
	if cfg.KB.BatchSize != 10 || cfg.KB.TopK != 8 {
		t.Errorf("defaults = batch %d / topk %d, want 10 / 8", cfg.KB.BatchSize, cfg.KB.TopK)
	}
	// ★ `splitter.headers` **刻意没有缺省值**（v2 起）：它不再决定任何产物，
	//   而预填一个缺省会让 `kb status` 的「该配置项已不生效」提示对每一个用户
	//   都出现 —— 包括从没写过它的那些。留空才能让那句提示只对真正受影响的人
	//   出现，那也正是它存在的理由（§9.1）。
	if len(cfg.KB.Splitter.Headers) != 0 {
		t.Errorf("splitter.headers = %v, want empty (no default: the field is inert in v2)",
			cfg.KB.Splitter.Headers)
	}
}

// ★ 这一条是整个知识库设计的结构保证：语料必须跟着 projectDir 走。
func TestKBCorpusRejectsAbsolutePath(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: /usr/share/doc\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	err := loadErr(t, f)
	if err == nil {
		t.Fatal("an absolute corpus path was accepted")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Errorf("error should say why, got: %v", err)
	}
}

func TestKBModelMustBeEmbedding(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 把这个模型降级成 chat —— 校验必须在建索引**之前**拦住。
	cfg.Models["local/Qwen3-Embedding-0.6B-GGUF"].Kind = string(decl.ModelKindChat)
	if err := cfg.validateKB(); err == nil {
		t.Fatal("a chat model was accepted as the embedding model")
	} else if !strings.Contains(err.Error(), "kind: embedding") {
		t.Errorf("error should say how to fix it, got: %v", err)
	}
}

func TestKBModelMustExist(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: docs\nmodel: local/nope\n")

	err := loadErr(t, f)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("err = %v, want a complaint about the missing model", err)
	}
}

func TestKBModelRequiredWhenCorpusSet(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: docs\n")

	err := loadErr(t, f)
	if err == nil || !strings.Contains(err.Error(), "kb.model") {
		t.Fatalf("err = %v, want a complaint about the missing model", err)
	}
}

func TestKBCorpusAloneIsNotAnError(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: docs\n")

	err := loadErr(t, f)
	if err == nil || !strings.Contains(err.Error(), "kb.model") {
		t.Fatalf("err = %v, want a complaint about the missing model", err)
	}
}

// ★ `splitter.headers` 在 v2 里**什么都不决定**，所以它的内容也不该被校验。
//
//	一个被忽略的字段去校验内容，只会让「按文档说的把它清空」变成一次启动
//	失败 —— 而报出来的理由（「每个文件会变成一个块」）在 v2 里根本不成立。
//	存量配置里写着它，所以字段本身要留着；留着不等于要管它。
func TestKBEmptyHeadersIsAccepted(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml",
		"corpus: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\nsplitter:\n  headers: {}\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("清空一个不生效的字段不该让配置起不来（§9.1）：%v", err)
	}
	if len(cfg.KB.Splitter.Headers) != 0 {
		t.Errorf("headers = %v, want empty", cfg.KB.Splitter.Headers)
	}
}

// ★ 逐层整体替换，不合并。项目级只想改一个层级，合并会让两级悄悄并存 ——
//
//	而 symptoms 是「用户级配的 ### 不见了 / 还在」，两种都很难查。
func TestKBSplitterHeadersReplaceNotMerge(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml",
		"corpus: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\nsplitter:\n  headers: {\"##\": section}\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := cfg.KB.Splitter.Headers["###"]; ok {
		t.Errorf("headers = %v, want the project level to **replace** the default", cfg.KB.Splitter.Headers)
	}
	if got := cfg.KB.Splitter.Headers["##"]; got != "section" {
		t.Errorf("headers = %v, want ##: section", cfg.KB.Splitter.Headers)
	}
}

func TestModelDimensionsMustNotBeNegative(t *testing.T) {
	f := kbFixture(t)
	f.write(t, "tools/kb.yaml", "corpus: docs\nmodel: local/Qwen3-Embedding-0.6B-GGUF\n")

	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Models["local/Qwen3-Embedding-0.6B-GGUF"].Dimensions = -1
	if err := cfg.validateKB(); err == nil {
		t.Fatal("a negative dimension was accepted")
	}
}

// ★ 缺省成 chat 而不是必填：已入库的 llm/*.yaml 里一个 kind 都没有，
//
//	它们必须继续原样加载。
func TestModelKindDefaultsToChat(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	cfg, err := load(t, f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for ref, m := range cfg.Models {
		if m.Kind != "" && m.Kind != string(decl.ModelKindChat) {
			t.Errorf("%s kind = %q, want chat or empty", ref, m.Kind)
		}
		if m.IsEmbedding() {
			t.Errorf("%s must not be an embedding model by default", ref)
		}
	}
}

// ── 分区表 ───────────────────────────────────────────────────────────────

// ★ kb 必须进 Categories —— 那是**加载门与写入门唯一共用的分区真相**。
//
// 不进就既加载不了也写不了，而写不了意味着配置编辑器看不到它，而接口说
// 它不存在 —— 没有任何东西会响。
func TestKBCategoryIsRegistered(t *testing.T) {
	var found bool
	for _, c := range Categories {
		if c.Dir == DirTools {
			found = true
			if !c.Editable {
				t.Error("kb/ should be editable")
			}
			if !c.Hot() {
				t.Error("kb/ should hot-reload")
			}
			if c.Ext != ".yaml" {
				t.Errorf("kb ext = %q", c.Ext)
			}
		}
	}
	if !found {
		t.Fatalf("kb is not in Categories: %v", dirNames(Categories))
	}
}

func dirNames(cs []Category) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Dir
	}
	return out
}

package spec

import (
	"testing"

	"openheron/agentkit/spec/decl"
)

// ★ 分区模板里的键必须是声明里**认得的**键 —— 否则用户在编辑器里点
//
//	「新建」，得到的文件**存不下来**（加载器对未知字段是硬报错）。
//
//	那是最差的「新建」体验：文件建出来了，却要用户自己猜哪个键错了。
//
// ★ 这里用**加载器同一个严格解码器**来验，而不是维护一份键名清单：
//
//	清单会漂移（加字段时忘了改清单），而解码器不会 ——
//	它就是生产路径本身。
func TestCategoryTemplatesDecodeStrictly(t *testing.T) {
	t.Run("policy", func(t *testing.T) {
		var p decl.Policy
		if err := decodeStrict([]byte(Template(DirPolicy, nil)), &p); err != nil {
			t.Fatalf("the policy template must decode with the loader's own decoder: %v", err)
		}
	})

	t.Run("llm", func(t *testing.T) {
		var pr decl.Provider
		if err := decodeStrict([]byte(Template(DirLLM, nil)), &pr); err != nil {
			t.Fatalf("the llm template must decode with the loader's own decoder: %v", err)
		}
	})

	t.Run("agent", func(t *testing.T) {
		// agent 的模板是 frontmatter + 正文，与 agents/<id>.md 同一形状。
		fm, _, err := splitFrontmatter([]byte(Template(DirAgent, nil)))
		if err != nil {
			t.Fatalf("the agent template must have a frontmatter: %v", err)
		}
		var a decl.Agent
		if err := decodeStrict(fm, &a); err != nil {
			t.Fatalf("the agent template must decode with the loader's own decoder: %v", err)
		}
	})
}

// 声明里的键与**读**的路径必须一致：加载器读的就是这些 yaml tag。
//
// ★ 这条是上一条的另一半 —— 模板对上了还不够，声明本身也得能被
//
//	「同一份」加载器读懂（否则写进去的文件读不出来）。
func TestDeclarationsDecodeWithTheirOwnTags(t *testing.T) {
	// 每个声明类型都能用它自己的 yaml tag 往返一次。
	var (
		provider decl.Provider
		model    decl.Model
		agent    decl.Agent
		policy   decl.Policy
		limits   decl.Limits
		server   decl.Server
		kb       decl.KB
	)
	for _, tc := range []struct {
		name string
		v    any
	}{
		{"provider", &provider},
		{"model", &model},
		{"agent", &agent},
		{"policy", &policy},
		{"limits", &limits},
		{"server", &server},
		{"kb", &kb},
	} {
		if err := decodeStrict([]byte("{}\n"), tc.v); err != nil {
			t.Errorf("%s must decode from an empty document: %v", tc.name, err)
		}
	}
}

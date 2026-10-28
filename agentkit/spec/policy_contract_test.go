package spec

import (
	"testing"

	"openheron/agentkit/spec/decl"
	"openheron/kb"
)

// 工具名分散在两个包里：kb 定义它们（工具自己的名字），config 的默认档位表
// 引用它们。两个包互不相识（config 是叶子，kb 也是），所以没法共用常量。
//
// ★ 那就在这里把它们焊住。漂移的后果不是编译错误，而是**kb_index 落回兜底
// 的 allow** —— 写索引不再要人点头，症状看起来像「索引悄悄不审批了」。
// 那种症状很难追到这里。
func TestDefaultPolicyCoversKBTools(t *testing.T) {
	p := defaultPolicy()

	if got := p.TierOf(kb.ToolSearch); got != decl.TierAllow {
		t.Errorf("effective tier of %s = %q, want %q — a read-only tool behind approval is one "+
			"the model learns not to use", kb.ToolSearch, got, decl.TierAllow)
	}
	if got := p.TierOf(kb.ToolIndex); got != decl.TierAsk {
		t.Errorf("effective tier of %s = %q, want %q", kb.ToolIndex, got, decl.TierAsk)
	}
}

// ★ 未点名的工具一律**放行** —— 兜底是 allow。这条守住的是「读文件、
// 列目录这类只读工具不该每次都要人点头」。
//
// ★ 有后果的工具（写文件、执行命令）靠**点名**进 ask / deny ✅ ——
//
//	那是显式的动作，不是默认行为 ✅ ✅。
func TestDefaultPolicyFallsBackToAllow(t *testing.T) {
	p := defaultPolicy()
	if got := p.TierOf("some_tool_nobody_declared"); got != decl.TierAllow {
		t.Fatalf("undeclared tool tier = %q, want %q", got, decl.TierAllow)
	}
}

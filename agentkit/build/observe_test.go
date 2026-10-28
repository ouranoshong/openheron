package build

import (
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// 这个文件钉住 `/v2/agents` 的**另一半**：哪些工具真的存在。
//
// 配置里点过名的名字只能回答「能不能用」，答不了「还有哪些」—— `task` 与
// `skill` 由 eino 的中间件注册，名字不出现在任何配置文件里，于是从来不报。

func verdictsByName(t *testing.T, vs []ToolVerdict) map[string]ToolVerdict {
	t.Helper()
	out := map[string]ToolVerdict{}
	for _, v := range vs {
		out[v.Name] = v
	}
	return out
}

// ★ 没有 deny / disable 时也必须观测。
//
//	`Filter` 原本在那条路径上直接短路返回 —— 而 `allow` 里全是工具、`deny`
//	为空的配置是最常见的一种，恰好在那种配置下观测永远不会发生。
func TestFilterObservesEvenWithoutAnyBlocking(t *testing.T) {
	tbl := newTable("main", nil, nil) // 既无 deny 也无 disable
	tools := []tool.BaseTool{
		&stubTool{name: "read_file"},
		&stubTool{name: "task"},
	}
	kept, removed := tbl.Filter(tools)
	if len(kept) != 2 || len(removed) != 0 {
		t.Fatalf("kept %d removed %v, want all kept", len(kept), removed)
	}

	names, ok := tbl.Observed()
	if !ok {
		t.Fatal("Observed reported Known=false right after a Filter")
	}
	if len(names) != 2 || names[0] != "read_file" || names[1] != "task" {
		t.Fatalf("observed = %v, want [read_file task] (sorted)", names)
	}
}

// ★ 观测必须在**摘除之前**。
//
//	放在之后的话，被 deny 的工具会从「存在」里消失 —— 而它恰恰是最需要被
//	看见的那一类：「策略挡住了一个真实存在的工具」与「策略点名了一个不存在
//	的工具」是两件事，而两者在接口上长得一模一样。
func TestFilterObservesBeforeRemoving(t *testing.T) {
	tbl := newTable("main", []string{"grep"}, nil) // grep 被 deny
	kept, removed := tbl.Filter([]tool.BaseTool{
		&stubTool{name: "grep"},
		&stubTool{name: "read_file"},
	})
	if len(kept) != 1 || removed[LayerDeny] != 1 {
		t.Fatalf("kept %d removed %v", len(kept), removed)
	}

	names, _ := tbl.Observed()
	if len(names) != 2 {
		t.Fatalf("observed = %v, want grep to still be observed despite being removed", names)
	}
}

// ★ `Known=false` 与「一个工具都没有」必须分开。把前者当后者正是这个端点
func TestObservedUnknownBeforeAnyTurn(t *testing.T) {
	tbl := newTable("main", nil, nil)
	names, ok := tbl.Observed()
	if ok {
		t.Fatal("a table that never filtered should report Known=false")
	}
	if len(names) != 0 {
		t.Fatalf("names = %v, want empty", names)
	}
}

// ★ 观测是**覆盖**而不是累加：工具表在运行期会变（技能装上/卸下、深度
//
//	用尽后 `task` 被摘掉），接口该报的是「现在有哪些」，不是「历史上出现过哪些」。
func TestObserveOverwritesRatherThanAccumulates(t *testing.T) {
	tbl := newTable("main", nil, nil)
	tbl.Filter([]tool.BaseTool{&stubTool{name: "a"}, &stubTool{name: "b"}})
	tbl.Filter([]tool.BaseTool{&stubTool{name: "b"}})

	names, ok := tbl.Observed()
	if !ok || len(names) != 1 || names[0] != "b" {
		t.Fatalf("observed = %v, want just [b]", names)
	}
}

// ★ 报不出名字的工具**不猜** —— 猜一个名字会让接口报出一个不存在的工具。
func TestObserveSkipsToolsWithoutNames(t *testing.T) {
	tbl := newTable("main", nil, nil)
	tbl.Filter([]tool.BaseTool{
		&stubTool{name: ""},
		&stubTool{name: "grep"},
	})
	names, _ := tbl.Observed()
	if len(names) != 1 || names[0] != "grep" {
		t.Fatalf("observed = %v, want just [grep]", names)
	}
}

// ★ 运行期观测到的名字必须**进全集** —— 这正是本函数此前漏掉的那一半。
//
//	`task` 与 `skill` 由 eino 的中间件注册，名字不出现在任何配置里，于是
//	无论怎么配都报不出来，而模型在运行期真的能调到它们。漏报的表现是
//	「接口说这个 agent 只有 N 个工具」，而它其实有 N+2 个。
func TestRuntimeOnlyToolsAppearInEffectiveTools(t *testing.T) {
	cfg := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, nil)), nil)
	b := newTestBuilderWithConfig(t, cfg)
	// 配置里只点过 read_file —— 模拟真实项目里 allow 不写 task/skill 的情形。
	mustSnapshot(t, b).tableFor(mustSnapshot(t, b).agentSpec.defaultAgent).Filter([]tool.BaseTool{
		&stubTool{name: "read_file"},
		&stubTool{name: "task"},
		&stubTool{name: "skill"},
	})

	got := verdictsByName(t, b.EffectiveTools("main"))
	for _, want := range []string{"read_file", "task", "skill"} {
		v, ok := got[want]
		if !ok {
			t.Fatalf("EffectiveTools is missing %q; got %v", want, keysOfVerdicts(got))
		}
		// 没点名的工具落到兜底 allow，且**确实存在**。
		if want != "read_file" {
			if v.Tier != TierAllow {
				t.Errorf("%s tier = %q, want the allow fallback", want, v.Tier)
			}
			if !v.Present || !v.ObservedKnown {
				t.Errorf("%s: Present=%v ObservedKnown=%v, want both true", want, v.Present, v.ObservedKnown)
			}
		}
	}
}

// ★ 配置点名了一个**不存在**的工具（通常是拼错）—— 接口必须能说出来。
//
//	此前它与「挡住了一个真实存在的工具」长得一模一样：都是一行 deny。
func TestNamedButAbsentToolIsMarked(t *testing.T) {
	cfg := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, []string{"grep_search"})), nil)
	b := newTestBuilderWithConfig(t, cfg)
	mustSnapshot(t, b).tableFor(mustSnapshot(t, b).agentSpec.defaultAgent).Filter([]tool.BaseTool{&stubTool{name: "grep"}})

	got := verdictsByName(t, b.EffectiveTools("main"))
	typo := got["grep_search"]
	if typo.Tier != TierDeny {
		t.Fatalf("tier = %q, want deny", typo.Tier)
	}
	if !typo.ObservedKnown {
		t.Fatal("ObservedKnown should be true after a turn")
	}
	if typo.Present {
		t.Error("grep_search was never registered; Present must be false")
	}
	if real := got["grep"]; !real.Present {
		t.Error("grep was registered; Present must be true")
	}
}

// 还没跑过任何一轮时，Present 恒为 false 而 ObservedKnown 为 false ——
// 调用方据此知道「这个 false 不代表工具不存在」。
func TestBeforeAnyTurnNothingLooksPresent(t *testing.T) {
	cfg := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, nil)), nil)
	b := newTestBuilderWithConfig(t, cfg)
	mustSnapshot(t, b)

	vs := b.EffectiveTools("main")
	if len(vs) == 0 {
		t.Fatal("EffectiveTools returned nothing")
	}
	for _, v := range vs {
		if v.ObservedKnown {
			t.Errorf("%s: ObservedKnown = true before any turn", v.Name)
		}
		if v.Present {
			t.Errorf("%s: Present = true before any turn", v.Name)
		}
	}
	obs := b.RuntimeTools("main")
	if obs.Known || len(obs.Names) != 0 {
		t.Errorf("RuntimeTools before any turn = %+v, want unknown and empty", obs)
	}
}

func keysOfVerdicts(m map[string]ToolVerdict) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

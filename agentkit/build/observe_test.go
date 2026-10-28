package build

import (
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func verdictsByName(t *testing.T, vs []ToolVerdict) map[string]ToolVerdict {
	t.Helper()
	out := map[string]ToolVerdict{}
	for _, v := range vs {
		out[v.Name] = v
	}
	return out
}

func TestFilterObservesEvenWithoutAnyBlocking(t *testing.T) {
	tbl := newTable("main", nil, nil)
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

func TestFilterObservesBeforeRemoving(t *testing.T) {
	tbl := newTable("main", []string{"grep"}, nil)
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

func TestObserveOverwritesRatherThanAccumulates(t *testing.T) {
	tbl := newTable("main", nil, nil)
	tbl.Filter([]tool.BaseTool{&stubTool{name: "a"}, &stubTool{name: "b"}})
	tbl.Filter([]tool.BaseTool{&stubTool{name: "b"}})

	names, ok := tbl.Observed()
	if !ok || len(names) != 1 || names[0] != "b" {
		t.Fatalf("observed = %v, want just [b]", names)
	}
}

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

func TestRuntimeOnlyToolsAppearInEffectiveTools(t *testing.T) {
	cfg := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, nil)), nil)
	b := newTestBuilderWithConfig(t, cfg)
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

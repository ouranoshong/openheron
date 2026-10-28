package build

import (
	"context"
	"testing"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/contract"
)

// 没有「主 agent」：每个 agent 都能当会话的 agent，装配按它自己的配置来。
//
//	main → worker（worker 在 main 的树里是**下属**）
//	worker 自己也能当入口 ⇒ 以 worker 为入口时，它的树里没有 main
//
//	⇒ 同一个 agent 在不同入口下装配出的**树**不同，而不只是「深度」不同。
func TestTheEntryAgentBuildsItsOwnTree(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{},
		[]decl.SubAgentSpec{{ID: "worker", ModelRef: "test/model"}})
	cfg.Agents[spec.DefaultAgentID].SubAgents = []string{"worker"}
	cfg.Agents["worker"].SubAgents = nil

	b := newTestBuilderWithConfig(t, cfg)

	// ── 入口是 main → main + worker（worker 是下属）────────────────────
	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "main", contract.TurnQuery); err != nil {
		t.Fatalf("build main: %v", err)
	}
	if bc.n != 2 {
		t.Fatalf("main's tree must build main + worker, got %d builds", bc.n)
	}
	// ★ worker 作为下属时，它的树里**没有** main。
	for _, in := range bc.inputs {
		if in.Name == "worker" && len(in.SubAgents) != 0 {
			t.Fatalf("worker under main must have no children, got %v", in.SubAgents)
		}
	}

	// ── 入口是 worker → 只有 worker（它是入口）────────────────────────
	bc2 := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "worker", contract.TurnQuery); err != nil {
		t.Fatalf("build worker: %v", err)
	}
	if bc2.n != 1 {
		t.Fatalf("worker's own tree must build only itself, got %d builds", bc2.n)
	}
	if got := entryName(bc2); got != "worker" {
		t.Fatalf("the entry must be worker, got %q", got)
	}
}

// entryName 找出这次装配的**入口**是哪个。
//
// ★ 不能靠下标：buildNode 先递归子树再构造自己，所以 inputs 的**最后**
//
//	一个才是入口。
func entryName(bc *buildCount) string {
	if len(bc.inputs) == 0 {
		return ""
	}
	return bc.inputs[len(bc.inputs)-1].Name
}

// 空 agentID ⇒ 默认 agent（main）—— 会话的 agent 正常不会为空（建会话
// 时就落盘了），这里兜的是「调用方没经过会话层」的情况。
func TestTheDefaultAgentNeedsNoConfiguration(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{},
		[]decl.SubAgentSpec{{ID: "worker", ModelRef: "test/model"}})
	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "", contract.TurnQuery); err != nil {
		t.Fatalf("build the default agent: %v", err)
	}
	if bc.n != 2 {
		t.Fatalf("the default agent must build main + worker, got %d builds", bc.n)
	}
	if got := entryName(bc); got != spec.DefaultAgentID {
		t.Fatalf("the default agent must be main, got %q", got)
	}
}

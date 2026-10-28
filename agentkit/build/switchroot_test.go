package build

import (
	"context"
	"errors"
	"testing"

	"openheron/agentkit/spec/decl"
	"openheron/contract"
)

// ★ 只有 main 的**直接**子 agent 能作为会话的 agent —— 孙 agent 需要自带一条
// 委派链，「以它为根」与「通过 task 走进去」不是同一件事。
//
// ★ 这条走的是**生产的切换路径**（[Builder.Build]），不是某个只被测试
//
//	调用的查找辅助函数 —— 后者钉不住任何真实行为：生产代码换掉查找方式，
//	测试照样绿。
func TestEveryDeclaredAgentCanBeTheSessionAgent(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		// ★ worker 必须自带模型引用：它当入口时**不继承** main 的模型
		//   （见 TestBuildRootWithoutModelFailsLoudly）。
		// ★ leaf 也自带模型引用：它当入口时**不继承**别人的模型
		//   （config 的校验要求每个 agent 都声明 model）。
		{ID: "worker", ModelRef: "test/model", Sub: []decl.SubAgentSpec{
			{ID: "leaf", ModelRef: "test/model"},
		}},
	})
	b := newTestBuilderWithConfig(t, cfg)

	ctx := context.Background()
	// ★ 声明了的都能当会话的 agent —— 嵌套多深都行。
	if _, err := b.Build(ctx, "s1", "worker", contract.TurnQuery); err != nil {
		t.Fatalf("a declared agent must be usable as the session agent: %v", err)
	}
	if _, err := b.Build(ctx, "s1", "leaf", contract.TurnQuery); err != nil {
		t.Fatalf("a nested declared agent must be usable as the session agent: %v", err)
	}
	// 只有**没声明**的才拒绝。
	if _, err := b.Build(ctx, "s1", "ghost", contract.TurnQuery); !errors.Is(err, errAgentNotDeclared) {
		t.Fatalf("an undeclared agent must be rejected, got %v", err)
	}
}

// ★ 同名 agent 在**不同代际**下不能共用实例。
//
//	池的键只有 (名, 代际)：换代 ⇒ 新实例，同代 ⇒ 同一实例。
func TestPoolSeparatesGenerations(t *testing.T) {
	p := newPool()
	c := &countingBuild{}

	old, err := p.GetOrBuild(context.Background(), "task", 1, c.fn("task"))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := p.GetOrBuild(context.Background(), "task", 2, c.fn("task"))
	if err != nil {
		t.Fatal(err)
	}
	if old == cur {
		t.Fatal("★ entries from different generations must not be shared")
	}
	if c.count() != 2 {
		t.Fatalf("each generation must build once, got %d builds", c.count())
	}
}

// 切到直接子 agent：它以自己的 instruction / 工具表 / 模型装配成根，
// 并且第二次命中池。
func TestBuildSwitchableRootIsPooledSeparately(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		{ID: "worker", Description: "worker", Instruction: "do work", ModelRef: "test/model"},
	})
	b := newTestBuilderWithConfig(t, cfg)

	ctx := context.Background()
	mainAgent, err := b.Build(ctx, "s1", "", contract.TurnQuery)
	if err != nil {
		t.Fatalf("build main: %v", err)
	}
	worker, err := b.Build(ctx, "s1", "worker", contract.TurnQuery)
	if err != nil {
		t.Fatalf("build worker as root: %v", err)
	}
	if worker == nil {
		t.Fatal("the switched root must not be nil")
	}
	if worker == mainAgent {
		t.Fatal("the switched root must not reuse the main instance")
	}

	again, err := b.Build(ctx, "s1", "worker", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	if again != worker {
		t.Fatal("the second turn must hit the pool, not rebuild")
	}
}

// 未知 / 不可切换的名字必须报出可识别的错误，而不是回退到 main ——
// 回退会让一次写错名字的切换静默变成「没切」，用户以为切了。
func TestBuildRejectsUnknownRoot(t *testing.T) {
	b := newTestBuilder(t)
	_, err := b.Build(context.Background(), "s1", "ghost", contract.TurnQuery)
	if !errors.Is(err, errAgentNotDeclared) {
		t.Fatalf("want errAgentNotDeclared, got %v", err)
	}
}

// 没有模型引用时以它为根必须失败，不能悄悄继承 main 的模型 ——
// 那会是一轮「按 worker 的工具表、用 main 的模型」的混合运行。
func TestBuildRootWithoutModelFailsLoudly(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		{ID: "worker", Description: "worker"},
	})
	b := newTestBuilderWithConfig(t, cfg)

	_, err := b.Build(context.Background(), "s1", "worker", contract.TurnQuery)
	if err == nil {
		t.Fatal("a root without a model reference must be rejected")
	}
}

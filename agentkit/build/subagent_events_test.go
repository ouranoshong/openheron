package build

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk/prebuilt/deep"

	"openheron/agentkit/spec/decl"
	"openheron/contract"
)

// ★ EmitInternalEvents 必须**开着** —— 关着的时候，子 agent 是父的一个普通
//
//	工具调用，它的中间过程（推理、工具调用、原始工具返回）在工具内部跑完，
//	界面看不到、记录里也没有。
//
//	打开后 eino 把子 agent 的事件转发到父的 AsyncGenerator，界面才能实时
//	显示专家的过程（带 subagentRunId 归属），归档里也才有可观测的记录。
//
//	★ 这是**接线测试**：走真实的 Build 路径，断言到达 deep.New 的那个
//	  配置里开关是开的 —— 只测 applyToolConfig 的话，漏掉调用点照样全绿。
func TestSubagentEventsAreForwardedToTheParentStream(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		{ID: "main", ModelRef: "test/model", Description: "根", Sub: []decl.SubAgentSpec{
			{ID: "task", ModelRef: "test/model", Description: "专家"},
		}},
	})
	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "main", contract.TurnQuery); err != nil {
		t.Fatalf("build main: %v", err)
	}
	if bc.n == 0 {
		t.Fatal("the assembly entry was not called")
	}
	for _, in := range bc.inputs {
		if !in.ToolsConfig.EmitInternalEvents {
			t.Fatalf("agent %q: EmitInternalEvents must be on — otherwise the subagent's "+
				"reasoning and tool calls never reach the parent's event stream", in.Name)
		}
	}
}

// ★ 开关只管**转发**，不管隔离 —— 隔离在 agentkit/run 的 buildInput（下一轮建上下文时
//
//	剔掉带 openheron_subagent 标记的消息）。
//
//	这条守的是「别把两件事混成一件」：打开转发**不等于**把专家的过程
//	灌回父的上下文。
func TestForwardingIsPairedWithTheContextFilter(t *testing.T) {
	// 装配层**只**设转发开关；它不持有任何消息，也不做过滤 ——
	// 过滤的唯一地点是 buildInput（见 agentkit/run/input_subagent_scope_test.go）。
	cfg := deep.Config{}
	applyToolConfig(&cfg, heavyDeps{})
	if !cfg.ToolsConfig.EmitInternalEvents {
		t.Fatal("applyToolConfig must turn EmitInternalEvents on")
	}
	// 装配层不产生消息 —— 它没有消息可剔。
	if len(cfg.ToolsConfig.Tools) != 0 {
		t.Fatalf("the assembly layer must not hold messages, got %d tools", len(cfg.ToolsConfig.Tools))
	}
}

package build

import (
	"bytes"
	"context"
	"encoding/gob"
	"github.com/cloudwego/eino/adk"
	"openheron/agentkit/spec/decl"
	"openheron/approval"
	"reflect"
	"testing"
)

// ★ 根与子 agent 必须走**同一条** tool 侧中间件链。
//
//	两者的差别是「工具边界」（子 agent 的 disabled_tools 更严），不是
//	「有哪些中间件」。各写一遍的话，改动只会生效一边，而两边的测试照样
//	都绿 —— 这正是「单一产地」要消灭的那类漂移。
//
//	★ 这条曾经是红的：子 agent 那条链少了 SafeTool，于是子 agent 调工具
//
//	出错会整轮失败，而根同样的错会变成一句「[tool error] …」让模型自己改。
func TestRootAndSubAgentShareTheSameToolChain(t *testing.T) {
	b := newTestBuilder(t)
	snap := mustSnapshot(t, b)
	policy, table, maxDepth := snap.agentSpec.policy, snap.tableFor(snap.agentSpec.defaultAgent), snap.agentSpec.maxDepth

	root := toolChain(newBoundary(policy, table), maxDepth)
	sub := subHandlers(policy, decl.SubAgentSpec{ID: "x"}, maxDepth)

	if len(root) != len(sub) {
		t.Fatalf("root chain has %d middlewares, sub-agent has %d — "+
			"they must be the same chain, only the tool table differs",
			len(root), len(sub))
	}
	for i := range root {
		if reflect.TypeOf(root[i]) != reflect.TypeOf(sub[i]) {
			t.Fatalf("middleware %d differs: root has %T, sub-agent has %T",
				i, root[i], sub[i])
		}
	}
}

// ★ 中间件链的**顺序**有讲究（eino quickstart 第五章的洋葱模型）。
//
//	Filter 必须在 Approval 之前：deny 档的工具要在闸门看见它之前就被摘掉。
//	UnknownToolScope 必须在 Filter 之后：它要标注的是**过滤后**的工具表。
func TestToolChainOrderPutsFilterBeforeApproval(t *testing.T) {
	chain := toolChain(newBoundary(NewPolicy(nil, nil), newTable("main", nil, nil)), 1)

	fi, ai, ui := -1, -1, -1
	for i, mw := range chain {
		switch mw.(type) {
		case *filterMiddleware:
			fi = i
		case *approvalMiddleware:
			ai = i
		case *unknownToolScope:
			ui = i
		}
	}
	if fi < 0 || ai < 0 || ui < 0 {
		t.Fatalf("chain is missing a middleware: filter=%d approval=%d scope=%d", fi, ai, ui)
	}
	if fi > ai {
		t.Fatalf("filterMiddleware at %d runs after ApprovalMiddleware at %d; "+
			"a denied tool would reach the gate before it was removed", fi, ai)
	}
	if ui < fi {
		t.Fatalf("UnknownToolScope at %d runs before filterMiddleware at %d; "+
			"it would annotate the unfiltered table and offer tools this turn cannot call", ui, fi)
	}
}

// ★ SafeTool 必须在链上：它把工具错误变成「[tool error] …」让模型自己改，
//
//	而不是整轮失败。
//
//	而它**不会**把审批中断吞掉 —— helpers/middleware.go 里透传了
//	IsInterruptRerunError。
func TestToolChainHasTheSafeToolMiddleware(t *testing.T) {
	chain := toolChain(newBoundary(NewPolicy(nil, nil), newTable("main", nil, nil)), 1)

	// SafeTool 是链上唯一一个包住工具**调用**的中间件，其余都作用在
	// 工具表或 agent 拓扑上 —— 所以按「有没有包住调用」来认它。
	found := false
	for _, mw := range chain {
		if _, ok := mw.(interface {
			WrapInvokableToolCall(context.Context, adk.InvokableToolCallEndpoint, *adk.ToolContext) (adk.InvokableToolCallEndpoint, error)
		}); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("★ no SafeToolMiddleware on the chain — a tool error would kill the turn " +
			"instead of reaching the model as a readable result")
	}
}

// ★ The approval payload crosses a gob boundary every time a turn stops on a
//
//	human decision. Without this registration the failure surfaces as a model
//	error at checkpoint-commit time, which reads like a bad model rather than
//	a missing registration.
func TestApprovalInfoSurvivesGob(t *testing.T) {
	in := map[string]any{"interrupt": &approval.ApprovalInfo{
		ToolName:        "execute",
		ArgumentsInJSON: `{"command":"go build ./..."}`,
	}}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out map[string]any
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, ok := out["interrupt"].(*approval.ApprovalInfo)
	if !ok {
		t.Fatalf("round trip lost the concrete type: %T", out["interrupt"])
	}
	if got.ToolName != "execute" || got.ArgumentsInJSON == "" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/cloudwego/eino/adk/prebuilt/deep"

	"openheron/agentkit/spec/decl"
)

func subSpec(id string, subs ...decl.SubAgentSpec) decl.SubAgentSpec {
	return decl.SubAgentSpec{
		ID:          id,
		Description: "子 agent " + id,
		Instruction: "你是 " + id,
		Sub:         subs,
	}
}

// buildSubtree 装配一棵**以子 agent 为顶层**的子树（不含根）。
//
// ★ depth=1 与生产里「根的下挂」同一层：maxDepth=1 时它们的下一层不再构造。
func buildSubtree(t *testing.T, specs []decl.SubAgentSpec, maxDepth int) *buildCount {
	t.Helper()
	// ★ 策略要**点名** write_file（ask 档）—— 兜底是 allow，
	//   没点名的工具压根不进闸门 ✅ ✅。
	cfg := configFromSpecs("test/model", nil,
		decl.Policy{Ask: []string{"write_file"}}, specs)
	b := newTestBuilderWithConfig(t, cfg)
	snap, err := b.snapshotFor(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	snap.agentSpec.maxDepth = maxDepth

	// ★ 底板带的是**入口 agent**的身份（每个 agent 的身份不同）。
	rootSp, ok := snap.agentSpec.agentSpec(snap.agentSpec.defaultAgent)
	if !ok {
		t.Fatalf("the default agent %q is not declared", snap.agentSpec.defaultAgent)
	}
	base := b.baseConfig(snap, rootSp)
	base.ChatModel = &stubModel{}
	bc := countBuilds(t)
	for i := range specs {
		sp := specs[i]
		if _, _, err := b.buildNode(context.Background(), snap, node{
			base: base, agentSpec: &sp, depth: 1,
		}); err != nil {
			t.Fatalf("build %q: %v", sp.ID, err)
		}
	}
	return bc
}

// ── 深度 ─────────────────────────────────────────────────────────────────

func TestDepthStartsAtZeroAndIncrements(t *testing.T) {
	ctx := context.Background()
	if got := depthFrom(ctx); got != 0 {
		t.Fatalf("unlabelled depth must be 0 (root), got %d", got)
	}
	ctx2 := withDepth(ctx, 1)
	if got := depthFrom(ctx2); got != 1 {
		t.Fatalf("after annotation it must be 1, got %d", got)
	}
}

// ★ 无论是否摘工具都要往下标一层 —— 否则上限变成「每层各算一次」。
func TestDepthGuardAlwaysIncrements(t *testing.T) {
	mw := newDepthMiddleware(1)
	ctx, _, err := mw.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatal(err)
	}
	if got := depthFrom(ctx); got != 1 {
		t.Fatalf("the layer must still be marked after reaching the cap, got %d", got)
	}
}

func TestDepthGuardRemovesTaskAtLimit(t *testing.T) {
	mw := newDepthMiddleware(1)
	runCtx := &adk.ChatModelAgentContext{
		Tools: []tool.BaseTool{&stubTool{name: "read_file"}, &stubTool{name: taskToolName}},
	}
	// 深度 1 == 上限 1 ⇒ 不许再往下派。
	ctx := withDepth(context.Background(), 1)
	_, out, err := mw.BeforeAgent(ctx, runCtx)
	if err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(namesOf(out.Tools), ","); strings.Contains(names, taskToolName) {
		t.Fatalf("* at the cap, task must be removed, got %q", names)
	}
	if names := strings.Join(namesOf(out.Tools), ","); !strings.Contains(names, "read_file") {
		t.Fatalf("the other tools must be untouched, got %q", names)
	}
}

func TestDepthGuardKeepsTaskBelowLimit(t *testing.T) {
	mw := newDepthMiddleware(2)
	runCtx := &adk.ChatModelAgentContext{Tools: []tool.BaseTool{&stubTool{name: taskToolName}}}
	ctx := withDepth(context.Background(), 0)
	_, out, err := mw.BeforeAgent(ctx, runCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 1 {
		t.Fatalf("below the cap, task must be kept, got %v", namesOf(out.Tools))
	}
}

// ★ 到顶时**仍然标层**，且到顶的 agent 仍能做自己的事。
func TestDepthGuardDoesNotBreakTheAgentItself(t *testing.T) {
	mw := newDepthMiddleware(1)
	runCtx := &adk.ChatModelAgentContext{Tools: []tool.BaseTool{&stubTool{name: "read_file"}}}
	ctx := withDepth(context.Background(), 5)
	_, out, err := mw.BeforeAgent(ctx, runCtx)
	if err != nil {
		t.Fatalf("reaching the cap must not error: %v", err)
	}
	if len(out.Tools) != 1 {
		t.Fatal("到顶的 agent 仍应保留自己的工具")
	}
}

// ── 子 agent 构造 ───────────────────────────────────────────────────────

func TestSubAgentsAreBuilt(t *testing.T) {
	bc := buildSubtree(t, []decl.SubAgentSpec{subSpec("researcher"), subSpec("tester")}, 1)
	if bc.n != 2 {
		t.Fatalf("2 sub-agents must be built, got %d", bc.n)
	}
}

// ★ R1 闭环：子 agent 必须**逐个**带自己的审批中间件。
//
// 这是本次最关键的一条 —— eino 不把 cfg.Handlers 传播给用户 SubAgents，
// 不显式挂上就等于子 agent 能无审批地写文件。
func TestSubAgentsCarryTheirOwnApprovalMiddleware(t *testing.T) {
	policy := NewPolicy([]string{"write_file"}, nil)
	handlers := subHandlers(policy, subSpec("writer"), 1)

	if !chainGatesWriteFile(t, handlers) {
		t.Fatal("★ 子 agent 的中间件链里没有闸门 —— 它能无审批地写文件")
	}
}

func TestSubHandlersApplyItsOwnDeny(t *testing.T) {
	policy := NewPolicy([]string{"write_file"}, []string{"execute"})
	handlers := subHandlers(policy, decl.SubAgentSpec{ID: "w", DisabledTools: []string{"write_file"}}, 1)

	// 它的 disabled_tools 不含 execute，但全局 deny 含 —— deny 必须赢。
	fw := findFilter(t, handlers)
	if fw == nil {
		t.Fatal("中间件链里没有过滤器")
	}
	if got := fw.table.Blocked("execute"); got != LayerDeny {
		t.Fatalf("* the global deny must hold on sub-agents too, got %q", got)
	}
	if got := fw.table.Blocked("write_file"); got != LayerDisable {
		t.Fatalf("its own disabled_tools must be handled as the inner layer, got %q", got)
	}
}

// ── 深度截断 ─────────────────────────────────────────────────────────────

// ★ 到顶**不再递归构造下一层**，而不是建出来再摘工具。
func TestSubAgentsStopAtMaxDepth(t *testing.T) {
	bc := buildSubtree(t, []decl.SubAgentSpec{subSpec("researcher", subSpec("grandchild"))}, 1)
	if bc.n != 1 {
		t.Fatalf("with maxDepth=1 only researcher must be built, got %d", bc.n)
	}
}

func TestSubAgentsOneLevelDeeperWhenAllowed(t *testing.T) {
	bc := buildSubtree(t, []decl.SubAgentSpec{subSpec("researcher", subSpec("grandchild"))}, 2)
	if bc.n != 2 {
		t.Fatalf("with maxDepth=2 there must be 2 sub-agents, got %d", bc.n)
	}
}

// ★★ R1 在**每一层**都成立：嵌套的子 agent 也必须带自己的闸门。
func TestEveryNestedSubAgentCarriesTheApprovalGate(t *testing.T) {
	bc := buildSubtree(t, []decl.SubAgentSpec{subSpec("researcher", subSpec("grandchild"))}, 2)
	if bc.n != 2 {
		t.Fatalf("expected two sub-agents to be built, got %d", bc.n)
	}
	for _, cfg := range bc.inputs {
		if cfg.Name == "" {
			t.Fatal("a sub-agent must carry its identity")
		}
		if !chainGatesWriteFile(t, cfg.Handlers) {
			t.Fatalf("★ sub-agent %q has no approval gate — it can write files "+
				"without a human decision", cfg.Name)
		}
	}
}

func TestDefaultMaxDepthBlocksFurtherNesting(t *testing.T) {
	if decl.DefaultMaxDepth != 1 {
		t.Fatalf("the default depth must be 1 (only root delegates), got %d", decl.DefaultMaxDepth)
	}
}

// countBuilds 把包内的装配入口换成计数版本，返回这次真构造了几个 agent
// 以及每次的输入。
//
// ★ 它换的是**生产的那个入口**（`buildAgent`），不是另写一份装配逻辑。
func countBuilds(t *testing.T) *buildCount {
	t.Helper()
	orig := buildAgent
	bc := &buildCount{}
	buildAgent = func(ctx context.Context, cfg deep.Config) (adk.TypedAgent[*schema.Message], error) {
		bc.n++
		bc.inputs = append(bc.inputs, cfg)
		return orig(ctx, cfg)
	}
	t.Cleanup(func() { buildAgent = orig })
	return bc
}

// buildCount 记下装配入口被调了几次、每次的输入是什么。
type buildCount struct {
	n      int
	inputs []deep.Config
}

// findFilter 从链里找出过滤器中间件。
func findFilter(t *testing.T, h []adk.TypedChatModelAgentMiddleware[*schema.Message]) *filterMiddleware {
	t.Helper()
	for _, m := range h {
		if fm, ok := m.(*filterMiddleware); ok {
			return fm
		}
	}
	return nil
}

// chainGatesWriteFile 逐个跑中间件的 WrapInvokableToolCall，
// 看 write_file 最终是不是挂起而不是直接执行。
func chainGatesWriteFile(t *testing.T, h []adk.TypedChatModelAgentMiddleware[*schema.Message]) bool {
	t.Helper()
	reached := false
	endpoint := func(context.Context, string, ...tool.Option) (string, error) {
		reached = true
		return "", nil
	}
	var wrapped adk.InvokableToolCallEndpoint = endpoint
	tc := toolContext("write_file")

	for _, m := range h {
		if !implementsToolWrapper(m) {
			continue
		}
		next, err := wrapToolCall(m, wrapped, tc)
		if err != nil {
			t.Fatal(err)
		}
		wrapped = next
	}
	// 不给 resume context ⇒ 若被闸门挂起，endpoint 绝不该被触达。
	_, _ = wrapped(context.Background(), "{}")
	return !reached
}

// implementsToolWrapper 判断中间件是否实现了工具调用包装。
type toolWrapper interface {
	WrapInvokableToolCall(context.Context, adk.InvokableToolCallEndpoint, *adk.ToolContext) (adk.InvokableToolCallEndpoint, error)
}

func implementsToolWrapper(m adk.TypedChatModelAgentMiddleware[*schema.Message]) bool {
	_, ok := m.(toolWrapper)
	return ok
}

func wrapToolCall(m adk.TypedChatModelAgentMiddleware[*schema.Message],
	endpoint adk.InvokableToolCallEndpoint, tc *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return m.(toolWrapper).WrapInvokableToolCall(context.Background(), endpoint, tc)
}

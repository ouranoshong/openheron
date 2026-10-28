package build

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/contract"
)

// scriptModel 按脚本逐轮返回消息，并**记下每一轮收到的历史**。
//
// ★ 断言打在「模型第二轮看到的历史」上，而不是解析事件流：工具结果是
//
//	不是被正确回灌给模型，恰好就是这件事的全部含义，且不需要解码
//	adk 的事件类型。
type scriptModel struct {
	mu      sync.Mutex
	turn    int
	seen    [][]*schema.Message
	factory func() model.ToolCallingChatModel
	// tool 是第一轮要调的工具名。留空 ⇒ "grep"。
	//
	// ★ 参数化是为了「调一个**被 deny 掉**的工具」也能测 —— 那时工具
	//   存在过（被摘掉了），与「本来就不存在」是两件事。
	tool string
}

func (m *scriptModel) record(in []*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen = append(m.seen, in)
}

func (m *scriptModel) history() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen
}

func (m *scriptModel) next(in []*schema.Message) (*schema.Message, error) {
	m.record(in)
	m.mu.Lock()
	m.turn++
	turn := m.turn
	m.mu.Unlock()

	if turn == 1 {
		// ★ 调一个**本轮不存在**的工具名。ToolsNode 认不出它。
		name := m.tool
		if name == "" {
			name = "grep"
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      name,
				Arguments: `{"pattern":"x"}`,
			},
		}}), nil
	}
	return schema.AssistantMessage("done", nil), nil
}

func (m *scriptModel) Generate(_ context.Context, in []*schema.Message,
	_ ...model.Option) (*schema.Message, error) {
	return m.next(in)
}

func (m *scriptModel) Stream(_ context.Context, in []*schema.Message,
	_ ...model.Option) (*schema.StreamReader[*schema.Message], error) {

	msg, err := m.next(in)
	if err != nil {
		return schema.StreamReaderFromArray([]*schema.Message{}), err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *scriptModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// runScripted 造一个会调 unknownName 的 agent，跑一轮，把模型看到的历史交回。
//
// ★ deny 里的名字会被 [filterMiddleware] 摘掉，于是模型调它就等于调了一个
//
//	「本轮不存在的工具」—— 这正是要测的场景。
//
//	★ 不能靠「选一个本来就不存在的工具名」来造这个场景：文件系统工具
//
//	（read_file / grep / execute …）是**真的存在**的，2026-10-21 之前它们
//	被漏接上了（见 backendtools_test.go），用它们当「不存在的工具」会让
//	这条用例在某天突然测不到它想测的东西。
func runScripted(t *testing.T, unknownName string, deny ...string) [][]*schema.Message {
	t.Helper()

	m := &scriptModel{}
	cfg := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, deny)), nil)
	cfg.Agents[spec.DefaultAgentID].Instruction = "you are a test agent"
	cfg.Agents[spec.DefaultAgentID].MaxIterations = 3

	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(context.Context, *spec.Config, string, CallIntent) (
		model.BaseModel[*schema.Message], error) {
		return m, nil
	}
	a, err := b.Build(context.Background(), "main", "", contract.TurnQuery)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	iter := a.Run(context.Background(), &adk.TypedAgentInput[*schema.Message]{
		Messages: []*schema.Message{schema.UserMessage("find it")},
	})
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			// ★ 这就是修复前用户看到的那一行：整轮失败，零输出。
			t.Fatalf("run failed on the %q tool call: %v", unknownName, ev.Err)
		}
	}
	return m.history()
}

// toolResultFor 在历史里找名字为 name 的那条工具结果。
func toolResultFor(hist [][]*schema.Message, name string) string {
	var b strings.Builder
	for _, msgs := range hist {
		for _, msg := range msgs {
			if msg.Role != "tool" {
				continue
			}
			b.WriteString(msg.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// TestUnknownToolCallDoesNotKillTheRun 是这个文件存在的理由。
//
// ★ 修复前：模型调一个本轮没有的工具 ⇒ ToolsNode 返回
//
//	"tool grep not found in toolsNode indexes" ⇒ 整轮失败，模型看不到任何
//	东西，也就无从改用别的工具。而触发它的正是 openheron 自己的 context
//	prompt（agentkit/run/input.go 无条件教模型用 grep），那份 prompt 并不知道
//	工具表被 disabled_tools / deny 摘掉了什么。
func TestUnknownToolCallDoesNotKillTheRun(t *testing.T) {
	hist := runScripted(t, "grep", "grep")

	got := toolResultFor(hist, "grep")
	if !strings.Contains(got, `"grep"`) {
		t.Fatalf("no tool result mentioning grep; the model was never told anything. history:\n%v", hist)
	}
	if !strings.Contains(got, "not available") {
		t.Fatalf("tool result should say grep is unavailable, got: %q", got)
	}
	// ★ 必须带上替代项：只说「不可用」等于让模型在原地转圈。
	if !strings.Contains(got, "Available tools:") {
		t.Fatalf("tool result should list what IS available, got: %q", got)
	}
}

// TestUnknownToolScopeNamesOnlySurvivingTools 钉住「可用清单是过滤后的」。
//
// ★ 注册顺序是这个中间件的全部意义。eino 的 applyBeforeAgent 按注册顺序
//
//	依次调 handler，后一个看到的是前一个改过的 runCtx —— 所以标注必须
//	排在 filterMiddleware **之后**。反了的话，它标的是未过滤的那份，
//	deny 掉 grep 之后兜底仍回一句「grep 可用」，模型转一圈再撞同一堵墙。
func TestUnknownToolScopeNamesOnlySurvivingTools(t *testing.T) {
	filter := newFilterMiddleware(newTable("main", []string{"grep"}, nil))
	scope := newUnknownToolScope()

	runCtx := &adk.ChatModelAgentContext{
		Tools: []tool.BaseTool{
			&stubTool{name: "ls"}, &stubTool{name: "grep"}, &stubTool{name: "read_file"},
		},
	}
	ctx := context.Background()
	var err error
	ctx, runCtx, err = filter.BeforeAgent(ctx, runCtx)
	if err != nil {
		t.Fatal(err)
	}
	ctx, _, err = scope.BeforeAgent(ctx, runCtx)
	if err != nil {
		t.Fatal(err)
	}

	got := toolNamesFrom(ctx)
	if slices.Contains(got, "grep") {
		t.Fatalf("the scope middleware saw the unfiltered table: %v", got)
	}
	for _, want := range []string{"ls", "read_file"} {
		if !slices.Contains(got, want) {
			t.Fatalf("scope middleware lost a surviving tool: %v", got)
		}
	}

	// 兜底文案必须与标注一致，否则就是「说一套做一套」。
	reply, err := UnknownToolsReply(ctx, "grep", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "grep,") || strings.Contains(reply, ", grep") {
		t.Fatalf("reply offered a filtered tool: %q", reply)
	}
	if !strings.Contains(reply, "ls") {
		t.Fatalf("reply lost the surviving tools: %q", reply)
	}
}

// TestUnknownToolScopeIsRegisteredAfterTheFilter 守住**实际注册顺序**。
//
// ★ 上一条用例是手工把两个中间件按正确顺序串起来的，测的是契约；
//
//	这条测的是装配 —— 真正的顺序写在 b.handlers 与 subHandlers 里，而那两处
//	没有任何类型系统的约束：调换它们编译照样过、[Table.Filter] 照样绿，
//	只有兜底文案会开始说谎。
func TestUnknownToolScopeIsRegisteredAfterTheFilter(t *testing.T) {
	check := func(t *testing.T, label string, hs []adk.TypedChatModelAgentMiddleware[*schema.Message]) {
		t.Helper()
		fi, si := -1, -1
		for i, h := range hs {
			switch h.(type) {
			case *filterMiddleware:
				fi = i
			case *unknownToolScope:
				si = i
			}
		}
		if fi < 0 {
			t.Fatalf("%s: no filterMiddleware registered", label)
		}
		if si < 0 {
			t.Fatalf("%s: no UnknownToolScope registered", label)
		}
		if si < fi {
			t.Fatalf("%s: UnknownToolScope at %d runs before filterMiddleware at %d; "+
				"it would annotate the unfiltered table and offer tools this turn cannot call",
				label, si, fi)
		}
	}

	b := newTestBuilder(t)
	snap := mustSnapshot(t, b)
	policy, table, maxDepth := snap.agentSpec.policy, snap.tableFor(snap.agentSpec.defaultAgent), snap.agentSpec.maxDepth
	check(t, "root", toolChain(newBoundary(policy, table), maxDepth))
	check(t, "sub-agent", subHandlers(NewPolicy(nil, nil), decl.SubAgentSpec{ID: "x"}, 1))
}

// TestUnknownToolsReplyIsSafeWithoutToolNames 覆盖中间件没跑到的情况
// （例如 ToolsNode 被直接复用）。此时只能说「本轮没有工具」，
// 不能编一个清单出来 —— 编出来的清单会让模型去调第二个不存在的工具。
func TestUnknownToolsReplyIsSafeWithoutToolNames(t *testing.T) {
	got, err := UnknownToolsReply(context.Background(), "grep", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"grep"`) || !strings.Contains(got, "no tools") {
		t.Fatalf("reply = %q", got)
	}
	if strings.Contains(got, "Available tools:") {
		t.Fatalf("must not invent a tool list: %q", got)
	}
}

// TestWithUnknownToolsReplyKeepsAnExistingHandler 守住「不覆盖」。
// 装配层若自己挂了 handler，那是它的选择，本函数只管「没配时有兜底」。
func TestWithUnknownToolsReplyKeepsAnExistingHandler(t *testing.T) {
	mine := func(context.Context, string, string) (string, error) { return "mine", nil }

	tc := withUnknownToolsReply(adk.ToolsConfig{})
	if tc.ToolsNodeConfig.UnknownToolsHandler == nil {
		t.Fatal("zero config must get the fallback")
	}

	tc2 := withUnknownToolsReply(withHandler(mine))
	got, err := tc2.ToolsNodeConfig.UnknownToolsHandler(context.Background(), "grep", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "mine" {
		t.Fatalf("an existing handler was overwritten: got %q", got)
	}
}

func withHandler(h func(context.Context, string, string) (string, error)) adk.ToolsConfig {
	var tc adk.ToolsConfig
	tc.ToolsNodeConfig.UnknownToolsHandler = h
	return tc
}

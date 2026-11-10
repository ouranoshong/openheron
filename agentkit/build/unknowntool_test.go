package build

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

type scriptModel struct {
	mu      sync.Mutex
	turn    int
	seen    [][]*schema.Message
	factory func() model.ToolCallingChatModel
	tool    string
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

// runScripted runs a scripted model and returns the history.
//
// ★ The second argument names the tools the agent does **not** get: they
//
//	are the agent's own tools list, and a model that calls one has to
//	be told it is not available.
//
// runScripted builds an agent whose tools: list is everything except the
// named ones — so the model calls a tool the agent does not have.
func runScripted(t *testing.T, unknownName string, without ...string) [][]*schema.Message {
	t.Helper()

	m := &scriptModel{}
	cfg := configFromSpecs("test/model", allToolsExcept(without...), nil, nil)
	cfg.Agents[spec.DefaultAgentID].Instruction = "you are a test agent"
	cfg.Agents[spec.DefaultAgentID].MaxIterations = 3

	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(context.Context, *spec.Config, string, CallIntent, *slog.Logger) (
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
			t.Fatalf("run failed on the %q tool call: %v", unknownName, ev.Err)
		}
	}
	return m.history()
}

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

func TestUnknownToolCallDoesNotKillTheRun(t *testing.T) {
	hist := runScripted(t, "grep", "grep")

	got := toolResultFor(hist, "grep")
	if !strings.Contains(got, `"grep"`) {
		t.Fatalf("no tool result mentioning grep; the model was never told anything. history:\n%v", hist)
	}
	if !strings.Contains(got, "not available") {
		t.Fatalf("tool result should say grep is unavailable, got: %q", got)
	}
	if !strings.Contains(got, "Available tools:") {
		t.Fatalf("tool result should list what IS available, got: %q", got)
	}
}

func TestUnknownToolScopeNamesOnlySurvivingTools(t *testing.T) {
	// ★ The list names the two tools that survive; `grep` is not among
	//   them, so the filter drops it.
	filter := newFilterMiddleware(newTable("main", []string{"ls", "read_file"}, nil))
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
	check(t, "sub-agent", subHandlers(NewPolicy(nil), decl.SubAgentSpec{ID: "x"}, 1))
}

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

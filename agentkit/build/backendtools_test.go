package build

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

type plainModel struct{ turns int }

func (m *plainModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.turns++
	return schema.AssistantMessage("done", nil), nil
}

func (m *plainModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return schema.StreamReaderFromArray([]*schema.Message{}), err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *plainModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type toolCallingModel struct {
	tool string
	args string
	turn int
}

func (m *toolCallingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.turn++
	if m.turn != 1 {
		return schema.AssistantMessage("done", nil), nil
	}
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:   "call-1",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      m.tool,
			Arguments: m.args,
		},
	}}), nil
}

func (m *toolCallingModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return schema.StreamReaderFromArray([]*schema.Message{}), err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *toolCallingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func newModelBuilder(t *testing.T, cfg *spec.Config, fallback model.BaseModel[*schema.Message],
	byRef map[string]model.BaseModel[*schema.Message]) *Builder {

	t.Helper()
	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(_ context.Context, _ *spec.Config, ref string,
		_ CallIntent, _ *slog.Logger) (model.BaseModel[*schema.Message], error) {

		if m := byRef[ref]; m != nil {
			return m, nil
		}
		return fallback, nil
	}
	return b
}

func TestFilesystemToolsAreActuallyWired(t *testing.T) {
	m := &plainModel{}
	cfg := configFromSpecs("test/model", allTools(),
		allowOf(NewPolicy(nil)), nil)
	cfg.Agents[spec.DefaultAgentID].Instruction = "you are a test"
	cfg.Agents[spec.DefaultAgentID].MaxIterations = 2
	b := newModelBuilder(t, cfg, m, nil)

	a, _, err := b.Build(context.Background(), "main", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	runOnce(t, a)

	obs := b.RuntimeTools("main")
	if !obs.Known {
		t.Fatal("★ the turn never handed a tool table to the framework — nothing was observed")
	}
	for _, name := range []string{"read_file", "write_file", "edit_file", "ls", "glob", "grep", "execute"} {
		if !obs.Has(name) {
			t.Errorf("★ the %q tool is not in the real tool table: %v", name, obs.Names)
		}
	}
}

func TestFilesystemToolsSurviveTheAgentsRootSwitch(t *testing.T) {
	child := &toolCallingModel{tool: "write_file", args: `{"file_path":"/tmp/x"}`}
	cfg := configFromSpecs("test/model", allTools(), nil,
		[]decl.SubAgentSpec{{ID: "task", ModelRef: "child/model", Tools: allTools()}})
	cfg.Agents[spec.DefaultAgentID].Instruction = "you are a test"
	cfg.Agents[spec.DefaultAgentID].MaxIterations = 2
	b := newModelBuilder(t, cfg, &plainModel{}, map[string]model.BaseModel[*schema.Message]{
		"child/model": child,
	})

	a, _, err := b.Build(context.Background(), "main", "task", contract.TurnQuery, "")
	if err != nil {
		t.Fatalf("build the sub agent as root: %v", err)
	}

	if !runUntilInterrupt(t, a) {
		t.Fatal("★ the write_file call was not gated — the tool is probably not wired")
	}
}

func TestSubAgentsAlsoGetTheFilesystemTools(t *testing.T) {
	// ★ write_file, not read_file: the readers run without a nod, so only
	//   a mutating tool reaches the gate.
	child := &toolCallingModel{tool: "write_file", args: `{"file_path":"/tmp/x"}`}
	root := &toolCallingModel{tool: "task", args: `{"subagent_type":"task","description":"write it"}`}

	cfg := configFromSpecs("test/model", allTools(), nil,
		[]decl.SubAgentSpec{{ID: "task", ModelRef: "child/model", Tools: allTools()}})
	cfg.Agents[spec.DefaultAgentID].Instruction = "you are a test"
	cfg.Agents[spec.DefaultAgentID].MaxIterations = 4
	b := newModelBuilder(t, cfg, root, map[string]model.BaseModel[*schema.Message]{
		"child/model": child,
	})

	a, _, err := b.Build(context.Background(), "main", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	if !runUntilInterrupt(t, a) {
		t.Fatal("★ the sub agent's write_file was not gated — it probably has no filesystem tools")
	}
}

func runOnce(t *testing.T, a adk.TypedAgent[*schema.Message]) {
	t.Helper()
	iter := a.Run(context.Background(), &adk.TypedAgentInput[*schema.Message]{
		Messages: []*schema.Message{schema.UserMessage("hello")},
	})
	for {
		ev, ok := iter.Next()
		if !ok {
			return
		}
		if ev.Err != nil {
			t.Fatalf("run failed: %v", ev.Err)
		}
	}
}

func runUntilInterrupt(t *testing.T, a adk.TypedAgent[*schema.Message]) bool {
	t.Helper()
	interrupted := false
	iter := a.Run(context.Background(), &adk.TypedAgentInput[*schema.Message]{
		Messages: []*schema.Message{schema.UserMessage("hello")},
	})
	for {
		ev, ok := iter.Next()
		if !ok {
			return interrupted
		}
		if ev.Err != nil {
			t.Fatalf("run failed: %v", ev.Err)
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			interrupted = true
		}
	}
}

// ★ There is no global deny any more: the agent's own tools: list is
//
//	what takes a tool away from the model.
func TestTheToolListRemovesTheFilesystemToolsFromTheModel(t *testing.T) {
	m := &scriptModel{tool: "write_file"}
	cfg := configFromSpecs("test/model", allToolsExcept("write_file"), nil, nil)
	cfg.Agents[spec.DefaultAgentID].Instruction = "you are a test"
	cfg.Agents[spec.DefaultAgentID].MaxIterations = 3
	b := newModelBuilder(t, cfg, m, nil)

	a, _, err := b.Build(context.Background(), "main", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	runOnce(t, a)

	hist := m.history()
	if len(hist) < 2 {
		t.Fatalf("the model must have been called twice (call + retry), got %d turns", len(hist))
	}
	got := toolResultFor(hist, "")
	if !strings.Contains(got, "write_file") {
		t.Fatalf("the model must have been told that write_file is unavailable: %q", got)
	}
	if !strings.Contains(got, "read_file") {
		t.Fatalf("★ read_file must still be available — the file tools are in the table "+
			"and only write_file was denied; got: %q", got)
	}
}

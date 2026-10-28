package build

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"openheron/approval"
)

// 这个用例钉住“入口 agent → task 下属 → 审批工具 → resume”这条链路。
//
// ★ 它比单测中间件更靠近生产：TurnLoop 负责 checkpoint/resume，子 agent
//   由 task 工具包装，resume 时还会用**重新构造**的 agent 实例（模拟 Pool）。
//   只测 agent_tool 或只测 approvalMiddleware 都覆盖不到这条组合。

type nestedToolCallModel struct {
	mu   sync.Mutex
	call *schema.ToolCall
}

func (m *nestedToolCallModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range in {
		// 工具结果已经回灌：模型可以收尾。
		if msg.Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return schema.AssistantMessage("", []schema.ToolCall{*m.call}), nil
}

func (m *nestedToolCallModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *nestedToolCallModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type nestedWriteTool struct {
	mu    sync.Mutex
	calls int
}

func (t *nestedWriteTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "write_file",
		Desc: "write a file",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path": {Type: schema.String},
		}),
	}, nil
}

func (t *nestedWriteTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return "written", nil
}

type nestedStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (s *nestedStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	return v, ok, nil
}

func (s *nestedStore) Set(_ context.Context, id string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[id] = append([]byte(nil), v...)
	return nil
}

func (s *nestedStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

func buildNestedRoleAgents(t *testing.T) (adk.TypedAgent[*schema.Message], *nestedWriteTool) {
	t.Helper()
	ctx := context.Background()
	taskArgs, err := json.Marshal(map[string]any{
		"subagent_type": "task",
		"description":   "write the file",
	})
	if err != nil {
		t.Fatal(err)
	}

	writeTool := &nestedWriteTool{}
	inner, err := deep.New(ctx, &deep.Config{
		Name:        "task",
		Description: "executor",
		ChatModel: &nestedToolCallModel{call: &schema.ToolCall{
			ID: "call-write", Type: "function",
			Function: schema.FunctionCall{Name: "write_file", Arguments: `{"path":"/tmp/x"}`},
		}},
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{writeTool},
		}},
		Handlers:               []adk.TypedChatModelAgentMiddleware[*schema.Message]{newApprovalMiddleware(NewPolicy([]string{"write_file"}, nil))},
		MaxIteration:           4,
		WithoutGeneralSubAgent: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	outer, err := deep.New(ctx, &deep.Config{
		Name: "main",
		ChatModel: &nestedToolCallModel{call: &schema.ToolCall{
			ID: "call-task", Type: "function",
			Function: schema.FunctionCall{Name: "task", Arguments: string(taskArgs)},
		}},
		SubAgents:              []adk.TypedAgent[*schema.Message]{inner},
		MaxIteration:           4,
		WithoutGeneralSubAgent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return outer, writeTool
}

type nestedLoopItem struct {
	Query       string
	HasApproval bool
	Approved    bool
	InterruptID string
	// Payload 覆盖「塞进 Targets 的那个值」。nil ⇒ 用默认的
	// *approval.Decision。★ 它存在是为了能塞**类型不对**的值 ——
	// 那正是本文件要钉的那类缺陷（闸门读的类型与送的类型不是同一个）。
	Payload any
}

func TestNestedApprovalResumeThroughTurnLoop(t *testing.T) {
	ctx := context.Background()
	store := &nestedStore{}
	rootID := ""
	var eventErrs []error

	newLoop := func(agent adk.TypedAgent[*schema.Message]) *adk.TurnLoop[nestedLoopItem, *schema.Message] {
		return adk.NewTurnLoop(adk.TurnLoopConfig[nestedLoopItem, *schema.Message]{
			Store:        store,
			CheckpointID: "nested-approval",
			GenInput: func(_ context.Context, _ *adk.TurnLoop[nestedLoopItem, *schema.Message], items []nestedLoopItem) (*adk.GenInputResult[nestedLoopItem, *schema.Message], error) {
				for _, it := range items {
					if it.Query != "" {
						return &adk.GenInputResult[nestedLoopItem, *schema.Message]{
							Input:    &adk.TypedAgentInput[*schema.Message]{Messages: []*schema.Message{schema.UserMessage(it.Query)}},
							Consumed: []nestedLoopItem{it},
						}, nil
					}
				}
				return &adk.GenInputResult[nestedLoopItem, *schema.Message]{}, nil
			},
			PrepareAgent: func(context.Context, *adk.TurnLoop[nestedLoopItem, *schema.Message], []nestedLoopItem) (adk.TypedAgent[*schema.Message], error) {
				return agent, nil
			},
			OnAgentEvents: func(_ context.Context, tc *adk.TurnContext[nestedLoopItem, *schema.Message], events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) error {
				interrupted := false
				for {
					ev, ok := events.Next()
					if !ok {
						break
					}
					if ev.Err != nil {
						eventErrs = append(eventErrs, ev.Err)
					}
					if ev.Action != nil && ev.Action.Interrupted != nil {
						for _, ic := range ev.Action.Interrupted.InterruptContexts {
							if ic.IsRootCause {
								rootID = ic.ID
								interrupted = true
							}
						}
					}
				}
				if !interrupted {
					tc.Loop.Stop(adk.WithStopCause("done"))
				}
				return nil
			},
			GenResume: func(_ context.Context, _ *adk.TurnLoop[nestedLoopItem, *schema.Message], interrupted, unhandled, newItems []nestedLoopItem) (*adk.GenResumeResult[nestedLoopItem, *schema.Message], error) {
				var decision nestedLoopItem
				for _, it := range newItems {
					if it.HasApproval {
						decision = it
					}
				}
				payload := decision.Payload
				if payload == nil {
					payload = &approval.Decision{Approved: decision.Approved}
				}
				return &adk.GenResumeResult[nestedLoopItem, *schema.Message]{
					ResumeParams: &adk.ResumeParams{
						Targets: map[string]any{decision.InterruptID: payload},
					},
					Consumed:  interrupted,
					Remaining: unhandled,
				}, nil
			},
		})
	}

	outer, _ := buildNestedRoleAgents(t)
	loop := newLoop(outer)
	loop.Push(nestedLoopItem{Query: "delegate"})
	loop.Run(ctx)
	if exit := loop.Wait(); exit.ExitReason == nil {
		t.Fatal("first turn must stop at the nested approval")
	}
	if rootID == "" {
		t.Fatal("no root-cause interrupt from the task sub-agent")
	}

	// ★ 模拟生产：resume 时 PrepareAgent 会通过 Pool 重新取/建 agent。
	rebuilt, writeTool := buildNestedRoleAgents(t)
	loop2 := newLoop(rebuilt)
	loop2.Push(nestedLoopItem{HasApproval: true, Approved: true, InterruptID: rootID})
	loop2.Run(ctx)
	if exit := loop2.Wait(); exit.ExitReason != nil {
		t.Fatalf("resume failed: %v", exit.ExitReason)
	}
	if writeTool.calls != 1 {
		t.Fatalf("write_file calls = %d, want 1", writeTool.calls)
	}
}

// ★★ 「拒绝」必须真的拦住工具。
//
//	这条曾经是**红的**，而且红得没有声音：agentkit/run 送进 Targets 的是
//	`*agentkit/run.ApprovalResult`，闸门读的是 `*agent.ApprovalResult` ——
//	eino 的 GetResumeContext 是具体类型断言（internal/core/resume.go:102），
//	断言失败 ⇒ hasData=false ⇒ 而闸门把「拿不到决定」当成「照常执行」。
//
//	于是批准与拒绝的效果**完全一样**：工具都跑。而拒绝那句
//	「tool was rejected」模型根本没看到。
func TestApprovalGateRefusesTheToolWhenRejected(t *testing.T) {
	calls, exit := runGatedResume(t, nestedLoopItem{
		HasApproval: true,
		Approved:    false,
		Payload:     &approval.Decision{Approved: false, Reason: "not now"},
	})
	if calls != 0 {
		t.Fatalf("write_file ran %d times after a rejection — the gate let it through", calls)
	}
	// 拒绝不是「整轮崩掉」：它要以工具结果的形式回到模型，让模型改主意。
	if exit != nil {
		t.Fatalf("a rejection must not fail the turn, got %v", exit)
	}
}

// ★★ 类型对不上时**不许执行工具**（fail closed）。
//
//	这条钉的是「闸门读的类型」与「送的类型」必须同一个。它曾经不成立，
//	而失败的方式是**放行** —— 最坏的那种失败。
//
//	用的载荷是结构完全相同、但类型不同的另一个结构 —— 正是本仓库曾经
//	agentkit/run.ApprovalResult / agent.ApprovalResult 的关系。
type foreignDecision struct {
	Approved bool
	Reason   string
}

func TestApprovalGateRefusesTheToolWhenTheDecisionTypeIsWrong(t *testing.T) {
	calls, err := runGatedResume(t, nestedLoopItem{
		HasApproval: true,
		Payload:     &foreignDecision{Approved: true},
	})
	if calls != 0 {
		t.Fatalf("write_file ran %d times with an unreadable decision — "+
			"the gate must fail closed, not assume approval", calls)
	}
	// ★ 而且要**说出来**：静默拒绝会让人以为「审批生效了」，于是去查策略、
	//	查前端、查会话 —— 而真正的原因是载荷类型对不上。
	if err == nil {
		t.Fatal("an unreadable decision must be reported, not silently swallowed")
	}
}

// runGatedResume 跑一遍「先中断、再答复」的两轮 turn，返回工具被调用的次数
// 与第二轮的退出原因。
func runGatedResume(t *testing.T, reply nestedLoopItem) (int, error) {
	t.Helper()
	ctx := context.Background()
	store := &nestedStore{}
	rootID := ""
	var eventErrs []error

	newLoop := func(agent adk.TypedAgent[*schema.Message]) *adk.TurnLoop[nestedLoopItem, *schema.Message] {
		return adk.NewTurnLoop(adk.TurnLoopConfig[nestedLoopItem, *schema.Message]{
			Store:        store,
			CheckpointID: "nested-approval",
			GenInput: func(_ context.Context, _ *adk.TurnLoop[nestedLoopItem, *schema.Message], items []nestedLoopItem) (*adk.GenInputResult[nestedLoopItem, *schema.Message], error) {
				for _, it := range items {
					if it.Query != "" {
						return &adk.GenInputResult[nestedLoopItem, *schema.Message]{
							Input:    &adk.TypedAgentInput[*schema.Message]{Messages: []*schema.Message{schema.UserMessage(it.Query)}},
							Consumed: []nestedLoopItem{it},
						}, nil
					}
				}
				return &adk.GenInputResult[nestedLoopItem, *schema.Message]{}, nil
			},
			PrepareAgent: func(context.Context, *adk.TurnLoop[nestedLoopItem, *schema.Message], []nestedLoopItem) (adk.TypedAgent[*schema.Message], error) {
				return agent, nil
			},
			OnAgentEvents: func(_ context.Context, tc *adk.TurnContext[nestedLoopItem, *schema.Message], events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) error {
				interrupted := false
				for {
					ev, ok := events.Next()
					if !ok {
						break
					}
					if ev.Err != nil {
						eventErrs = append(eventErrs, ev.Err)
					}
					if ev.Action != nil && ev.Action.Interrupted != nil {
						for _, ic := range ev.Action.Interrupted.InterruptContexts {
							if ic.IsRootCause {
								rootID = ic.ID
								interrupted = true
							}
						}
					}
				}
				if !interrupted {
					tc.Loop.Stop(adk.WithStopCause("done"))
				}
				return nil
			},
			GenResume: func(_ context.Context, _ *adk.TurnLoop[nestedLoopItem, *schema.Message], interrupted, unhandled, newItems []nestedLoopItem) (*adk.GenResumeResult[nestedLoopItem, *schema.Message], error) {
				var decision nestedLoopItem
				for _, it := range newItems {
					if it.HasApproval {
						decision = it
					}
				}
				payload := decision.Payload
				if payload == nil {
					payload = &approval.Decision{Approved: decision.Approved}
				}
				return &adk.GenResumeResult[nestedLoopItem, *schema.Message]{
					ResumeParams: &adk.ResumeParams{
						Targets: map[string]any{decision.InterruptID: payload},
					},
					Consumed:  interrupted,
					Remaining: unhandled,
				}, nil
			},
		})
	}

	outer, _ := buildNestedRoleAgents(t)
	loop := newLoop(outer)
	loop.Push(nestedLoopItem{Query: "delegate"})
	loop.Run(ctx)
	if exit := loop.Wait(); exit.ExitReason == nil {
		t.Fatal("first turn must stop at the nested approval")
	}
	if rootID == "" {
		t.Fatal("no root-cause interrupt from the task sub-agent")
	}

	rebuilt, writeTool := buildNestedRoleAgents(t)
	loop2 := newLoop(rebuilt)
	reply.InterruptID = rootID
	loop2.Push(reply)
	loop2.Run(ctx)
	exit := loop2.Wait()
	if len(eventErrs) > 0 {
		return writeTool.calls, eventErrs[0]
	}
	return writeTool.calls, exit.ExitReason
}

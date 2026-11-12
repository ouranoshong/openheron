package run

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/approval"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// ---- the fixture ----
//
// ★ The round is interrupted, which is the case the pin exists for: the
//	report is written by the agent that started the work, and an agent
//	that was swapped in between cannot write it.
//
// ★ The fixture is a REAL agent, not a scripted one, and that is not
//	decoration. The framework writes a resumable checkpoint only when an
//	agent pauses for a decision, and it reads that pause off an
//	unexported field of the event — a fake agent that emits the event by
//	hand can never produce one. ★ So a fake agent makes the resume
//	unreachable, and the test would prove nothing about it.

// pinTool asks for approval, and writes once the approval is there.
type pinTool struct {
	mu    sync.Mutex
	calls int
}

func (t *pinTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "write_file",
		Desc: "write a file",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path": {Type: schema.String, Required: true},
		}),
	}, nil
}

func (t *pinTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()

	// ★ The first call pauses for a decision. This is what makes the round
	//	resumable: the interrupt is the framework's cue to write the
	//	checkpoint the resume reads back.
	interrupted, hasState, state := tool.GetInterruptState[string](ctx)
	if !interrupted {
		return "", tool.StatefulInterrupt(ctx, &approval.ApprovalInfo{
			ToolName:        "write_file",
			ArgumentsInJSON: args,
		}, args)
	}
	if !hasState {
		return "", errors.New("the interrupted call lost its state")
	}
	return "written " + state, nil
}

// callCount reports how often the tool ran.
func (t *pinTool) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// pinModel calls one tool, and answers once the tool came back.
type pinModel struct {
	call *schema.ToolCall
}

func (m *pinModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range in {
		if msg.Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
	}
	return schema.AssistantMessage("writing", []schema.ToolCall{*m.call}), nil
}

// Stream splits the answer into pieces, the way a real provider does.
//
// ★ The tool call rides on the last piece: a model that says "writing" and
//
//	then calls a tool streams exactly like that.
func (m *pinModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	head, tail := msg.Content[:len(msg.Content)-1], msg.Content[len(msg.Content)-1:]
	return schema.StreamReaderFromArray([]*schema.Message{
		{Role: schema.Assistant, Content: head},
		{Role: schema.Assistant, Content: tail, ToolCalls: msg.ToolCalls},
	}), nil
}

func (m *pinModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

// pinAgent builds the fixture: a root agent that delegates, and the
// sub-agent whose tool asks for approval.
//
// ★ It is the DeepAgent, because that is what openheron builds — the pin
//
//	has to be proven on the agent that actually runs in production, and
//	the plain ChatModelAgent is not it.
//
// ★ The approval comes from the SUB-agent, the way it comes in
//
//	production: `main` delegates, `task` writes. ★ That is not a detail:
//	a cancelled ROOT agent re-announces its interrupt, and the framework
//	writes that announcement into the loop's own state — a race inside
//	the framework, in code openheron cannot reach.
//
// ★ It returns a FACTORY, not an agent: every turn gets a fresh instance.
//
//	An interrupted agent's event stream is still open, and a second turn
//	that reuses the instance would feed the old stream as well.
func pinAgent(t *testing.T) (func() adk.TypedAgent[*schema.Message], *pinTool) {
	t.Helper()
	tl := &pinTool{}
	return func() adk.TypedAgent[*schema.Message] {
		inner, err := deep.New(context.Background(), &deep.Config{
			Name:        "task",
			Description: "the worker that writes",
			ChatModel: &pinModel{call: &schema.ToolCall{
				ID: "call-write", Type: "function",
				Function: schema.FunctionCall{Name: "write_file", Arguments: `{"path":"/tmp/x"}`},
			}},
			ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{tl},
			}},
			MaxIteration:           4,
			WithoutGeneralSubAgent: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		outer, err := deep.New(context.Background(), &deep.Config{
			Name:        "main",
			Description: "the fixture that delegates",
			ChatModel: &pinModel{call: &schema.ToolCall{
				ID: "call-task", Type: "function",
				Function: schema.FunctionCall{Name: "task", Arguments: `{"subagent_type":"task","description":"write the file"}`},
			}},
			SubAgents:              []adk.TypedAgent[*schema.Message]{inner},
			MaxIteration:           4,
			WithoutGeneralSubAgent: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return outer
	}, tl
}

// pinRuntime wires a runtime over the fixture.
func pinRuntime(t *testing.T, b *fakeBuilder) (*Runtime, *fakeSession) {
	t.Helper()
	sm := newFakeSessionManager()
	sm.ckpt = newFakeCheckpoints()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	return rt, mustSession(t, sm)
}

// interruptRound runs the first round until it asks for approval.
//
// ★ It hands the round's loop back: the caller keeps it alive, because a
//
//	loop the allocator may reuse is a loop a race report cannot point at.
func interruptRound(t *testing.T, rt *Runtime, sess *fakeSession) *adk.TurnLoop[loopItem, *schema.Message] {
	t.Helper()
	ctx := context.Background()
	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := rt.stateFor(sess.ID())
	st.mu.Lock()
	keep := st.loop
	st.mu.Unlock()
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 1
	})
	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State == storage.RunWaitingApproval
	})
	return keep
}

// approve answers the outstanding approval, and returns its id.
func approve(t *testing.T, sess *fakeSession) string {
	t.Helper()
	pending, err := sess.PendingApproval()
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.Interrupts) == 0 {
		t.Fatal("there is no approval to answer")
	}
	return pending.Interrupts[0].InterruptID
}

// TestAResumeRunsOnTheTreeTheRoundStartedWith is the pin's promise.
//
// ★ The first round runs on a tree, and the tree's fingerprint is written
//
//	into the run's row. ★ The resume asks for that fingerprint — so the
//	round continues on the tree it started with, and the report comes
//	back from the agent that began the work.
func TestAResumeRunsOnTheTreeTheRoundStartedWith(t *testing.T) {
	newAgent, tl := pinAgent(t)
	b := &fakeBuilder{
		agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] { return newAgent() },
		tree:  "tree-1",
	}
	rt, sess := pinRuntime(t, b)
	ctx := context.Background()

	firstLoop := interruptRound(t, rt, sess)

	// ★ The row says which tree the round ran on.
	runs := ledgerOf(t, sess)
	if runs[0].TreeFingerprint != "tree-1" {
		t.Fatalf("the row must record the tree, got %q", runs[0].TreeFingerprint)
	}

	id := approve(t, sess)
	resume, err := rt.Resume(ctx, sess.ID(), "",
		[]ApprovalDecision{{InterruptID: id, Approved: true}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.pinsSeen()) == 2 })

	// ★ The resume asked for the tree the round started with.
	pins := b.pinsSeen()
	if pins[0] != "" {
		t.Fatalf("a fresh query asks for the current tree, got %q", pins[0])
	}
	if pins[1] != "tree-1" {
		t.Fatalf("★ the resume must ask for the tree the round started with, got %q", pins[1])
	}

	// ★ And the round finished: the tool ran again, this time for real.
	waitFor(t, func() bool { return tl.callCount() == 2 })

	// ★ And the resume's own row says so too.
	waitFor(t, func() bool { return len(ledgerOf(t, sess)) == 2 })
	runs = ledgerOf(t, sess)
	if runs[1].TreeFingerprint != "tree-1" {
		t.Fatalf("the resume's row must record the tree, got %q", runs[1].TreeFingerprint)
	}
	resume.WaitReleased()
	runtime.KeepAlive(firstLoop)
}

// TestTheNextTurnRunsOnTheNewTree is the other half of the pin.
//
// ★ The pin belongs to the round that was interrupted — not to the session
//
//	and not to the next question. ★ A fresh query after the change runs on
//	the new tree.
func TestTheNextTurnRunsOnTheNewTree(t *testing.T) {
	newAgent, _ := pinAgent(t)
	b := &fakeBuilder{
		agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] { return newAgent() },
		tree:  "tree-1",
	}
	rt, sess := pinRuntime(t, b)
	ctx := context.Background()

	firstLoop := interruptRound(t, rt, sess)

	// ★ The configuration changed: the tree moved.
	b.mu.Lock()
	b.tree = "tree-2"
	b.mu.Unlock()

	id := approve(t, sess)
	resume, err := rt.Resume(ctx, sess.ID(), "",
		[]ApprovalDecision{{InterruptID: id, Approved: true}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.pinsSeen()) == 2 })
	waitFor(t, func() bool { return len(ledgerOf(t, sess)) == 2 })

	// ★ The round has to be over before the next question: a live turn
	//	holds the session, and the gate refuses a query behind it.
	resume.WaitReleased()

	// ★ The next question asks for the current tree.
	if _, err := rt.Submit(ctx, sess.ID(), "", "another question", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.pinsSeen()) == 3 })
	pins := b.pinsSeen()
	if pins[2] != "" {
		t.Fatalf("★ a fresh query must ask for the current tree, got %q", pins[2])
	}
	waitFor(t, func() bool { return len(ledgerOf(t, sess)) == 3 })
	runs := ledgerOf(t, sess)
	if runs[2].TreeFingerprint != "tree-2" {
		t.Fatalf("★ the next turn must run on the new tree, got %q", runs[2].TreeFingerprint)
	}
	runtime.KeepAlive(firstLoop)
}

// TestARunWithoutATreeRecordsNone is the honest empty case.
//
// ★ A builder that cannot name its tree leaves the field empty, and a
//
//	resume that finds no tree runs on the current one — which is the old
//	behaviour, and the best that can be done without a tree to hold on to.
func TestARunWithoutATreeRecordsNone(t *testing.T) {
	newAgent, _ := pinAgent(t)
	b := &fakeBuilder{
		agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] { return newAgent() },
	}
	rt, sess := pinRuntime(t, b)
	ctx := context.Background()

	firstLoop := interruptRound(t, rt, sess)

	runs := ledgerOf(t, sess)
	if runs[0].TreeFingerprint != "" {
		t.Fatalf("a run with no tree must record none, got %q", runs[0].TreeFingerprint)
	}

	id := approve(t, sess)
	if _, err := rt.Resume(ctx, sess.ID(), "",
		[]ApprovalDecision{{InterruptID: id, Approved: true}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.pinsSeen()) == 2 })

	pins := b.pinsSeen()
	if pins[1] != "" {
		t.Fatalf("a run with no tree pins nothing, got %q", pins[1])
	}
	runtime.KeepAlive(firstLoop)
}

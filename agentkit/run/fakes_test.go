package run

import (
	"context"
	"errors"
	"fmt"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// ---- in-memory storage.SessionManager for Runtime tests ----

type fakeSessionManager struct {
	mu   sync.Mutex
	byID map[string]*fakeSession
	ckpt *fakeCheckpoints
}

func newFakeSessionManager() *fakeSessionManager {
	return &fakeSessionManager{byID: make(map[string]*fakeSession)}
}

func (m *fakeSessionManager) Create(_ context.Context, agentID string) (storage.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &fakeSession{id: fmt.Sprintf("session-%d", len(m.byID)+1), agentID: agentID, ckpt: m.ckpt}
	m.byID[s.id] = s
	return s, nil
}

func (m *fakeSessionManager) Get(_ context.Context, id string) (storage.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	return s, nil
}

func (m *fakeSessionManager) List(context.Context) ([]storage.Meta, error) { return nil, nil }

func (m *fakeSessionManager) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byID, id)
	return nil
}

func (m *fakeSessionManager) Inspect(context.Context) (storage.Report, error) {
	return storage.Report{}, nil
}

type fakeSession struct {
	mu       sync.Mutex
	id       string
	messages []*schema.Message
	cursor   int
	pending  *storage.PendingApproval
	title    string
	// setCursorOnAppend emulates a render advancing the view cursor.
	setCursorOnAppend bool
	agentID           string
	decisions         []storage.ApprovalRecord
	ckpt              *fakeCheckpoints
	ledger            *fakeRunLedger
}

func (s *fakeSession) ID() string           { return s.id }
func (s *fakeSession) CreatedAt() time.Time { return time.Unix(0, 0).UTC() }

func (s *fakeSession) Append(msg *schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	if s.setCursorOnAppend {
		s.cursor++
	}
	return nil
}

func (s *fakeSession) Messages(effective bool) []*schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*schema.Message, len(s.messages))
	copy(out, s.messages)
	if effective {
		out = message.EffectiveView(out)
	}
	return out
}

func (s *fakeSession) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

func (s *fakeSession) Cursor() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

func (s *fakeSession) SetCursor(cursor int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cursor > s.cursor {
		s.cursor = cursor
	}
	return nil
}

func (s *fakeSession) SetPendingApproval(p *storage.PendingApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = p
	return nil
}

func (s *fakeSession) PendingApproval() (*storage.PendingApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return nil, nil
	}
	c := *s.pending
	return &c, nil
}

func (s *fakeSession) TakePendingApproval() (*storage.PendingApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	s.pending = nil
	return p, nil
}

func (s *fakeSession) ClearPendingApproval() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	return nil
}

func (s *fakeSession) Title() string { s.mu.Lock(); defer s.mu.Unlock(); return s.title }

func (s *fakeSession) RecordDecision(recs ...storage.ApprovalRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, recs...)
	return nil
}

func (s *fakeSession) Decisions() []storage.ApprovalRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.ApprovalRecord(nil), s.decisions...)
}

func (s *fakeSession) AgentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentID
}

func (s *fakeSession) SetAgentID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentID = id
	return nil
}

func (s *fakeSession) SetTitle(t string) error {
	if t == "" {
		return errors.New("title must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.title = t
	return nil
}

// Init lays down a fake session (the real one writes files; this one does not).
func (s *fakeSession) Init(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentID = agentID
	return nil
}

// Delete is a no-op for the fake: there are no files to remove.
func (s *fakeSession) Delete() error { return nil }

// Meta is the fake session's metadata.
func (s *fakeSession) Meta() storage.Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return storage.Meta{
		ID:           s.id,
		Title:        s.title,
		CreatedAt:    s.CreatedAt(),
		MessageCount: len(s.messages),
	}
}

// HasPending reports whether the fake session owes a decision.
func (s *fakeSession) HasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending != nil && len(s.pending.Interrupts) > 0
}

// HasCheckpoint asks the fake store whether a checkpoint exists.
func (s *fakeSession) HasCheckpoint(ctx context.Context) (bool, error) {
	if s.ckpt == nil {
		return false, nil
	}
	_, found, err := s.ckpt.Get(ctx, s.id)
	return found, err
}

// GetCheckPointStore returns the repository's checkpoint store.
func (s *fakeSession) GetCheckPointStore() (adk.CheckPointStore, adk.CheckPointDeleter) {
	if s.ckpt == nil {
		return nil, nil
	}
	return s.ckpt, s.ckpt
}

// RunLedger returns the fake session's in-memory run ledger.
//
// ★ The ledger is not optional in the fake: the runtime writes every run's
//
//	row through it, and a fake without one would exercise a different
//	code path than production.
func (s *fakeSession) RunLedger() storage.RunLedger {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ledger == nil {
		s.ledger = &fakeRunLedger{}
	}
	return s.ledger
}

// fakeRunLedger is the in-memory ledger of a fake session.
type fakeRunLedger struct {
	mu    sync.Mutex
	rows  []storage.Run
	epoch uint64
}

func (l *fakeRunLedger) Append(_ context.Context, r storage.Run) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, r)
	return nil
}

func (l *fakeRunLedger) List(_ context.Context) ([]storage.Run, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return storage.FoldRuns(append([]storage.Run(nil), l.rows...)), nil
}

func (l *fakeRunLedger) NextEpoch(_ context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.epoch++
	return l.epoch, nil
}

func (l *fakeRunLedger) Reconcile(_ context.Context) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	closed := 0
	for i := range l.rows {
		if l.rows[i].State.IsTerminal() {
			continue
		}
		l.rows[i].State = storage.RunInterrupted
		closed++
	}
	return closed, nil
}

func (s *fakeSession) Workspace() string                         { return "/tmp" }
func (s *fakeSession) PutFile(string, io.Reader) (string, error) { return "", nil }

// ---- fake agent ----

// fakeAgent emits a scripted sequence of events and then exits.
type fakeAgent struct {
	script func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool)
}

func (a *fakeAgent) Name(context.Context) string        { return "fake" }
func (a *fakeAgent) Description(context.Context) string { return "fake" }

func (a *fakeAgent) Run(ctx context.Context, _ *adk.TypedAgentInput[*schema.Message], _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	go func() {
		defer gen.Close()
		if a.script == nil {
			return
		}
		a.script(ctx, func(e *adk.TypedAgentEvent[*schema.Message]) bool {
			if ctx.Err() != nil {
				return false
			}
			gen.Send(e)
			return true
		})
	}()
	return iter
}

// textEvent builds an assistant message event.
func textEvent(text string) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role:    schema.Assistant,
				Message: message.NewAssistant(text, nil),
			},
		},
	}
}

func exitEvent() *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Action: &adk.AgentAction{Exit: true},
	}
}

// interruptedEvent builds an event that pauses the turn for approval.
func interruptedEvent(id, desc string) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Action: &adk.AgentAction{
			Interrupted: &adk.InterruptInfo{
				InterruptContexts: []*adk.InterruptCtx{
					{ID: id, IsRootCause: true, Info: desc},
				},
			},
		},
	}
}

func interruptedEvents(ids []string) *adk.TypedAgentEvent[*schema.Message] {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		Action: &adk.AgentAction{
			Interrupted: &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{}},
		},
	}
	for _, id := range ids {
		ev.Action.Interrupted.InterruptContexts = append(
			ev.Action.Interrupted.InterruptContexts,
			&adk.InterruptCtx{ID: id, IsRootCause: true, Info: id + " needs approval"},
		)
	}
	return ev
}

// errorEvent builds an event that carries the agent's failure.
func errorEvent(err error) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{Err: err}
}

// compactionEvent builds the event the summarization middleware raises when
// it replaces the history.
func compactionEvent(msgs ...*schema.Message) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Action: &adk.AgentAction{
			CustomizedAction: &summarization.TypedCustomizedAction[*schema.Message]{
				Type: summarization.ActionTypeAfterSummarize,
				After: &summarization.TypedAfterSummarizeAction[*schema.Message]{
					Messages: msgs,
				},
			},
		},
	}
}

// fakeBuilder hands out a scripted agent per turn.
type fakeBuilder struct {
	mu       sync.Mutex
	builds   int
	agentIDs []string
	agent    func(call int, kind contract.TurnKind) adk.TypedAgent[*schema.Message]
	err      error
}

// Contexts reports what each agent's context allows.
//
// ★ The fake has no models, so the windows are empty. The derivation asks
//
//	for them to turn a token count into a ratio, and an empty map means
//	"no window configured" — which is what a model-less agent has.
func (b *fakeBuilder) Contexts() map[string]contract.AgentContext {
	return nil
}

func (b *fakeBuilder) Build(_ context.Context, _ string, agentID string, kind contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {
	b.mu.Lock()
	b.builds++
	n := b.builds
	b.agentIDs = append(b.agentIDs, agentID)
	agent := b.agent
	b.mu.Unlock()

	if b.err != nil {
		return nil, b.err
	}
	if agent == nil {
		return &fakeAgent{}, nil
	}
	return agent(n, kind), nil
}

// drain consumes a turn's events to completion and reports the outcome.
// drain waits for the run to finish.
//
// ★ The run consumes its own events now, so the test drains nothing: it
//
//	waits for the run's archive. What the turn produced comes from the
//	agent's events, exactly as it does in production.
func drain(t *testing.T, turn *Turn) {
	t.Helper()
	turn.WaitArchived()
}

var errBoom = errors.New("boom")

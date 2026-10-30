package run

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/emit"
	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// newLoop builds the TurnLoop for one session.
// There is no withResume parameter: whether a run resumes or starts fresh is
// decided by the checkpoint, which the framework reads itself.
func (rt *Runtime) newLoop(ctx context.Context, sess storage.Session, turn *Turn) *adk.TurnLoop[loopItem, *schema.Message] {
	cfg := adk.TurnLoopConfig[loopItem, *schema.Message]{
		GenInput:      rt.makeGenInput(sess, turn),
		PrepareAgent:  rt.makePrepareAgent(sess),
		OnAgentEvents: rt.makeOnAgentEvents(sess, turn),
	}
	if store, _ := sess.GetCheckPointStore(); store != nil {
		cfg.Store = store
		cfg.CheckpointID = sess.ID()
		cfg.GenResume = rt.makeGenResume(sess)
	}
	return adk.NewTurnLoop(cfg)
}

// makeGenInput assembles one turn's agent input and persists the user message.
// Persisting here is safe because GenInput only fires once the previous turn's
// OnAgentEvents has finished archiving. That ordering is the sole guarantee that
// history stays ordered under preemption.
func (rt *Runtime) makeGenInput(sess storage.Session, turn *Turn) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem) (*adk.GenInputResult[loopItem, *schema.Message], error) {
	return func(ctx context.Context, loop *adk.TurnLoop[loopItem, *schema.Message], items []loopItem) (*adk.GenInputResult[loopItem, *schema.Message], error) {
		var (
			consumed  []loopItem
			remaining []loopItem
			queryItem *loopItem
		)
		for i := range items {
			if queryItem == nil && items[i].Query != "" {
				queryItem = &items[i]
				consumed = append(consumed, items[i])
			} else {
				remaining = append(remaining, items[i])
			}
		}

		if queryItem == nil {
			turn.fail(ErrNoCheckpoint)
			loop.Stop(adk.WithStopCause("no query items"))
			return &adk.GenInputResult[loopItem, *schema.Message]{
				Input:     &adk.TypedAgentInput[*schema.Message]{Messages: []*schema.Message{message.NewUser("done")}},
				Remaining: items,
			}, nil
		}

		userMsg := message.NewUser(queryItem.Query)
		message.StampNow(userMsg)
		if err := sess.Append(userMsg); err != nil {
			rt.logf("session=%s failed to persist user message: %v", sess.ID(), err)
		}

		built, err := rt.cfg.Input.buildInput(ctx, inputParams{
			History:   sess.Messages(true),
			Workspace: sess.Workspace(),
		}, queryItem.Query)
		if err != nil {
			return nil, fmt.Errorf("assemble turn input: %w", err)
		}

		return &adk.GenInputResult[loopItem, *schema.Message]{
			Input:     built,
			Consumed:  consumed,
			Remaining: remaining,
		}, nil
	}
}

// makePrepareAgent asks the injected builder for this turn's agent. The
// Runtime never imports the agent package, so tests can inject a fake.
func (rt *Runtime) makePrepareAgent(sess storage.Session) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem) (adk.TypedAgent[*schema.Message], error) {
	return func(ctx context.Context, _ *adk.TurnLoop[loopItem, *schema.Message], _ []loopItem) (adk.TypedAgent[*schema.Message], error) {
		if rt.cfg.AgentBuilder == nil {
			return nil, errors.New("openheronrt: no AgentBuilder configured")
		}
		return rt.cfg.AgentBuilder.Build(ctx, sess.ID(), sess.AgentID(), contract.TurnQuery)
	}
}

// makeOnAgentEvents is the bridge between the TurnLoop and the run.
//
// ★ The run consumes its own events. The connection is a subscriber of what
//
//	the run publishes, not the consumer of the agent's stream — so a
//	connection that dies cannot stop the archive.
//
// ★ Nobody else may read the stream: the derivation folds the model's
//
//	chunks into one message, and a second reader would split them. That
//	single-reader rule is why this used to be the transport's job, and
//	why moving it here is what unbinds the run from the connection.
func (rt *Runtime) makeOnAgentEvents(sess storage.Session, turn *Turn) func(context.Context, *adk.TurnContext[loopItem, *schema.Message], *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) error {
	return func(ctx context.Context, _ *adk.TurnContext[loopItem, *schema.Message], events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) error {
		// ★ The agent has started producing events: the run is executing,
		//   not just claimed. The row says so before anything is archived.
		turn.row.set(storage.RunRunning)

		turn.events = events
		turn.signalReady()

		artifacts, err := emit.Derive(events, emit.Options{
			RunID:     turn.RunID,
			RootAgent: sess.AgentID(),
			Contexts:  rt.contexts(),
		}, turn.publisher, turn.abort)

		out := Outcome{
			Intermediates: artifacts.Intermediates,
			Interrupts:    artifacts.Pending,
			Usage:         artifacts.Usage,
			Compaction:    artifacts.Compaction,
			Err:           err,
		}
		turn.holdPending(artifacts.Pending)

		rt.archive(sess, turn, out)
		// ★ The messages are on disk: the turn is finished with the
		//   session's history, and the client may go on.
		turn.markArchived()
		rt.finishRun(turn, out)
		// ★ The publisher closes when the RUN is over, not when the turn's
		//   events end. A run that ended by raising an interrupt is not
		//   over until the approval is on disk: the client learns about
		//   the interrupt from the closing channel, and a client that
		//   answers an approval the server has not persisted yet is
		//   refused for no reason the user can see. That run's publisher
		//   closes at the loop's exit instead.
		if !turn.awaitingApproval {
			turn.publisher.Close()
		}

		return rt.finalize(sess, turn, out)
	}
}

// finishRun writes the run's terminal row.
//
// ★ It runs AFTER the archive. That single ordering is what makes the row
//
//	a barrier: a scan that sees "completed" knows the messages are on disk,
//	so the busy flag can be cleared without waiting for anything.
//
// ★ A run that ended by raising an interrupt is the one exception: its
//
//	row waits for the loop's exit, which is where the approval and the
//	checkpoint are written. A scan must not be able to call the run over
//	before the question the user has to answer is on disk.
func (rt *Runtime) finishRun(turn *Turn, out Outcome) {
	if turn.row == nil {
		return
	}
	switch {
	case out.Err != nil:
		// ★ A cancelled run is "aborted", not "failed": the user stopped
		//   it. The distinction is the user's, and the row is what a
		//   post-mortem reads.
		state, code := failedState(out.Err)
		turn.row.finish(state, code, out.Err.Error())
	case len(out.Interrupts) > 0:
		turn.awaitingApproval = true
	default:
		turn.row.finish(storage.RunCompleted, "", "")
	}
}

func (rt *Runtime) finalize(sess storage.Session, turn *Turn, out Outcome) error {
	// ★ A cancelled turn is not a session error. The user stopped it, or
	//   the run was aborted — putting "context canceled" in front of the
	//   user as the session's last failure would be a lie about the
	//   session. The run's own row still says "aborted".
	if out.Err != nil && !errors.Is(out.Err, context.Canceled) {
		rt.stateFor(sess.ID()).markError(out.Err)
	}
	//   the session state made /v2/sessions/{id} keep reporting "running"
	//   forever after a completed turn — the loop stays alive waiting for
	//   the next item, so "a loop exists" says nothing about activity.
	rt.stateFor(sess.ID()).markTurnDone(turn)

	if out.Err != nil {
		return out.Err
	}
	if len(out.Interrupts) > 0 {
		// Stopping the loop is what makes the checkpoint survive: the
		// framework only writes one when the stop is committed.
		rt.stateFor(sess.ID()).markError(errInterrupted)
		return errInterrupted
	}
	return nil
}

// archive persists the turn's intermediates and, on an interrupt, the pending
// approval.
func (rt *Runtime) archive(sess storage.Session, turn *Turn, out Outcome) {
	rt.archiveCompaction(sess, out)

	for _, msg := range out.Intermediates {
		if err := sess.Append(msg); err != nil {
			rt.logf("session=%s failed to archive intermediate message: %v", sess.ID(), err)
		}
	}

	if len(out.Interrupts) == 0 {
		return
	}

	// ★ The approval is HELD, not written. The checkpoint it would resume
	//   from is written by the framework when the loop stops — after this
	//   callback returns. Writing the approval now would leave a window
	//   where the user sees the card but answering it fails with
	//   no_checkpoint. The approval is written by the loop's exit, once the
	//   checkpoint is on disk.
	turn.holdPending(out.Interrupts)
}

// archiveCompaction writes the compacted message to the transcript.
//
// It goes first, ahead of the turn's own messages: the compaction summarizes
// everything that came before it, and the turn's messages came after the
// compaction happened. Writing it later would put the summary at the end of
// the conversation, where it summarizes nothing.
//
// The boundary is the transcript length at this moment — every message before
// the compacted one is superseded. Those messages stay on disk: compaction is
// not deletion.
func (rt *Runtime) archiveCompaction(sess storage.Session, out Outcome) {
	if out.Compaction == nil {
		return
	}
	msg := out.Compaction
	message.StampCompaction(msg, message.Compaction{
		Superseded:  sess.Len(),
		SavedTokens: savedTokens(sess, msg),
		At:          time.Now().UTC(),
	})
	message.StampNow(msg)
	if agent := sess.AgentID(); agent != "" {
		message.StampAgent(msg, agent)
	}
	if err := sess.Append(msg); err != nil {
		rt.logf("session=%s failed to persist the compacted message: %v", sess.ID(), err)
	}
}

// savedTokens estimates how many tokens the compaction removed from the next
// request.
//
// The baseline is the one eino's own counter uses — the total tokens of the
// last assistant message that carried usage — and the summary's size is
// estimated at four characters per token, which is eino's rule. Both numbers
// are estimates; the point is the order of magnitude, not an exact count.
func savedTokens(sess storage.Session, summary *schema.Message) int {
	baseline := baselineTokens(sess)
	summaryTokens := len(message.Text(summary)) / 4
	saved := baseline - summaryTokens
	if saved < 0 {
		return 0
	}
	return saved
}

// makeGenResume delivers the approval decisions.
// openheron does not decide whether a resume is possible — the framework reads the
// checkpoint and judges that itself. The one thing the framework cannot supply
// is the interrupt id, which lives inside a gob payload openheron cannot decode.
func (rt *Runtime) makeGenResume(sess storage.Session) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem, []loopItem, []loopItem) (*adk.GenResumeResult[loopItem, *schema.Message], error) {
	return func(_ context.Context, _ *adk.TurnLoop[loopItem, *schema.Message], canceledItems, unhandledItems, newItems []loopItem) (*adk.GenResumeResult[loopItem, *schema.Message], error) {
		targets := map[string]any{}
		for i := range newItems {
			for id, res := range newItems[i].Approvals {
				if id == "" || res == nil {
					continue
				}
				targets[id] = res
			}
		}
		if len(targets) == 0 {
			return nil, ErrNoPendingInterrupt
		}

		return &adk.GenResumeResult[loopItem, *schema.Message]{
			ResumeParams: &adk.ResumeParams{Targets: targets},
			Consumed:     canceledItems,
			Remaining:    unhandledItems,
		}, nil
	}
}

func (rt *Runtime) logf(format string, args ...any) {
	logf(format, args...)
}

// logf writes a warning through the one log outlet.
//
// ★ It is a package-level function because not every caller has a Runtime:
//
//	the run row is written by the turn handle, which does not.
func logf(format string, args ...any) {
	obs.Logger().Warn("[openheronrt] " + fmt.Sprintf(format, args...))
}

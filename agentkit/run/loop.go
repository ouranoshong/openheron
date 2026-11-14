package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

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
		PrepareAgent:  rt.makePrepareAgent(sess, turn),
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
		message.StampMessageID(userMsg, uuid.NewString())
		// ★ The attachment list belongs to the message, not to the session:
		//   only a message-level binding can answer "that image you just
		//   sent".
		message.StampAttachments(userMsg, queryItem.Attachments)

		// ★ Build first, persist second. The stamps are put on the message
		//   before it is handed to the assembly, and the copy the model sees
		//   shares its Extra map, so the id and the attachment list reach the
		//   model too. A build that fails leaves no half-written question
		//   behind: the transcript only learns about the turn once it starts.
		built, err := rt.cfg.Input.buildInput(ctx, inputParams{
			History:   sess.Messages(true),
			Workspace: sess.Workspace(),
		}, userMsg)
		if err != nil {
			// ★ An error the framework records but nobody hands to the turn
			//   is an error the client never sees: it would surface as
			//   "turn start timeout", which names neither the cause nor the
			//   file. Attachments are checked earlier (resolveAttachments),
			//   but the input still owns an error channel for anything the
			//   context assembly may legitimately fail on.
			fail := fmt.Errorf("assemble turn input: %w", err)
			turn.fail(fail)
			return nil, fail
		}

		if err := sess.Append(userMsg); err != nil {
			// ★ A failed write does not cost the model the question: the
			//   input is already assembled and carries it. The transcript
			//   loses the record, and the turn still answers.
			rt.logf("session=%s failed to persist user message: %v", sess.ID(), err)
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
//
// ★ **A build failure has to fail the turn.** The builder is where a
//
//	configuration problem becomes a runtime one — an agent whose model is
//	not declared, a skill that cannot be read back, a provider file that
//	does not parse. The framework records the error and stops the loop, but
//	nothing ever marked the turn failed: `Wait` sat until the start
//	timeout, and the client was told "turn start timeout" — a symptom that
//	names neither the file nor the reason.
//
//	So the error is handed to the turn here, and the turn's failure is what
//	the client sees: the reason, in the builder's own words.
func (rt *Runtime) makePrepareAgent(sess storage.Session, turn *Turn) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem) (adk.TypedAgent[*schema.Message], error) {
	return func(ctx context.Context, _ *adk.TurnLoop[loopItem, *schema.Message], _ []loopItem) (adk.TypedAgent[*schema.Message], error) {
		if rt.cfg.AgentBuilder == nil {
			err := errors.New("openheronrt: no AgentBuilder configured")
			turn.fail(agentUnavailable{reason: err})
			return nil, err
		}
		agent, fingerprint, err := rt.cfg.AgentBuilder.Build(ctx, sess.ID(), sess.AgentID(),
			contract.TurnQuery, turn.pin)
		if err != nil {
			// ★ The reason is wrapped, not replaced: the client's message is
			//   the builder's, and the sentinel is what lets the transport
			//   tell "your configuration is broken" from "something went
			//   wrong inside".
			turn.fail(agentUnavailable{reason: err})
			return nil, err
		}
		// ★ The row records the tree the run ran on. ★ It is the only place
		//   that tree is written down, and it is what a resume asks for:
		//   the round it continues has to run on the same tree, or the
		//   report is written by an agent that is not the one that
		//   started the work.
		turn.runRow().noteTree(fingerprint)
		return agent, nil
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
		turn.runRow().set(storage.RunRunning)

		turn.events = events
		turn.signalReady()

		// ★ The drain channel is the run's word that the agent is done:
		//   the derivation closes it when the agent's stream has closed.
		drained := make(chan struct{})

		artifacts, err := emit.Derive(events, emit.Options{
			RunID:     turn.RunID,
			RootAgent: sess.AgentID(),
			Contexts:  rt.contexts(),
			Drained:   drained,
		}, turn.publisher, turn.abort)

		// ★ The agent may still be winding down: the framework reads the
		//   agent's state when the loop stops, and the agent's stream is
		//   what writes it. ★ So a round that ended waits for the stream
		//   to close — otherwise the framework reads a field the agent is
		//   still writing.
		//
		// ★ A round that ENDED is the case: the agent has said what it
		//   had to say, and the stream closes right behind it. ★ A round
		//   that FAILED is not: the agent may still be running, and the
		//   framework's teardown is what stops it.
		if err == nil {
			rt.settleAgent(turn, drained)
		}

		out := Outcome{
			Intermediates: artifacts.Intermediates,
			Interrupts:    artifacts.Pending,
			Usage:         artifacts.Usage,
			Compaction:    artifacts.Compaction,
			Err:           err,
		}
		turn.holdPending(artifacts.Pending)

		// ★ The ledger names the servers this run touched, so a
		//   post-mortem can answer "which server was this" without
		//   reading the whole transcript.
		turn.runRow().noteMCPServers(mcpServersOf(artifacts.Intermediates))

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

// agentSettleTimeout bounds the wait for the agent's event stream to close.
//
// ★ The wait is short in practice — the agent's stream closes right
//
//	behind its last event — and it is bounded because an agent that
//	never closes would otherwise hold the round for ever.
const agentSettleTimeout = time.Second

// settleAgent waits for the agent's event stream to close.
//
// ★ The framework reads the agent's state when the loop stops, and the
//
//	agent's stream is what writes it. ★ Ending the round while the agent
//	is still winding down leaves the framework reading a field the agent
//	is still writing — a data race inside the framework.
//
// ★ The wait does not cancel anything. ★ The turn's context is what the
//
//	framework writes the checkpoint with, and a cancelled context would
//	take the checkpoint with it — and the checkpoint is what makes the
//	approval answerable.
func (rt *Runtime) settleAgent(turn *Turn, drained <-chan struct{}) {
	if drained == nil {
		return
	}
	select {
	case <-drained:
		return
	case <-time.After(agentSettleTimeout):
		rt.logf("session=%s the agent's stream did not close after the round ended", turn.sessionID)
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
	row := turn.runRow()
	if row == nil {
		return
	}
	switch {
	case out.Err != nil:
		// ★ A cancelled run is "aborted", not "failed": the user stopped
		//   it. The distinction is the user's, and the row is what a
		//   post-mortem reads.
		state, code := failedState(out.Err)
		row.finish(state, code, out.Err.Error())
	case len(out.Interrupts) > 0:
		turn.awaitingApproval = true
	default:
		row.finish(storage.RunCompleted, "", "")
	}
}

func (rt *Runtime) finalize(sess storage.Session, turn *Turn, out Outcome) error {
	// ★ The run is over, so it is not holding the session any more.
	turn.lease.release()

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
		// ★ Every archived message carries its own id, so the client can key
		//   it no matter what the transcript looks like later. A message
		//   that already has one keeps it — the stream may have stamped it
		//   before it reached here.
		message.StampMessageID(msg, uuid.NewString())
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
	// ★ The boundary is the one message that lands in the **middle** of the
	//   transcript, so it is the one that renumbers its neighbours. It needs
	//   an id of its own for the same reason every other message does.
	message.StampMessageID(msg, uuid.NewString())
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
	logf(rt.log, format, args...)
}

// logf writes a warning through the run's logger.
//
// ★ It takes the logger explicitly because not every caller has a Runtime:
//
//	the run row is written by the turn handle, which carries the logger
//	the Runtime handed it. A nil logger drops the line.
func logf(log *slog.Logger, format string, args ...any) {
	if log == nil {
		return
	}
	log.Warn("[openheronrt] " + fmt.Sprintf(format, args...))
}

var discard = obs.Discard()

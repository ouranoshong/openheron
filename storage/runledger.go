package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ouranoshong/openheron/contract"
)

// ErrFenceStale signals a write from a holder that has been fenced out.
//
// ★ The fence is the epoch. A run whose epoch is below the session's current
//
//	epoch has been taken over by a newer run and must not record
//	anything. The check lives in the READER as well (see [ActiveRun]), so a
//	stale row is harmless even when its writer never learns it is stale.
var ErrFenceStale = errors.New("the run's fence is stale")

// RunState is where a run is in its lifecycle.
//
// ★ The states are the run's own facts. "The session is busy" is NOT one of
//
//	them: a busy session has two possible causes — a live run and a pending
//	approval — and they are different locks with different lifetimes. Folding
//	them into one state would make the ledger unable to say which one holds.
type RunState uint8

const (
	// RunStarting is claimed but not yet executing.
	RunStarting RunState = iota
	// RunRunning is executing.
	RunRunning
	// RunWaitingApproval ended by raising an interrupt.
	RunWaitingApproval
	// RunCompleted finished normally.
	RunCompleted
	// RunFailed finished with an error.
	RunFailed
	// RunAborted was stopped by the user.
	RunAborted
	// RunInterrupted lost its owner mid-run and was reconciled.
	RunInterrupted
)

// String returns the wire name of the state.
//
// ★ The names are the ones the API publishes. They are part of the contract
//
//	with the client, so they are spelled out here rather than derived from
//	the constant names.
func (s RunState) String() string {
	switch s {
	case RunStarting:
		return "starting"
	case RunRunning:
		return "running"
	case RunWaitingApproval:
		return "waiting_approval"
	case RunCompleted:
		return "completed"
	case RunFailed:
		return "failed"
	case RunAborted:
		return "aborted"
	case RunInterrupted:
		return "interrupted"
	default:
		return "unknown"
	}
}

// IsTerminal reports whether the state is final.
//
// ★ A terminal run does not own the session any more. RunWaitingApproval is
//
//	terminal: the run stopped to ask the user a question, the answer starts
//	a NEW run (the one that carries the ParentRunID), and the session is
//	held by the pending approval — a different lock.
func (s RunState) IsTerminal() bool {
	switch s {
	case RunStarting, RunRunning:
		return false
	default:
		return true
	}
}

// MarshalJSON encodes the state as its wire name.
func (s RunState) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON decodes a wire name back into a state.
//
// ★ An unknown name is an error rather than a silent zero: a zero is
//
//	RunStarting, and a ledger that quietly read its rows as "starting"
//	would make every finished run look live again.
func (s *RunState) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	switch name {
	case "starting":
		*s = RunStarting
	case "running":
		*s = RunRunning
	case "waiting_approval":
		*s = RunWaitingApproval
	case "completed":
		*s = RunCompleted
	case "failed":
		*s = RunFailed
	case "aborted":
		*s = RunAborted
	case "interrupted":
		*s = RunInterrupted
	default:
		return fmt.Errorf("unknown run state %q", name)
	}
	return nil
}

// Run is one row of the run ledger.
//
// ★ It carries lifecycle facts only. Usage, message counts and the transcript
//
//	itself belong to the session's messages — keeping them here would give
//	the numbers two homes, and the dashboard would eventually disagree with
//	itself.
type Run struct {
	// RunID is the protocol-level identifier of this run. The client sees
	// it in every SSE frame and may send it back to re-attach.
	RunID string `json:"run_id"`
	// SessionID is the conversation the run belongs to.
	SessionID string `json:"session_id"`
	// Kind says whether the run is a fresh query or the answer to an
	// approval.
	Kind contract.TurnKind `json:"kind"`
	// State is where the run is in its lifecycle.
	State RunState `json:"state"`
	// Epoch is the fence this run holds. It is monotonically increasing per
	// session, and every durable write the holder makes carries it.
	Epoch uint64 `json:"epoch"`
	// Owner is the instance that claimed the run.
	Owner string `json:"owner,omitempty"`
	// ParentRunID is the run a resume continues. Empty for a query.
	ParentRunID string `json:"parent_run_id,omitempty"`
	// StartedAt is when the run was claimed.
	StartedAt time.Time `json:"started_at"`
	// UpdatedAt is when the row was last written.
	UpdatedAt time.Time `json:"updated_at"`
	// EndedAt is when the run reached a terminal state.
	EndedAt time.Time `json:"ended_at,omitempty"`
	// ErrorCode names the failure. Empty when the run did not fail.
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorMessage is the failure's message.
	ErrorMessage string `json:"error_message,omitempty"`
}

// RunLedger is the durable record of the runs of one session.
//
// ★ Append writes one COMPLETE snapshot of a run, not a delta. The reader
//
//	takes the last row per run id, and the highest epoch among the open
//	runs is the session's current owner. Both rules are order-tolerant,
//	which is what makes a fenced-out owner harmless: it cannot write itself
//	back into the lead.
type RunLedger interface {
	// Append writes one complete snapshot of a run.
	Append(ctx context.Context, r Run) error
	// List returns one row per run, newest row per run id wins.
	List(ctx context.Context) ([]Run, error)
	// NextEpoch allocates the fence epoch for a new run of this session.
	//
	// ★ The epoch is allocated by the ledger because the ledger is the
	//	durable thing: an epoch that only lived in memory would repeat
	//	after a restart, and a repeated epoch breaks the fence — the
	//	reader could no longer tell the old owner from the new one.
	NextEpoch(ctx context.Context) (uint64, error)
	// Reconcile closes the runs that were still open when the process
	// stopped, and reports how many it closed.
	//
	// ★ It runs once at startup. A run that was open when the process died
	//	has no owner any more: nothing is going to finish it and nothing is
	//	going to archive its messages. Leaving its row open would make the
	//	session read as busy for ever.
	Reconcile(ctx context.Context) (int, error)
}

// FoldRuns keeps the newest row per run id.
//
// ★ The order of the returned rows is the order of the newest rows, so a
//
//	caller can render them as a list without sorting again.
func FoldRuns(rows []Run) []Run {
	latest := make(map[string]Run, len(rows))
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, seen := latest[r.RunID]; !seen {
			order = append(order, r.RunID)
		}
		latest[r.RunID] = r
	}
	out := make([]Run, 0, len(order))
	for _, id := range order {
		out = append(out, latest[id])
	}
	return out
}

// ActiveRun returns the run that currently owns the session.
//
// ★ The owner is the OPEN run with the highest epoch. Not the last one
//
//	written: a zombie owner from epoch 3 that wakes up after epoch 4 took
//	over writes a row, but the reader still sees epoch 4 as the owner.
//	That is the fence doing its work on the read side, where it is cheap
//	and needs no lock.
func ActiveRun(runs []Run) (Run, bool) {
	var best Run
	found := false
	for _, r := range runs {
		if r.State.IsTerminal() {
			continue
		}
		if !found {
			best, found = r, true
			continue
		}
		if r.Epoch > best.Epoch {
			best = r
			continue
		}
		if r.Epoch == best.Epoch && r.StartedAt.After(best.StartedAt) {
			best = r
		}
	}
	return best, found
}

// SortRunsByStart orders runs oldest first. The list route shows a
// chronology, and the fold does not promise an order.
func SortRunsByStart(runs []Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
}

package run

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// runRow is the durable row of one run.
//
// ★ Every write goes through here, so the ledger only ever sees a complete
//
//	snapshot of the run and never a delta. The row is the run's public
//	record: it is what a scan reads, and what a second surface attaches to.
//
// ★ The first terminal write wins. A run that is closed by an abort and
//
//	then by the archive must not change its story halfway through: the
//	reader would see two different endings for the same run.
type runRow struct {
	mu     sync.Mutex
	ledger storage.RunLedger
	// ops is the session's operation log. ★ The timeline's "the round ended"
	//   line is written here, because this is the one place every ending
	//   passes through — the abort, the archive and the loop's own exit all
	//   call finish.
	ops storage.OperationLog
	run storage.Run
	// closed says the row is final. ★ A closed row stays closed: the run's
	// story is written once.
	closed bool

	log *slog.Logger
}

// newRunRow wraps a run's first snapshot. log may be nil: the row then drops
// the one warning it can write. ops may be nil too: the row then keeps no
// timeline.
func newRunRow(ledger storage.RunLedger, ops storage.OperationLog, r storage.Run, log *slog.Logger) *runRow {
	return &runRow{ledger: ledger, ops: ops, run: r, log: log}
}

// set writes a non-terminal state.
func (rw *runRow) set(state storage.RunState) {
	if rw == nil {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	rw.run.State = state
	rw.run.UpdatedAt = time.Now().UTC()
	rw.writeLocked()
}

// finish writes the terminal row.
//
// ★ A closed row stays closed. The run's story is written once: the abort
//
//	and the archive race for it, and whichever lands first is the truth.
func (rw *runRow) finish(state storage.RunState, code, message string) {
	if rw == nil {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	now := time.Now().UTC()
	rw.run.State = state
	rw.run.UpdatedAt = now
	rw.run.EndedAt = now
	rw.run.ErrorCode = code
	rw.run.ErrorMessage = message
	rw.closed = true
	rw.writeLocked()
	// ★ The row is terminal, so the round is over — whatever it ended as.
	//   ★ The timeline says which ending it was, and it is written here
	//   because this is the one place every ending passes through.
	rw.recordFinished(state)
}

// recordFinished writes the timeline's "the round ended" line.
//
// ★ It never fails the run: the line is a record of the ending, and a
//
//	record that could not be written is a gap in the timeline, not a
//	different ending.
func (rw *runRow) recordFinished(state storage.RunState) {
	if rw.ops == nil {
		return
	}
	op := storage.Operation{
		Kind:  storage.OpRunFinished,
		Actor: storage.ActorSystem,
		RunID: rw.run.RunID,
		State: state.String(),
	}
	if err := rw.ops.Append(context.Background(), op); err != nil {
		logf(rw.log, "session=%s run=%s failed to write the run's end line: %v",
			rw.run.SessionID, rw.run.RunID, err)
	}
}

// noteMCPServers records the MCP servers the run touched.
//
// ★ The names are the servers' own, and the ledger holds them once: a run
//
//	touches a server many times, and the row says which servers were
//	involved, not how often.
func (rw *runRow) noteMCPServers(names []string) {
	if rw == nil || len(names) == 0 {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	seen := make(map[string]bool, len(rw.run.MCPServers))
	for _, n := range rw.run.MCPServers {
		seen[n] = true
	}
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		rw.run.MCPServers = append(rw.run.MCPServers, n)
	}
	sort.Strings(rw.run.MCPServers)
	rw.run.UpdatedAt = time.Now().UTC()
	rw.writeLocked()
}

// noteAuthorizationMode records the approval mode the round ran under.
//
// ★ The decision log only holds what a human decided, so a round that ran
//
//	without asking leaves no trace there. This is the row that says it.
//
// ★ The mode is written once, when the round starts: the round keeps the
//
//	mode it began with, and a row that followed the session's mode would
//	change its story halfway through.
func (rw *runRow) noteAuthorizationMode(mode contract.AuthorizationMode) {
	if rw == nil || mode == "" {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	// ★ The first note wins. The round keeps the mode it began with, so a
	//   second note would rewrite the row's story about the same round.
	if rw.run.AuthorizationMode != "" {
		return
	}
	rw.run.AuthorizationMode = mode
	rw.run.UpdatedAt = time.Now().UTC()
	rw.writeLocked()
}

// noteTree records the tree the run ran on.
//
// ★ The fingerprint is what a resume asks for: the round it continues must
//
//	run on the tree that round started with. ★ An empty fingerprint means
//	the tree is not known — and a resume that finds none falls back to the
//	current tree and says so.
func (rw *runRow) noteTree(fingerprint string) {
	if rw == nil || fingerprint == "" {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	rw.run.TreeFingerprint = fingerprint
	rw.run.UpdatedAt = time.Now().UTC()
	rw.writeLocked()
}

// mcpServersOf names the MCP servers the messages touched.
//
// ★ The tool name is namespaced, and the namespace's first segment is the
//
//	server: mcp__<server>__<tool>. ★ A server name may not contain a run
//	of underscores, so the boundary is unambiguous and the name does not
//	have to be parsed back into anything else.
func mcpServersOf(msgs []*schema.Message) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, tc := range m.ToolCalls {
			name, ok := mcpServerOf(tc.Function.Name)
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// mcpServerOf reads the server out of an exposed tool name.
func mcpServerOf(tool string) (string, bool) {
	if !strings.HasPrefix(tool, decl.MCPToolPrefix) {
		return "", false
	}
	rest := tool[len(decl.MCPToolPrefix):]
	i := strings.Index(rest, decl.MCPSeparator)
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}

// isClosed reports whether the row already carries a terminal state.
func (rw *runRow) isClosed() bool {
	if rw == nil {
		return true
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.closed
}

// writeLocked appends the current snapshot.
//
// ★ A failed write is logged, not returned: the run itself must not fail
//
//	because its bookkeeping could not be written. The ledger is a record,
//	not the run's execution — and a missing row is visible (the scan will
//	not see the run), while a failed run is not recoverable at all.
func (rw *runRow) writeLocked() {
	if rw.ledger == nil {
		return
	}
	if err := rw.ledger.Append(context.Background(), rw.run); err != nil {
		logf(rw.log, "session=%s run=%s failed to write the run row: %v", rw.run.SessionID, rw.run.RunID, err)
	}
}

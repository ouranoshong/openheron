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
	run    storage.Run
	closed bool

	log *slog.Logger
}

// newRunRow wraps a run's first snapshot. log may be nil: the row then drops
// the one warning it can write.
func newRunRow(ledger storage.RunLedger, r storage.Run, log *slog.Logger) *runRow {
	return &runRow{ledger: ledger, run: r, log: log}
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

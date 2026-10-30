package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/storage"
)

var _ storage.RunLedger = (*fileRunLedger)(nil)

const (
	// runsFile is the run ledger's name inside a session directory.
	//
	// ★ It is NOT state.json. state.json is rewritten whole on every
	//	change, so a growing list of runs inside it would mean rewriting
	//	the whole history of the session on every transition. An
	//	append-only file matches the transcript's shape and reuses the same
	//	append machinery.
	runsFile = "runs.jsonl"

	// maxRunLedgerRows bounds the ledger file.
	//
	// ★ A ledger that grows forever is a leak. The bound is what makes the
	//	file's cost predictable; a session with a thousand turns keeps the
	//	last two hundred rows and drops the rest.
	maxRunLedgerRows = 200
)

// fileRunLedger is one session's runs.jsonl.
//
// ★ The file is append-only in the common case. It is rewritten — atomically,
//
//	through the same tmp -> rename dance as state.json — only when it grows
//	past its bound.
type fileRunLedger struct {
	root string
	id   string

	// mu serializes the epoch allocation and the trim. The append itself is
	// an O_APPEND write, which the kernel already serializes.
	mu sync.Mutex
	// next is the last epoch handed out, and epochSeeded says whether it
	// was read from the file yet.
	next        uint64
	epochSeeded bool
}

// newRunLedger builds the ledger of one session. It touches no disk.
func newRunLedger(root, id string) *fileRunLedger {
	return &fileRunLedger{root: root, id: id}
}

// runsPath is the ledger's path.
func runsPath(root, id string) string {
	return filepath.Join(sessionDir(root, id), runsFile)
}

// Append writes one complete snapshot of a run.
//
// ★ The write is an append, not a rewrite: the ledger is the transcript's
//
//	sibling, and a reader that takes the last row per run id does not care
//	how many snapshots a run went through.
func (l *fileRunLedger) Append(_ context.Context, r storage.Run) error {
	if r.RunID == "" {
		return errors.New("a run row needs a run id")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode run %s: %w", r.RunID, err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	path := runsPath(l.root, l.id)
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append to %s: %w", path, err)
	}
	// The ledger row is the barrier that tells a reader "the messages are
	// on disk", so it has to survive a crash — not just a process exit.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	return l.trimLocked()
}

// trimLocked keeps the ledger within its bound.
//
// ★ The rewrite is atomic (tmp -> rename), so a reader never sees a
//
//	half-written ledger. It is rare: only a session with more than
//	maxRunLedgerRows snapshots pays for it.
func (l *fileRunLedger) trimLocked() error {
	path := runsPath(l.root, l.id)
	rows, err := readRunRows(path)
	if err != nil {
		return err
	}
	if len(rows) <= maxRunLedgerRows {
		return nil
	}
	keep := rows[len(rows)-maxRunLedgerRows:]

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	enc := json.NewEncoder(f)
	for i := range keep {
		if err := enc.Encode(keep[i]); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("encode run row: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// List returns one row per run, newest row per run id wins.
//
// ★ A malformed line is dropped rather than failing the read. The common
//
//	case is a torn last line — a crash between two writes — and dropping it
//	sends the run back to its previous state, which is a state it really
//	was in.
func (l *fileRunLedger) List(_ context.Context) ([]storage.Run, error) {
	rows, err := readRunRows(runsPath(l.root, l.id))
	if err != nil {
		return nil, err
	}
	return storage.FoldRuns(rows), nil
}

// NextEpoch allocates the fence epoch for a new run of this session.
//
// ★ The epoch is seeded from the file, not from a counter that only lives in
//
//	memory: an in-memory counter would repeat after a restart, and a
//	repeated epoch breaks the fence — the reader could no longer tell the
//	old owner from the new one.
//
// ★ The counter is kept in memory between the calls, because the file only
//
//	records the epochs that were actually used. An allocation that is
//	never written leaves a gap, and a gap is harmless: the epoch is an
//	ordering device, not a dense counter.
//
// ★ The allocation is not atomic with the append that follows it. Two
//
//	concurrent allocations could hand out the same epoch — but the
//	admission gate allows one turn per session, so there is only ever one
//	allocator for a session at a time.
func (l *fileRunLedger) NextEpoch(_ context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.epochSeeded {
		rows, err := readRunRows(runsPath(l.root, l.id))
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if r.Epoch > l.next {
				l.next = r.Epoch
			}
		}
		l.epochSeeded = true
	}
	l.next++
	return l.next, nil
}

// Reconcile closes the runs that were still open when the process stopped.
//
// ★ It runs once at startup, before the server accepts a request. A run
//
//	that was open when the process died has no owner any more: nothing is
//	going to finish it and nothing is going to archive its messages. The
//	only honest thing left is to say so — and to stop the session from
//	reading as busy forever.
//
// ★ The run's owner is kept: the row still says who held it. The
//
//	reconciler does not take the blame for someone else's crash.
func (l *fileRunLedger) Reconcile(ctx context.Context) (int, error) {
	runs, err := l.List(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	closed := 0
	for _, r := range runs {
		if r.State.IsTerminal() {
			continue
		}
		r.State = storage.RunInterrupted
		r.UpdatedAt = now
		r.EndedAt = now
		r.ErrorCode = "interrupted"
		r.ErrorMessage = "the process stopped while this run was open"
		if err := l.Append(ctx, r); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// readRunRows decodes a ledger file.
//
// ★ A missing file is an empty ledger, not an error: a session that never
//
//	ran has no rows, and that is a normal state.
func readRunRows(path string) ([]storage.Run, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	out := make([]storage.Run, 0, 16)
	skipped := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r storage.Run
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			skipped++
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if skipped > 0 {
		obs.Logger().Warn("[session] skipped malformed run rows",
			"path", path, "rows", skipped)
	}
	return out, nil
}

package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/storage"
)

var _ storage.OperationLog = (*fileOperationLog)(nil)

const (
	// operationsFile is the operation log's name inside a session directory.
	//
	// ★ It is NOT events.jsonl: /v2/sessions/{id}/events is the live
	//	announcement channel, and a file with that name would look like the
	//	channel's durable twin. "operations" is the timeline; "events" is the
	//	live notice.
	operationsFile = "operations.jsonl"
)

// fileOperationLog is one session's operations.jsonl.
//
// ★ The file is append-only and is never rewritten: a line that is gone is
//
//	an operation that cannot be looked up again.
type fileOperationLog struct {
	root string
	id   string
	log  *slog.Logger

	// mu serializes the sequence allocation and the write. The append itself
	// is an O_APPEND write, which the kernel already serializes — but the
	// number and the line have to land in the same order, and only this lock
	// can promise that.
	mu sync.Mutex

	// next is the last sequence number handed out, and seeded says whether it
	// was read from the file yet.
	next   uint64
	seeded bool
}

func newOperationLog(root, id string) *fileOperationLog {
	return &fileOperationLog{root: root, id: id}
}

func (l *fileOperationLog) logger() *slog.Logger {
	if l == nil || l.log == nil {
		return discard
	}
	return l.log
}

// operationsPath is the operation log's path.
func operationsPath(root, id string) string {
	return filepath.Join(sessionDir(root, id), operationsFile)
}

// Append writes one operation.
//
// ★ The sequence number and the line are written under one lock. Two writers
//
//	are normal — the API layer and the runtime both write — and a number
//	allocated apart from its line can land in the file after a bigger one,
//	which leaves the file's order and the numbers' order disagreeing.
func (l *fileOperationLog) Append(_ context.Context, op storage.Operation) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.seedLocked(); err != nil {
		return err
	}
	l.next++
	op.Seq = l.next
	if op.At.IsZero() {
		op.At = time.Now().UTC()
	}

	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("encode operation %s: %w", op.Kind, err)
	}

	path := operationsPath(l.root, l.id)
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
	// ★ The line is synced: the operation already happened, and a line that
	//   a crash swallowed says the operation did not. The transcript does not
	//   sync its appends — a message has the checkpoint behind it, an
	//   operation has nothing.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// seedLocked reads the last sequence number out of the file.
//
// ★ It is seeded from the file, not from a counter that only lives in
//
//	memory: a counter that restarts at 1 would hand out a number the file
//	already uses, and two lines would claim the same place in the timeline.
func (l *fileOperationLog) seedLocked() error {
	if l.seeded {
		return nil
	}
	rows, err := readOperationRows(operationsPath(l.root, l.id), l.logger())
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Seq > l.next {
			l.next = r.Seq
		}
	}
	l.seeded = true
	return nil
}

// List returns the session's operations, oldest first.
//
// ★ A malformed line is dropped rather than failing the read. The common
//
//	case is a torn last line — a crash between two writes — and dropping it
//	sends the timeline back to its previous line, which is a state it really
//	was in.
func (l *fileOperationLog) List(_ context.Context) ([]storage.Operation, error) {
	rows, err := readOperationRows(operationsPath(l.root, l.id), l.logger())
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// readOperationRows decodes an operation log file.
//
// ★ A missing file is an empty log, not an error: a session that never had
//
//	an operation has no rows, and that is a normal state.
func readOperationRows(path string, log *slog.Logger) ([]storage.Operation, error) {
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

	out := make([]storage.Operation, 0, 16)
	skipped := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var op storage.Operation
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			skipped++
			continue
		}
		out = append(out, op)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if skipped > 0 {
		log.Warn("[session] skipped malformed operation lines",
			"path", path, "lines", skipped)
	}
	return out, nil
}

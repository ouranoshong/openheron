package persist

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const pollInterval = 200 * time.Millisecond

type FileLock struct {
	path string
	f    *os.File

	depth int
}

func RunLockPath(dir string) string { return filepath.Join(dir, ".lock.run") }

func LockPath(dir string) string { return filepath.Join(dir, ".lock") }

func OpenFileLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("persist: create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("persist: open lock file %s: %w", path, err)
	}
	return &FileLock{path: path, f: f}, nil
}

func (l *FileLock) Lock(ctx context.Context) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	if l.depth > 0 {
		l.depth++
		return l.release, nil
	}
	for {
		ferr := lockFile(l.f)
		if ferr == nil {
			l.depth = 1
			l.stampHolder()
			return l.release, nil
		}
		if !errors.Is(ferr, errLockBusy) {
			return nil, fmt.Errorf("persist: lock %s: %w", l.path, ferr)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s is locked by another openheron process (%s): %w",
				l.path, l.holderHint(), ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

func (l *FileLock) Try() (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	if l.depth > 0 {
		l.depth++
		return l.release, nil
	}
	ferr := lockFile(l.f)
	if ferr == nil {
		l.depth = 1
		l.stampHolder()
		return l.release, nil
	}
	if errors.Is(ferr, errLockBusy) {
		return nil, fmt.Errorf("persist: %s is held by another openheron process (%s): %w",
			l.path, l.holderHint(), ErrLocked)
	}
	return nil, fmt.Errorf("persist: lock %s: %w", l.path, ferr)
}

var ErrLocked = fmt.Errorf("locked")

var errLockBusy = errors.New("file lock is held by another process")

func (l *FileLock) release() {
	l.depth--
	if l.depth > 0 {
		return
	}
	_ = unlockFile(l.f)
	_ = l.f.Truncate(0)
}

func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

func (l *FileLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *FileLock) stampHolder() {
	if l.f == nil {
		return
	}
	body := strconv.Itoa(os.Getpid()) + "\n"
	if err := l.f.Truncate(0); err != nil {
		return
	}
	if _, err := l.f.WriteAt([]byte(body), 0); err != nil {
		return
	}
}

func (l *FileLock) holderHint() string {
	b, err := os.ReadFile(l.path)
	if err != nil {
		return "holder unknown"
	}
	pid := strings.TrimSpace(string(b))
	if pid == "" {
		return "holder unknown"
	}
	return "pid " + pid
}

package persist

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tmpLock(t *testing.T) string {
	t.Helper()
	return LockPath(filepath.Join(t.TempDir(), "idx"))
}

func TestLockExcludesWithinTheProcess(t *testing.T) {
	path := tmpLock(t)

	a, err := OpenFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	rel, err := a.Lock(context.Background())
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	rel2, err := a.Lock(context.Background())
	if err != nil {
		t.Fatalf("re-entrant lock: %v", err)
	}

	b, err := OpenFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second handle acquired the lock: %v", err)
	}

	rel2()
	if _, err := b.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("the outer lock was lost when the inner one released: %v", err)
	}
	rel()
	rel3, err := b.Try()
	if err != nil {
		t.Fatalf("after full release: %v", err)
	}
	rel3()
}

func TestTryReportsLockedWithoutWaiting(t *testing.T) {
	path := tmpLock(t)
	a, _ := OpenFileLock(path)
	defer a.Close()
	b, _ := OpenFileLock(path)
	defer b.Close()

	rel, err := a.Try()
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	started := time.Now()
	if _, err := b.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if d := time.Since(started); d > 200*time.Millisecond {
		t.Fatalf("Try waited %s; it must return immediately", d)
	}
}

func TestLockIsCancellable(t *testing.T) {
	path := tmpLock(t)
	a, _ := OpenFileLock(path)
	defer a.Close()
	b, _ := OpenFileLock(path)
	defer b.Close()

	rel, err := a.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := b.Lock(ctx); err == nil {
		t.Fatal("Lock succeeded while another handle held it")
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Fatalf("Lock took %s to give up; it should follow ctx", d)
	}
}

func TestLockErrorNamesTheHolder(t *testing.T) {
	path := tmpLock(t)
	a, _ := OpenFileLock(path)
	defer a.Close()
	b, _ := OpenFileLock(path)
	defer b.Close()

	rel, _ := a.Lock(context.Background())
	defer rel()

	_, err := b.Try()
	if err == nil {
		t.Fatal("expected a lock conflict")
	}
	if !strings.Contains(err.Error(), "pid ") {
		t.Errorf("error should name the holder's pid, got: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the lock file, got: %v", err)
	}
}

func TestLockExcludesAnotherProcess(t *testing.T) {
	if os.Getenv("OPENHERON_LOCK_HELPER") == "1" {
		path := os.Getenv("OPENHERON_LOCK_PATH")
		l, err := OpenFileLock(path)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(3)
		}
		if _, err := l.Lock(context.Background()); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(4)
		}
		os.Stdout.WriteString("locked\n")
		_, _ = os.Stdin.Read(make([]byte, 1))
		os.Exit(0)
	}

	path := tmpLock(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestLockExcludesAnotherProcess")
	cmd.Env = append(os.Environ(),
		"OPENHERON_LOCK_HELPER=1",
		"OPENHERON_LOCK_PATH="+path,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	buf := make([]byte, 16)
	n, err := stdout.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "locked") {
		t.Fatalf("helper did not report holding the lock (n=%d err=%v)", n, err)
	}

	mine, err := OpenFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer mine.Close()
	if _, err := mine.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("this process acquired a lock another process holds: %v", err)
	}

	_ = stdin.Close()
	_ = cmd.Wait()

	deadline := time.Now().Add(3 * time.Second)
	for {
		rel, err := mine.Try()
		if err == nil {
			rel()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lock never came back after the holder exited: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLockSurvivesHolderBeingKilled(t *testing.T) {
	if os.Getenv("OPENHERON_LOCK_HELPER") == "1" {
		path := os.Getenv("OPENHERON_LOCK_PATH")
		l, err := OpenFileLock(path)
		if err != nil {
			os.Exit(3)
		}
		if _, err := l.Lock(context.Background()); err != nil {
			os.Exit(4)
		}
		os.Stdout.WriteString("locked\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}

	path := tmpLock(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestLockSurvivesHolderBeingKilled")
	cmd.Env = append(os.Environ(),
		"OPENHERON_LOCK_HELPER=1",
		"OPENHERON_LOCK_PATH="+path,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	if n, err := stdout.Read(buf); err != nil || !strings.Contains(string(buf[:n]), "locked") {
		t.Fatalf("helper did not take the lock (n=%d err=%v)", n, err)
	}

	mine, err := OpenFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer mine.Close()
	if _, err := mine.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("lock not held by the helper: %v", err)
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	deadline := time.Now().Add(3 * time.Second)
	for {
		rel, err := mine.Try()
		if err == nil {
			rel()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a killed holder left the lock stuck forever: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLockPathIsDerivedFromTheDir(t *testing.T) {
	if got := LockPath("/a/b"); got != "/a/b/.lock" {
		t.Fatalf("LockPath = %q", got)
	}
}

func TestNilLockIsUsable(t *testing.T) {
	var l *FileLock
	rel, err := l.Lock(context.Background())
	if err != nil || rel == nil {
		t.Fatalf("nil lock: err=%v (rel nil: %v)", err, rel == nil)
	}
	rel()
	rel2, err := l.Try()
	if err != nil || rel2 == nil {
		t.Fatalf("nil try: err=%v (rel nil: %v)", err, rel2 == nil)
	}
	rel2()
	if err := l.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if l.Path() != "" {
		t.Fatalf("nil path = %q", l.Path())
	}
}

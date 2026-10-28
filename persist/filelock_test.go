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

	// ★ 同一实例的第二次获取是**计数**，不是第二次 syscall。这一点关系到
	//   「Index 持锁跑完整轮、内部每次 Put 再锁一次」能不能成立。
	rel2, err := a.Lock(context.Background())
	if err != nil {
		t.Fatalf("re-entrant lock: %v", err)
	}

	b, err := OpenFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// ★ 另一个**打开文件描述**必须被挡住 —— 这正是「换个 fd 去拿同一把锁会
	//   死锁自己」的另一面，也是跨进程互斥的同一套语义。
	if _, err := b.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second handle acquired the lock: %v", err)
	}

	// 内层释放后外层仍然持有。
	rel2()
	if _, err := b.Try(); !errors.Is(err, ErrLocked) {
		t.Fatalf("the outer lock was lost when the inner one released: %v", err)
	}
	rel()
	// 外层也放开之后，另一个描述符才拿得到。
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

// ★ Lock 必须**能被 ctx 打断**。用 flock 的阻塞形态就做不到 —— 而对方可能
//
//	正在跑一次十分钟的索引，那意味着 Ctrl-C 也没用。
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
	// ★ 报出持有者的 pid：`kill -0` 能确认它还在不在，而「谁占着」是用户
	//   唯一能照做的信息。
	if !strings.Contains(err.Error(), "pid ") {
		t.Errorf("error should name the holder's pid, got: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the lock file, got: %v", err)
	}
}

// ★ 这条是本文件存在的原因：**另一个进程**拿不到锁。
//
//	进程内互斥由 sync.Mutex 就够了，真正会出事的是 `openheron kb index` 与服务
//	进程里的 kb_index 工具同时写 —— 而那是两个进程。用一个真的子进程来验，
//	而不是在同一进程里开两个 handle（后者验的是 flock 的描述符语义）。
func TestLockExcludesAnotherProcess(t *testing.T) {
	if os.Getenv("OPENHERON_LOCK_HELPER") == "1" {
		// 子进程：拿到锁、告诉父进程、然后一直持有到 stdin 关闭。
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

	// 放掉子进程，锁应当自己回来（不依赖它显式解锁）。
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

// ★ 持有者被 kill -9 之后锁必须自己回来。这正是选 flock 而不是「O_EXCL 锁
//
//	文件」的理由：一次十分钟的索引崩掉不该让索引功能永久不可用。
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
	// 零值可用：Store 在测试里可能没有锁文件。
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

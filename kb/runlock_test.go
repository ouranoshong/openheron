package kb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"openheron/persist"
)

// 这个文件钉住**运行锁**：一次索引运行只允许一个。
//
// ★ v2 **没有写锁**（`.lock` 已删，见 docs/kb-design.md §7.1）。写正确性由
//
//	乐观提交负责（[Store.Reconcile] 的写后读校验），所以运行锁只剩一个
//	职责：运行期互斥 —— 只有一个索引在跑。
//
//	它**不挡写**：一个正在跑索引的进程不该让另一个进程的一次小增量写
//	等它 —— 那次写只需要毫秒，而正确性由乐观提交保证。

func TestStartRunIsExclusiveWithinTheProcess(t *testing.T) {
	st := newTestStore(t)
	other, err := NewStore(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatalf("first StartRun: %v", err)
	}

	if _, err := other.StartRun(); !errors.Is(err, persist.ErrLocked) {
		t.Fatalf("a second run started while one was in flight: %v", err)
	}

	stop()
	stop2, err := other.StartRun()
	if err != nil {
		t.Fatalf("after the first run finished: %v", err)
	}
	stop2()
}

// ★ 运行锁**不排队**：拿不到立刻返回。命令行与 agent 工具各自决定怎么处理，
//
//	但两者都不该在这里挂住 —— 一次索引是十分钟量级。
func TestStartRunDoesNotQueue(t *testing.T) {
	st := newTestStore(t)
	other, _ := NewStore(st.Dir())
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	started := time.Now()
	_, rerr := other.StartRun()
	if !errors.Is(rerr, persist.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", rerr)
	}
	if d := time.Since(started); d > 300*time.Millisecond {
		t.Fatalf("StartRun waited %s; it must refuse immediately", d)
	}
	if !strings.Contains(rerr.Error(), "pid ") {
		t.Errorf("the error should name the holder so a human can check it, got: %v", rerr)
	}
}

// ★ 运行锁**不挡写**。这正是它与写正确性分开的意义：一个正在跑索引的进程
//
//	不该让另一个进程的一次小增量写等它 —— 那次写只需要毫秒。
func TestRunLockDoesNotBlockWrites(t *testing.T) {
	st := newTestStore(t)
	other, _ := NewStore(st.Dir())
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	done := make(chan error, 1)
	go func() {
		_, err := other.Reconcile(context.Background(),
			ReconcileRequest{Fresh: []Entry{ent("x.md", "t", "x")}})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Reconcile while a run lock is held: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Reconcile was blocked by the run lock; it must not gate writes")
	}
}

// ★ 反过来：一个写到一半的进程也不该挡住「开始一次运行」。若两者共用一把
// 锁，那么一个卡在写上的进程会让整个索引命令起不来 —— 而那两件事毫无关系。
func TestWriteDoesNotBlockStartingARun(t *testing.T) {
	st := newTestStore(t)
	other, _ := NewStore(st.Dir())
	defer other.Close()

	// 反复写，制造一个「正在写」的窗口。
	stopWrite := make(chan struct{})
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case <-stopWrite:
				return
			default:
			}
			_, _ = st.Reconcile(context.Background(),
				ReconcileRequest{Fresh: []Entry{ent("w.md", "t", "w")}})
		}
	}()

	stop, err := other.StartRun()
	if err != nil {
		close(stopWrite)
		<-writeDone
		t.Fatalf("a concurrent write blocked starting a run: %v", err)
	}
	stop()
	close(stopWrite)
	<-writeDone
}

// ★ 一次运行跑完之后锁要回来 —— 否则第二次命令永远起不来，而症状是
//
//	「索引功能突然不能用了」。
func TestRunLockComesBackAfterIndex(t *testing.T) {
	_, corpus := seedProject(t, map[string]string{"a.md": "# A\n\n## 一\n\n内容\n"})
	st := newTestStore(t)
	ix := newTestIndexer(t, st, newFakeEmbedder(16), 10)

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Index(context.Background(), ix, testIndexOpts(corpus), nil); err != nil {
		t.Fatalf("Index: %v", err)
	}
	stop()

	other, _ := NewStore(st.Dir())
	defer other.Close()
	stop2, err := other.StartRun()
	if err != nil {
		t.Fatalf("the run lock did not come back: %v", err)
	}
	stop2()
}

package kb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"openheron/persist"
)

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

func TestWriteDoesNotBlockStartingARun(t *testing.T) {
	st := newTestStore(t)
	other, _ := NewStore(st.Dir())
	defer other.Close()

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

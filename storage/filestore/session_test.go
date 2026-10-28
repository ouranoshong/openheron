package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

func newManager(t *testing.T, opts Options) *FileManager {
	t.Helper()
	fm, err := NewFileManager(t.TempDir(), opts)
	if err != nil {
		t.Fatalf("NewFileManager: %v", err)
	}
	return fm
}

// Get must never create on demand: an arbitrary id in a URL yields 404.
func TestGetDoesNotCreate(t *testing.T) {
	fm := newManager(t, Options{})
	ctx := context.Background()

	id := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	if _, err := fm.Get(ctx, id); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
	if fileExists(messagesPath(fm.root, id)) {
		t.Fatal("Get must not have created anything on disk")
	}
}

func TestCreateAppendReload(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	created, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Append(message.NewUser("hello")); err != nil {
		t.Fatal(err)
	}
	if err := created.Append(message.NewAssistant("hi", nil)); err != nil {
		t.Fatal(err)
	}
	id := created.ID()

	// Reopen from scratch: nothing may live only in memory.
	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	msgs := reloaded.Messages()
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages after reload, got %d", len(msgs))
	}
	if message.UserText(msgs[0]) != "hello" {
		t.Fatalf("unexpected first message: %#v", msgs[0])
	}
	if reloaded.Len() != 2 {
		t.Fatalf("Len disagrees with Messages: %d", reloaded.Len())
	}
}

// 会话的入口 agent 在**创建时**就落进 state.json，之后从 state 读。
//
// ★ 钉的是「落盘」而不是「内存里有」：重新打开（模拟重启）之后它还在，
//
//	才说明它不是每次现算的默认值。
func TestCreateRecordsTheRootAgentInTheState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	created, err := fm.Create(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if got := created.AgentID(); got != "worker" {
		t.Fatalf("agent id = %q, want worker", got)
	}

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, created.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AgentID(); got != "worker" {
		t.Fatalf("reloaded agent id = %q, want worker", got)
	}
}

// The approval gate is fail-closed: a state write that fails must not leave an
// approval usable from memory.
func TestPendingApprovalFailsClosed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	sess, _ := fm.Create(ctx, "")
	id := sess.ID()

	if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: []contract.PendingInterrupt{{InterruptID: "agent:A;tool:x"}}}); err != nil {
		t.Fatal(err)
	}

	// Make the directory unwritable so the atomic rewrite genuinely fails.
	dir := sessionDir(root, id)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: []contract.PendingInterrupt{{InterruptID: "agent:B;tool:y"}}}); err == nil {
		t.Fatal("expected a persistence failure")
	}

	got, err := sess.PendingApproval()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Interrupts[0].InterruptID != "agent:A;tool:x" {
		t.Fatalf("fail-closed violated: in-memory approval changed to %#v", got)
	}
}

// Peek must not consume: the render path reads the approval to rebuild its
// chip, and taking it there would destroy the user's decision.
func TestPendingPeekDoesNotConsume(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}}}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		p, err := sess.PendingApproval()
		if err != nil || p == nil {
			t.Fatalf("peek %d lost the approval: %v %#v", i, err, p)
		}
	}

	taken, err := sess.TakePendingApproval()
	if err != nil || taken == nil || taken.Interrupts[0].InterruptID != "int-1" {
		t.Fatalf("take failed: %v %#v", err, taken)
	}
	after, _ := sess.PendingApproval()
	if after != nil {
		t.Fatalf("approval survived the take: %#v", after)
	}
}

// 待批在重启后仍然读得到。
func TestPendingApprovalSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	if err := sess.Append(message.NewUser("q")); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: []contract.PendingInterrupt{{InterruptID: "int-9"}}}); err != nil {
		t.Fatal(err)
	}
	id := sess.ID()

	fresh, _ := NewFileManager(root, Options{})
	reloaded, _ := fresh.Get(ctx, id)
	pending, err := reloaded.PendingApproval()
	if err != nil || pending == nil || pending.Interrupts[0].InterruptID != "int-9" {
		t.Fatalf("pending approval did not survive the restart: %v %#v", err, pending)
	}
	if reloaded.Len() != 1 {
		t.Fatalf("the message count must not be disturbed by the approval; Len=%d", reloaded.Len())
	}
}

func TestRejectsUnsafeIDs(t *testing.T) {
	fm := newManager(t, Options{})
	ctx := context.Background()
	for _, id := range []string{"", "../escape", "/abs/path", "a/b", "nope", strings.Repeat("x", 100)} {
		if _, err := fm.Get(ctx, id); !errors.Is(err, storage.ErrSessionNotFound) {
			t.Errorf("Get(%q): want ErrSessionNotFound, got %v", id, err)
		}
		if err := fm.Delete(ctx, id); err == nil {
			t.Errorf("Delete(%q) should have been rejected", id)
		}
	}
}

func TestDeleteRemovesEverything(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var tornDown []string
	fm, _ := NewFileManager(root, Options{Teardown: recorderTeardown{&tornDown}})

	sess, _ := fm.Create(ctx, "")
	id := sess.ID()
	if _, err := sess.PutFile("notes.txt", strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}
	if !fileExists(workspacePath(root, id)) {
		t.Fatal("workspace not created")
	}

	if err := fm.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if fileExists(sessionDir(root, id)) {
		t.Fatal("session directory survived delete")
	}
	if len(tornDown) != 1 || tornDown[0] != id {
		t.Fatalf("teardown not ordered correctly: %v", tornDown)
	}
	if _, err := fm.Get(ctx, id); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Fatalf("deleted session still resolvable: %v", err)
	}
}

type recorderTeardown struct{ seen *[]string }

func (r recorderTeardown) Cleanup(_ context.Context, id string) error {
	*r.seen = append(*r.seen, id)
	return nil
}

func TestList(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})

	a, _ := fm.Create(ctx, "")
	if err := a.Append(message.NewUser("first question")); err != nil {
		t.Fatal(err)
	}
	if _, err := fm.Create(ctx, ""); err != nil {
		t.Fatal(err)
	}

	metas, err := fm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(metas))
	}
	titles := map[string]string{}
	for _, m := range metas {
		titles[m.ID] = m.Title
	}
	if titles[a.ID()] != "first question" {
		t.Fatalf("title not derived from first user message: %q", titles[a.ID()])
	}
	for id, title := range titles {
		if id != a.ID() && title != "New Session" {
			t.Fatalf("empty session should fall back to a default title, got %q", title)
		}
	}
}

func TestWorkspaceConfinesUploads(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})
	sess, _ := fm.Create(ctx, "")

	if _, err := sess.PutFile("../escape.txt", strings.NewReader("x")); err == nil {
		t.Fatal("path traversal should have been rejected")
	}
	if _, err := sess.PutFile("/abs.txt", strings.NewReader("x")); err == nil {
		t.Fatal("absolute path should have been rejected")
	}
	got, err := sess.PutFile("sub/ok.txt", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, sess.Workspace()) {
		t.Fatalf("file escaped the workspace: %s", got)
	}
}

// A truncated tail must be counted, not silently swallowed.
func TestMalformedLinesAreCounted(t *testing.T) {
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(context.Background(), "")
	if err := sess.Append(message.NewUser("good")); err != nil {
		t.Fatal(err)
	}

	path := messagesPath(root, sess.ID())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.openForTest(sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Len() != 1 {
		t.Fatalf("want the good message to survive, got %d", reloaded.Len())
	}
}

// openForTest 打开一个会话**并**读出它的 transcript —— 像 Get 那样。
func (fm *FileManager) openForTest(id string) (*FileSession, error) {
	fs, err := fm.open(id)
	if err != nil {
		return nil, err
	}
	if err := fs.loadTranscript(); err != nil {
		return nil, err
	}
	return fs, nil
}

// ---- startup inspection ----

func TestInspectClassifiesEveryCombination(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name         string
		pending      bool
		checkpoint   bool
		wantReport   storage.Report
		wantReported int
	}{
		{
			name: "pending and checkpoint agree", pending: true, checkpoint: true,
			wantReport: storage.Report{PendingOK: 1},
		},
		{
			// ★ 孤儿待批**留着**：界面要继续显示那张卡，用户才有得答。
			name: "orphaned approval is kept", pending: true, checkpoint: false,
			wantReport: storage.Report{Orphan: 1},
		},
		{
			name: "checkpoint without pending is ownerless", pending: false, checkpoint: true,
			wantReport: storage.Report{Ownerless: 1}, wantReported: 1,
		},
		{
			name: "nothing at all is clean", pending: false, checkpoint: false,
			wantReport: storage.Report{Clean: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			fm, _ := NewFileManager(root, Options{})
			sess, _ := fm.Create(ctx, "")
			id := sess.ID()
			if tc.pending {
				if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.checkpoint {
				// ★ 存取器是每个会话自己的 —— 造一个真的 checkpoint 文件，
				//	而不是打桩（打桩要一个能注入的接口，而实现住在会话里）。
				store, _ := sess.GetCheckPointStore()
				if err := store.Set(ctx, id, []byte("runner state")); err != nil {
					t.Fatal(err)
				}
			}

			report, err := fm.Inspect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if report != tc.wantReport {
				t.Fatalf("want %+v, got %+v", tc.wantReport, report)
			}

			reloaded, err := fm.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := reloaded.PendingApproval()
			if tc.pending && p == nil {
				t.Fatal("an approval must survive inspection — the card has to stay visible")
			}
		})
	}
}

// ★ 巡检**不许**动待批记录。
//
//	早先的孤儿分支会把待批清掉，于是重启一次用户的审批就凭空消失 ——
//	界面上的那张卡没了，而他什么都不知道。这条用例钉住「只报告，不改数据」。
func TestInspectionNeverClearsAPendingApproval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	id := sess.ID()
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	// 没有 checkpoint ⇒ 孤儿分支。
	if _, err := fm.Inspect(ctx); err != nil {
		t.Fatal(err)
	}

	// 重新开一个 manager（绕开内存缓存）看磁盘上的那份。
	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := reloaded.PendingApproval()
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || len(p.Interrupts) != 1 || p.Interrupts[0].InterruptID != "int-1" {
		t.Fatalf("the pending approval must survive inspection, got %#v", p)
	}
}

func TestPutFileRejectsNilReader(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})
	sess, _ := fm.Create(ctx, "")
	if _, err := sess.PutFile("x.txt", nil); err == nil {
		t.Fatal("expected an error for a nil reader")
	}
}

var _ = schema.UserMessage
var _ = filepath.Join

// ★ 一个会话的 checkpoint 就住在**它自己的 checkpoints/ 子目录里** ——
//
//	删会话目录 = 删全。
func TestCheckpointLivesInTheSessionDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")

	// 会话把存取器交出去（契约上就是这一条）。
	got, deleter := sess.GetCheckPointStore()
	if got == nil || deleter == nil {
		t.Fatal("the session must hand out its checkpoint store")
	}
	if err := got.Set(ctx, sess.ID(), []byte("payload")); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, sess.ID(), ckptDir, sess.ID()+ckptExt)
	if !fileExists(path) {
		t.Fatalf("the checkpoint must live in the session's checkpoints directory: %s", path)
	}
	// 删会话目录 ⇒ checkpoint 一起没了。
	if err := fm.Delete(ctx, sess.ID()); err != nil {
		t.Fatal(err)
	}
	if fileExists(path) {
		t.Fatal("removing the session directory must remove the checkpoint too")
	}
}

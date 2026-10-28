package filestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

// ★ 仓库**不碰文件**：List 与 Inspect 只问会话要状态，不解析 transcript。
//
//	这不是洁癖：侧栏每轮都要列一次会话，而 transcript 可能很大（长会话
//	几百条消息，开发侧实测最大一个 1.1 MB）。仓库一旦开始解析它，每轮
//	就是 2.5 MB 的 JSON。
//
//	判据：把 transcript 写成**垃圾**，List 与 Inspect 仍然要能答出来 ——
//	它们根本没读它。
func TestListingDoesNotParseTheTranscript(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")

	// 一条用户消息 ⇒ 标题被推导并缓存（于是 List 不需要正文）。
	if err := sess.Append(message.NewUser("hello there")); err != nil {
		t.Fatal(err)
	}

	// 把 transcript 写成垃圾。
	path := messagesPath(root, sess.ID())
	if err := os.WriteFile(path, []byte("{not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	metas, err := fresh.List(ctx)
	if err != nil {
		t.Fatalf("List must not need the transcript: %v", err)
	}
	if len(metas) != 1 || metas[0].Title != "hello there" {
		t.Fatalf("List = %+v", metas)
	}

	report, err := fresh.Inspect(ctx)
	if err != nil {
		t.Fatalf("Inspect must not need the transcript: %v", err)
	}
	if report.Clean != 1 {
		t.Fatalf("report = %+v", report)
	}
}

// ★ 但**打开**一个会话要当场读 transcript —— 损坏必须是当场的失败，
//
//	而不是事后一个空列表（那看起来像历史丢了）。
func TestOpeningASessionFailsOnACorruptTranscript(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")

	path := messagesPath(root, sess.ID())
	if err := os.WriteFile(path, []byte("{not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	if _, err := fresh.Get(ctx, sess.ID()); err == nil {
		t.Fatal("a corrupt transcript must fail the session open, not yield an empty conversation")
	}
}

// ★ 每个会话有自己的 checkpoint 目录 —— 互不干扰。
func TestEachSessionHasItsOwnCheckpointDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	first, _ := fm.Create(ctx, "")
	second, _ := fm.Create(ctx, "")

	store1, _ := first.GetCheckPointStore()
	if err := store1.Set(ctx, first.ID(), []byte("one")); err != nil {
		t.Fatal(err)
	}

	p1 := filepath.Join(root, first.ID(), ckptDir, first.ID()+ckptExt)
	p2 := filepath.Join(root, second.ID(), ckptDir, second.ID()+ckptExt)
	if !fileExists(p1) {
		t.Fatalf("the first session must own its checkpoint: %s", p1)
	}
	if fileExists(p2) {
		t.Fatalf("the second session must not see the first one's checkpoint: %s", p2)
	}
}

// ★ 会话落地自己：Create 之后，目录、transcript、state.json、workspace
//
//	都在 —— 而仓库只是铸了个 id。
func TestCreateLaysDownTheWholeSession(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "main")
	id := sess.ID()

	for _, p := range []string{
		filepath.Join(root, id),
		messagesPath(root, id),
		statePath(root, id),
		workspacePath(root, id),
	} {
		if !fileExists(p) {
			t.Fatalf("missing %s", p)
		}
	}
	// 入口 agent 落进 state.json。
	st, _, err := readState(statePath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentID != "main" {
		t.Fatalf("agent id = %q, want main", st.AgentID)
	}
}

// ★ 删会话幂等 —— 目录可能已经不在了。
func TestDeletingASessionTwiceIsANoOp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	id := sess.ID()

	if err := fm.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := fm.Delete(ctx, id); err != nil {
		t.Fatalf("the second delete must be a no-op: %v", err)
	}
	if fileExists(sessionDir(root, id)) {
		t.Fatal("the session directory must be gone")
	}
}

// ★ header 的 id 与目录名不一致时（有人手动挪过会话目录），checkpoint 的
//
//	存取器也得跟着走 —— 否则两半分家：transcript 在一个目录，checkpoint
//	在另一个。
func TestCheckpointStoreFollowsTheHeaderID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(ctx, "")
	id := sess.ID()

	other := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	rewriteHeader(t, messagesPath(root, id), func(h *logHeader) { h.ID = other })

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ID() != other {
		t.Fatalf("the header wins: want %q, got %q", other, reloaded.ID())
	}

	store, _ := reloaded.GetCheckPointStore()
	if err := store.Set(ctx, reloaded.ID(), []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(root, other, ckptDir, other+ckptExt)) {
		t.Fatal("the checkpoint store must follow the header's id")
	}
}

// ★ 待批在巡检之后**留着**（界面那张卡继续显示）。
func TestInspectionKeepsThePendingApproval(t *testing.T) {
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

	fresh, _ := NewFileManager(root, Options{})
	report, err := fresh.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Orphan != 1 {
		t.Fatalf("report = %+v", report)
	}

	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := reloaded.PendingApproval()
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.Interrupts) != 1 {
		t.Fatalf("the pending approval must survive the inspection: %+v", pending)
	}
}

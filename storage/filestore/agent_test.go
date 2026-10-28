package filestore

import (
	"context"
	"testing"

	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

// 会话的入口 agent 必须落盘，并且熬过后续的每一次状态写入。
//
// ★ 这里钉的是一类具体的 bug：曾经有一条状态写入路径构造裸 `state{…}`，
// 于是任何一次写入都会把 state.json 里其它字段抹掉。加 agent_id 之前
// 它的症状只是「标题缓存偶尔失效」，加上之后会变成「切了 task，下一轮
// 又跑回 main」—— 而且两边都自洽，很难查。
//
// ★ 那条路径（游标推进）已随 v1 下线一起删了，但**同类写入路径还在**
// （写待批），所以这条断言继续有效。
func TestAgentIDSurvivesApprovalWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(message.NewUser("do the thing")); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetAgentID("worker"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{
			InterruptID: "int-7",
			Detail:      &contract.InterruptDetail{ToolName: "write_file"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AgentID(); got != "worker" {
		t.Fatalf("agent id was dropped by a later state write: %q", got)
	}
	if reloaded.Title() == "" {
		t.Fatal("title was dropped by a later state write")
	}
	pending, err := reloaded.PendingApproval()
	if err != nil || pending == nil || pending.Interrupts[0].InterruptID != "int-7" {
		t.Fatalf("pending approval was dropped: %v / %+v", err, pending)
	}
	if pending.Interrupts[0].Detail == nil || pending.Interrupts[0].Detail.ToolName != "write_file" {
		t.Fatalf("the approval detail was dropped: %+v", pending.Interrupts[0].Detail)
	}
}

// 加字段之前写下的 state.json 没有 agent_id：读成空串（= main），不报错。
func TestLegacyStateWithoutAgentIDReadsEmpty(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})

	sess, _ := fm.Create(ctx, "")
	if err := sess.SetAgentID("worker"); err != nil {
		t.Fatal(err)
	}

	// 还原成旧格式：字段为空时 json 里就没有这个键。
	st, _, err := readState(statePath(root, sess.ID()))
	if err != nil {
		t.Fatal(err)
	}
	st.AgentID = ""
	if err := writeStateFile(statePath(root, sess.ID()), &st); err != nil {
		t.Fatal(err)
	}

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatalf("a legacy state file must still load: %v", err)
	}
	if got := reloaded.AgentID(); got != "" {
		t.Fatalf("a legacy state must read as empty (= main), got %q", got)
	}
}

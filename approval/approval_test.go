package approval

import (
	"bytes"
	"encoding/gob"
	"testing"
)

// ★ 决定载荷要进 checkpoint（agentkit/run.FileStore 用 gob），所以 gob 必须认识
//
//	它的**具体类型**。
//
//	缺了注册，失败发生在**停机提交 checkpoint 的那一刻**，表现为
//	「模型说要调工具 → 直接 RUN_ERROR」—— 看起来像模型出错，实际是编码器。
func TestDecisionSurvivesGob(t *testing.T) {
	in := map[string]any{"decision": &Decision{Approved: false, Reason: "not now"}}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out map[string]any
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, ok := out["decision"].(*Decision)
	if !ok {
		t.Fatalf("round trip lost the concrete type: %T", out["decision"])
	}
	// ★ 关键：拒绝（Approved=false）必须**原样**回来。
	//
	//	零值就是 false，所以「没填」与「拒绝」在结构上分不开 ——
	//	这正是把「拒绝」当成「没有决定」会出错的根源。
	if got.Approved || got.Reason != "not now" {
		t.Fatalf("decision not carried through: %+v", got)
	}
}

// ★ Approved=false 是一个**有效决定**，不是「没有决定」。
//
//	闸门那边必须能区分「人拒绝了」与「答复丢了」—— 前者照常把拒绝语回给
//	模型，后者要**拒绝执行**（fail closed）。两者的区别不在这个结构里，
//	而在闸门读它时有没有拿到数据。
func TestRejectionIsAValidDecision(t *testing.T) {
	d := Decision{Approved: false}
	if d.Approved {
		t.Fatal("a rejection must not read as an approval")
	}
	if d.Reason != "" {
		t.Fatalf("an empty reason must stay empty, got %q", d.Reason)
	}
}

package transport

import (
	"bytes"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"

	"github.com/ouranoshong/openheron/contract"
)

// renderSession feeds the channel to the session renderer and decodes it.
func renderSession(t *testing.T, evs ...contract.RunEvent) []events.Event {
	t.Helper()
	var buf bytes.Buffer
	ch := make(chan contract.RunEvent, len(evs)+1)
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	if err := RenderSession(&buf, "thread-1", ch); err != nil {
		t.Fatalf("RenderSession: %v", err)
	}
	return decode(t, buf.Bytes())
}

// customOf finds the custom frame with the given name.
func customOf(t *testing.T, evs []events.Event, name string) map[string]any {
	t.Helper()
	for _, ev := range evs {
		custom, ok := ev.(*events.CustomEvent)
		if !ok || custom.Name != name {
			continue
		}
		value, _ := custom.Value.(map[string]any)
		return value
	}
	names := make([]string, len(evs))
	for i, e := range evs {
		names[i] = string(e.Type())
	}
	t.Fatalf("the frame %q never reached the wire: %v", name, names)
	return nil
}

// The answer to an approval goes out on the session's channel.
//
// ★ It is a **custom** frame: AG-UI has no vocabulary for "an approval was
//
//	answered", and its own frames are about a run — while this fact belongs
//	to the session and outlives the run that answered it.
func TestTheAnswerToAnApprovalIsRenderedAsACustomFrame(t *testing.T) {
	evs := renderSession(t, contract.RunEvent{
		Kind:        contract.RunEventApprovalResolved,
		RunID:       "run-2",
		InterruptID: "int-1",
		Approved:    true,
		Reason:      "it is my own config",
	})

	value := customOf(t, evs, "approval_resolved")
	if value["interrupt_id"] != "int-1" {
		t.Fatalf("the frame names the wrong approval: %v", value)
	}
	if value["approved"] != true {
		t.Fatalf("the frame must carry the decision: %v", value)
	}
	if value["reason"] != "it is my own config" {
		t.Fatalf("the frame must carry the reason: %v", value)
	}
	if value["run_id"] != "run-2" {
		t.Fatalf("the frame must name the answering run: %v", value)
	}
}

// The round that began goes out on the same channel.
//
// ★ The kind is a **name**, not a number: the frame outlives the process
//
//	that wrote it.
func TestTheRoundThatBeganIsRenderedAsACustomFrame(t *testing.T) {
	evs := renderSession(t, contract.RunEvent{
		Kind:     contract.RunEventRunStarted,
		RunID:    "run-3",
		TurnKind: contract.TurnResume,
	})

	value := customOf(t, evs, "run_started")
	if value["run_id"] != "run-3" {
		t.Fatalf("the frame names the wrong run: %v", value)
	}
	if value["kind"] != "resume" {
		t.Fatalf("the frame must carry the kind's name, got %v", value["kind"])
	}
}

// The session's channel says nothing about a run's life.
//
// ★ There is no run here, so RUN_STARTED and RUN_FINISHED would both be
//
//	lies: the first says "a run began" and the second says "a run ended",
//	and neither is true of a channel that outlives every run on the session.
func TestTheSessionChannelSaysNothingAboutARunsLife(t *testing.T) {
	evs := renderSession(t,
		contract.RunEvent{Kind: contract.RunEventRunStarted, RunID: "run-1", TurnKind: contract.TurnQuery},
		contract.RunEvent{Kind: contract.RunEventApprovalResolved, RunID: "run-2", InterruptID: "int-1", Approved: true},
	)

	for _, ev := range evs {
		switch ev.Type() {
		case events.EventTypeRunStarted, events.EventTypeRunFinished:
			t.Fatalf("the session's channel must not carry a run frame, got %v", ev.Type())
		}
	}
}

// The channel has no terminal frame.
//
// ★ It ends when the session goes away or the connection drops, and the
//
//	client reads that as "come back to the scan" — not as "the session is
//	over". ★ A terminal frame would say the session ended, which is a lie
//	about a session that is still there.
func TestTheSessionChannelHasNoTerminalFrame(t *testing.T) {
	evs := renderSession(t, contract.RunEvent{
		Kind:        contract.RunEventApprovalResolved,
		RunID:       "run-2",
		InterruptID: "int-1",
		Approved:    true,
	})

	for _, ev := range evs {
		if ev.Type() == events.EventTypeRunFinished {
			t.Fatal("the session's channel must not close with a terminal frame")
		}
	}
}

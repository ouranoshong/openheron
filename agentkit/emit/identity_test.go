package emit

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
)

// collectingSink keeps the derived events.
type collectingSink struct{ events []contract.RunEvent }

func (s *collectingSink) Publish(ev contract.RunEvent) { s.events = append(s.events, ev) }

// sliceSource feeds a fixed list of agent events.
type sliceSource struct {
	items []*adk.TypedAgentEvent[*schema.Message]
}

func (s *sliceSource) Next() (*adk.TypedAgentEvent[*schema.Message], bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	ev := s.items[0]
	s.items = s.items[1:]
	return ev, true
}

// ★ The id the stream used must be the id the transcript holds.
//
//	The client keys a message by that id. A message that streams under one
//	id and is archived under another is two messages to the client, and the
//	conversation rebuilds itself on every reload.
func TestTheArchivedMessageCarriesTheStreamID(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](0)
	go func() {
		defer sw.Close()
		sw.Send(schema.AssistantMessage("hello", nil), nil)
	}()

	src := &sliceSource{items: []*adk.TypedAgentEvent[*schema.Message]{
		{
			Output: &adk.AgentOutput{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role:          schema.Assistant,
					IsStreaming:   true,
					MessageStream: sr,
				},
			},
		},
	}}

	sink := &collectingSink{}
	arts, err := Derive(src, Options{RunID: "run-1"}, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts.Intermediates) == 0 {
		t.Fatal("the run must have produced a message")
	}

	var streamedID string
	for _, ev := range sink.events {
		if ev.Kind == contract.RunEventTextStart && ev.MsgID != "" {
			streamedID = ev.MsgID
		}
	}
	if streamedID == "" {
		t.Fatal("the stream must have announced a message")
	}

	archived := arts.Intermediates[0]
	got := message.MessageID(archived)
	t.Logf("streamed id=%s archived id=%s", streamedID, got)
	if got != streamedID {
		t.Fatalf("the archived message carries id %q but the stream used %q", got, streamedID)
	}
}

// ★ A message that never streams still gets an id of its own.
func TestANonStreamingMessageCarriesAnID(t *testing.T) {
	src := &sliceSource{items: []*adk.TypedAgentEvent[*schema.Message]{
		{
			Output: &adk.AgentOutput{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role:    schema.Assistant,
					Message: schema.AssistantMessage("hi", nil),
				},
			},
		},
	}}

	sink := &collectingSink{}
	arts, err := Derive(src, Options{RunID: "run-1"}, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts.Intermediates) == 0 {
		t.Fatal("the run must have produced a message")
	}
	if !message.HasMessageID(arts.Intermediates[0]) {
		t.Fatal("the archived message must carry an id")
	}
}

var _ = context.Background

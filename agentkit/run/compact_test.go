package run

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// stubSummarizer answers with a fixed summary and remembers what it was
// asked to summarize.
type stubSummarizer struct {
	content string
	err     error
	called  int
	seen    []*schema.Message
}

func (s *stubSummarizer) Summarize(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
	s.called++
	s.seen = msgs
	if s.err != nil {
		return nil, s.err
	}
	return &schema.Message{Role: schema.User, Content: s.content}, nil
}

// conversation builds a session whose last request carried usage.
func conversation(t *testing.T, messages int, totalTokens int) *fakeSession {
	t.Helper()
	sess := &fakeSession{id: "s-1"}
	for i := 0; i < messages; i++ {
		role := schema.User
		if i%2 == 1 {
			role = schema.Assistant
		}
		msg := &schema.Message{Role: role, Content: "line"}
		if role == schema.Assistant {
			msg.ResponseMeta = &schema.ResponseMeta{
				Usage: &schema.TokenUsage{TotalTokens: totalTokens},
			}
		}
		if err := sess.Append(msg); err != nil {
			t.Fatal(err)
		}
	}
	return sess
}

// The manual compaction writes the same boundary the threshold writes: the
// summary is appended, it carries the marker, and the effective history
// starts at it.
func TestTheManualCompactionWritesTheBoundary(t *testing.T) {
	sess := conversation(t, 6, 20000)
	sum := &stubSummarizer{content: "the conversation so far"}

	res, err := Compact(context.Background(), sess, sum)
	if err != nil {
		t.Fatal(err)
	}
	if sum.called != 1 {
		t.Fatalf("the summarizer was called %d times, want 1", sum.called)
	}
	if len(sum.seen) != 6 {
		t.Fatalf("the summarizer saw %d messages, want the whole effective history", len(sum.seen))
	}

	// The summary is the last thing in the transcript, and it carries the
	// marker.
	if sess.Len() != 7 {
		t.Fatalf("the transcript holds %d messages, want 7", sess.Len())
	}
	last := sess.Messages(false)[6]
	mark, ok := message.ReadCompaction(last)
	if !ok {
		t.Fatal("the appended message carries no compaction marker")
	}
	if mark.Superseded != 6 {
		t.Fatalf("superseded = %d, want 6", mark.Superseded)
	}
	if mark.SavedTokens <= 0 {
		t.Fatalf("savedTokens = %d, want a positive estimate", mark.SavedTokens)
	}
	if res.Superseded != 6 || res.SavedTokens != mark.SavedTokens {
		t.Fatalf("the result = %+v, want it to match the marker", res)
	}

	// The effective history is the summary alone.
	effective := sess.Messages(true)
	if len(effective) != 1 {
		t.Fatalf("the effective history holds %d messages, want the summary alone", len(effective))
	}
	if effective[0].Content != "the conversation so far" {
		t.Fatalf("the effective history starts with %q", effective[0].Content)
	}
}

// A history of a few messages is not worth a summary: the summary carries a
// preamble, so it can come out longer than what it replaced.
func TestTheManualCompactionRefusesATinyHistory(t *testing.T) {
	sess := conversation(t, 3, 20000)

	_, err := Compact(context.Background(), sess, &stubSummarizer{content: "summary"})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
	if sess.Len() != 3 {
		t.Fatalf("the transcript grew to %d messages", sess.Len())
	}
}

// The same holds for a context that is small in tokens, however many
// messages it has.
func TestTheManualCompactionRefusesATinyContext(t *testing.T) {
	sess := conversation(t, 8, 100)

	_, err := Compact(context.Background(), sess, &stubSummarizer{content: "summary"})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
	if sess.Len() != 8 {
		t.Fatalf("the transcript grew to %d messages", sess.Len())
	}
}

// Without a summarizer there is nothing to do, and nothing is written.
func TestTheManualCompactionRefusesWithoutASummarizer(t *testing.T) {
	sess := conversation(t, 6, 20000)

	if _, err := Compact(context.Background(), sess, nil); err == nil {
		t.Fatal("a compaction without a summarizer must fail")
	}
	if sess.Len() != 6 {
		t.Fatalf("the transcript grew to %d messages", sess.Len())
	}
}

// The summary covers the agent's own context: the sub-agents' messages are
// not part of it, and putting them into the summary would carry chatter the
// agent never read.
func TestTheManualCompactionSkipsTheSubagentsMessages(t *testing.T) {
	sess := conversation(t, 6, 20000)
	// A message the sub-agent produced: it is in the transcript, and it is
	// not in the root agent's context.
	sub := &schema.Message{Role: schema.Assistant, Content: "the sub-agent's own output"}
	message.StampAgent(sub, "coder")
	message.StampSubagent(sub)
	if err := sess.Append(sub); err != nil {
		t.Fatal(err)
	}

	sum := &stubSummarizer{content: "summary"}
	if _, err := Compact(context.Background(), sess, sum); err != nil {
		t.Fatal(err)
	}
	for _, m := range sum.seen {
		if m.Content == "the sub-agent's own output" {
			t.Fatal("the summary must not cover the sub-agent's messages")
		}
	}
}

// A failing summarizer leaves the transcript alone: a half-written boundary
// would cut the history at a message that says nothing.
func TestAFailedCompactionWritesNothing(t *testing.T) {
	sess := conversation(t, 6, 20000)

	_, err := Compact(context.Background(), sess, &stubSummarizer{err: errors.New("boom")})
	if err == nil {
		t.Fatal("a failed summarizer must fail the compaction")
	}
	if sess.Len() != 6 {
		t.Fatalf("the transcript grew to %d messages", sess.Len())
	}
}

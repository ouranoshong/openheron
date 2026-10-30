package run

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// Summarizer turns a message list into the summary a compaction produces.
//
// It is an interface so this package never imports the agent package: the
// builder owns the model, and the builder is injected.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []*schema.Message) (*schema.Message, error)
}

// The manual compaction refuses to work on a context that is too small to
// be worth compressing.
//
// The summary carries a preamble and the user's own messages back, so a
// short conversation can come out **longer** than it went in — a compaction
// that does not shrink the context is a net loss, and the user would be
// paying for it.
const (
	// MinCompactMessages is the smallest number of effective messages worth
	// summarizing.
	MinCompactMessages = 4
	// MinCompactTokens is the smallest estimated context worth summarizing.
	MinCompactTokens = 4096
)

var (
	// ErrNothingToCompact says the context is too small to be compacted.
	ErrNothingToCompact = errors.New("there is nothing worth compacting yet")
	// ErrCompactionRunning says a turn is in flight.
	ErrCompactionRunning = errors.New("a run is in progress")
)

// CompactResult is what a manual compaction achieved.
type CompactResult struct {
	// Superseded is how many transcript messages the summary replaced.
	Superseded int
	// SavedTokens is the estimated number of tokens the compaction removed
	// from the next request.
	SavedTokens int
	// UsedTokens is the estimated size of the context that is left.
	UsedTokens int
	// At is when the compaction happened.
	At time.Time
}

// Compact summarizes the session's effective history now and makes the
// summary the new boundary.
//
// It is the manual counterpart of the threshold: the same summary, the same
// marker, the same transcript — only the trigger is the user instead of the
// token count.
//
// The summary is appended at the **end** of the transcript. The automatic
// compaction goes before the turn's own messages because it summarizes
// everything before them; here there are no turn messages, and the
// compaction is the last thing that happened.
func Compact(ctx context.Context, sess storage.Session, sum Summarizer) (*CompactResult, error) {
	if sess == nil {
		return nil, errors.New("no session")
	}
	if sum == nil {
		return nil, errors.New("no summarizer")
	}

	history := sess.Messages(true)
	if len(history) < MinCompactMessages {
		return nil, fmt.Errorf("%w: only %d messages", ErrNothingToCompact, len(history))
	}
	baseline := baselineTokens(sess)
	if baseline < MinCompactTokens {
		return nil, fmt.Errorf("%w: the context holds about %d tokens", ErrNothingToCompact, baseline)
	}

	// ★ The summary covers the same messages the automatic compaction would
	// summarize: the agent's own context, without the sub-agents' messages.
	// Summarizing more would put chatter the agent never read into the
	// summary it carries forward.
	summary, err := sum.Summarize(ctx, withoutSubagentMessages(history))
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, errors.New("the summarizer produced no summary")
	}

	at := time.Now().UTC()
	superseded := sess.Len()
	saved := savedTokens(sess, summary)
	message.StampCompaction(summary, message.Compaction{
		Superseded:  superseded,
		SavedTokens: saved,
		At:          at,
	})
	message.StampNow(summary)
	if agent := sess.AgentID(); agent != "" {
		message.StampAgent(summary, agent)
	}
	if err := sess.Append(summary); err != nil {
		return nil, err
	}

	return &CompactResult{
		Superseded:  superseded,
		SavedTokens: saved,
		UsedTokens:  len(message.Text(summary)) / 4,
		At:          at,
	}, nil
}

// baselineTokens is the token count of the last request that carried usage.
//
// It is the same number eino's own counter uses, and the same one the
// meter shows: the size of the most recent request, which is what the
// context's occupancy is measured against.
func baselineTokens(sess storage.Session) int {
	baseline := 0
	for _, m := range sess.Messages(false) {
		if m == nil || m.Role != schema.Assistant ||
			m.ResponseMeta == nil || m.ResponseMeta.Usage == nil {
			continue
		}
		baseline = m.ResponseMeta.Usage.TotalTokens
	}
	return baseline
}

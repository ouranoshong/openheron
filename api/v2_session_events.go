package api

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/ouranoshong/openheron/transport"
)

// ── GET /v2/sessions/{threadId}/events ──────────────────────────────────────

// sessionPoll is how often the session's channel checks whether it is still
// worth holding.
//
// ★ It only reads a flag, so it is cheap — but it is per open tab, and the
//
//	channel is idle most of the time.
const sessionPoll = 250 * time.Millisecond

// v2SessionEvents streams the session's announcements.
//
// ★ This is the session's channel, not a run's. A run's stream ends with the
//
//	run, and the approval outlives the run that raised it: the round ends,
//	the question stays — and the surface waiting on the question has no
//	stream left to hear that it was answered.
//
// ★ The facts are **notifications**, not the truth. The truth is the scan
//
//	(GET /v2/sessions/{id}/runs) and the session's own state. ★ A client
//	subscribes **first** and scans after: whatever happened before the scan
//	is in the scan, and whatever happened after it arrives here.
//
// ★ The channel has no terminal frame. It ends when the client goes away,
//
//	and the client reads that as "come back to the scan" — not as "the
//	session is over".
func (s *Server) v2SessionEvents(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}

	stream := s.newStream(c)
	kaStop := make(chan struct{})
	defer close(kaStop)
	// ★ The keepalive is the disconnect probe: it is the only thing that
	//   writes to an idle channel, and a write to a connection that went
	//   away is what tells the handler to stop.
	go stream.Keepalive(kaStop, s.sessionKeepaliveOrDefault())

	sub := s.rt.Announce(sess.ID())
	defer sub.Close()
	defer s.rt.ReleaseAnnounce(sess.ID())

	// ★ The subscription is in place, and only now the client is told that
	//   the channel is open. ★ The ping is what makes its fetch resolve:
	//   a fetch does not resolve until the first byte arrives, and the
	//   channel has nothing to say until a fact shows up.
	stream.Ping()

	// ★ The channel outlives the request only as long as the client reads
	//   it. ★ A dead connection is noticed by the keepalive's failed write,
	//   and the subscription closes with it — otherwise the handler would
	//   sit on a channel nobody reads, for ever.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(sessionPoll)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				sub.Close()
				return
			case <-stop:
				return
			case <-tick.C:
				if stream.Broken() {
					sub.Close()
					return
				}
			}
		}
	}()

	transport.RenderSession(stream, sess.ID(), sub.Events())
	stream.Flush()
}

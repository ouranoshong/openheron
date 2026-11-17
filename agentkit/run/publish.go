package run

import (
	"sync"
	"sync/atomic"

	"github.com/ouranoshong/openheron/contract"
)

// windowSize is how many events the run keeps for a late subscriber.
//
// ★ It is a bound, not a default. An unwatched turn pays a constant cost for
//
//	a log nobody reads, and a long turn costs no more than a short one.
//
// ★ "Coming back later" is the transcript's job — a client that opens a
//
//	session hydrates from the finished messages. The window serves only
//	"attaching while it is still running".
const windowSize = 1000

// subscriberQueue is how many events a subscriber may fall behind by.
//
// ★ It is the window plus headroom. A subscriber that joins late has the
// **whole** window replayed into its queue, and it must still have room for
// the events that follow: a queue that only fits the replay drops the
// subscriber on its very next event — and that event could be the approval.
const subscriberQueue = windowSize + 64

// publisher is the run's event bus.
//
// ★ The run publishes; the connections subscribe. Nobody publishes to a
//
//	connection directly — that is what makes the connection a view of the
//	run instead of its owner.
type publisher struct {
	mu     sync.Mutex
	window []contract.RunEvent
	subs   map[*subscription]struct{}
	closed bool

	// replay says whether a late subscriber gets the window.
	//
	// ★ A run's bus replays: a client that attaches while the run is still
	//   going must not miss the approval the run raised. The session's bus
	//   does not: its facts are live notifications, and "what happened
	//   before you arrived" is the scan's job — the ledger holds it.
	replay bool

	// onProgress is called when an event is published.
	//
	// ★ It is the run's liveness signal: the lease is renewed by
	//	progress, and by nothing else.
	onProgress func()
}

func newPublisher() *publisher {
	return &publisher{subs: map[*subscription]struct{}{}, replay: true}
}

// newLivePublisher is a bus without a window: a subscriber gets what comes
// after it, and nothing before.
//
// ★ It is the session's announcement channel. The facts it carries are
//
//	notifications, and the durable past is the ledger's — a surface that
//	was not listening hydrates from the scan, not from a replay.
func newLivePublisher() *publisher {
	return &publisher{subs: map[*subscription]struct{}{}}
}

// empty reports whether the bus has no subscribers left.
//
// ★ It is what lets the session's bus be dropped when its last reader goes:
//
//	a bus kept for a session nobody watches is a leak with one entry per
//	session the process ever saw.
func (p *publisher) empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs) == 0
}

// watcherCount reports how many subscribers the bus has.
func (p *publisher) watcherCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs)
}

// Publish hands one event to the window and to every subscriber.
//
// ★ It never blocks. A subscriber whose queue is full is dropped: its
//
//	connection is not reading, and a run that waits for a slow reader is a
//	run that stalls because somebody's connection is slow — the very
//	coupling the run's own consumption removes.
func (p *publisher) Publish(ev contract.RunEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.window = append(p.window, ev)
	if len(p.window) > windowSize {
		p.window = p.window[len(p.window)-windowSize:]
	}
	if p.onProgress != nil {
		p.onProgress()
	}
	for sub := range p.subs {
		if !sub.push(ev) {
			// ★ The subscriber is gone. It stopped reading, and the run
			//   does not wait for it.
			//
			// ★ But its stream was **cut**, not finished, and the two must
			//   not look alike on the wire: the renderer closes a finished
			//   stream with a terminal frame, and a client that reads
			//   "success" stops looking — so an approval raised after this
			//   point would never be shown until a manual reload. The
			//   subscriber marks itself, and the renderer leaves the stream
			//   open-ended instead; the client's own "the stream ended with
			//   no terminal frame" path then re-attaches or reloads.
			sub.truncated.Store(true)
			delete(p.subs, sub)
			sub.close()
		}
	}
}

// Subscribe registers a subscriber and replays the window into it.
//
// ★ The replay and the registration happen under one lock, so a subscriber
//
//	sees every event exactly once: the ones from the window, then the ones
//	that follow.
//
// ★ A subscriber that arrives **after** the run closed still gets the
//
//	window. The connection subscribes after the turn signals ready, and
//	the run may finish in between — an approval raised in that window
//	would otherwise be dropped, and the client would see a run that
//	finished without ever having asked.
func (p *publisher) Subscribe() *subscription {
	sub := &subscription{p: p, ch: make(chan contract.RunEvent, subscriberQueue)}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.replay {
		for _, ev := range p.window {
			sub.ch <- ev
		}
	}
	if p.closed {
		// ★ The run is over: no more events will come, so the channel
		//   closes — but the ones in the window are already in it.
		sub.close()
		return sub
	}
	p.subs[sub] = struct{}{}
	return sub
}

// remove detaches a subscriber.
func (p *publisher) remove(sub *subscription) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.subs[sub]; ok {
		delete(p.subs, sub)
		sub.close()
	}
}

// Close ends the run's publishing.
//
// ★ The window **stays**: a subscriber that arrives after the run closed
//
//	still needs the events that were published — the approval the run
//	raised, above all, because that is the one the client cannot do
//	without. ★ The subscribers themselves are closed: the run is over, and
//	no more events will come for them.
func (p *publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for sub := range p.subs {
		delete(p.subs, sub)
		sub.close()
	}
}

// subscription is one view of the run's events.
type subscription struct {
	p    *publisher
	ch   chan contract.RunEvent
	once sync.Once

	// truncated says this stream was cut short, not finished.
	//
	// ★ The difference is the terminal frame. A run that ended normally
	//   closes its subscribers and lets the renderer say "finished"; one
	//   that dropped a subscriber must not, because the subscriber did not
	//   see the end of the run — and the end is where the approval lives.
	truncated atomic.Bool
}

// Events is the subscriber's channel.
//
// ★ It closes when the run ends — that is how the renderer learns that the
//
//	stream is over.
func (s *subscription) Events() <-chan contract.RunEvent { return s.ch }

// Truncated reports whether the run cut this subscriber off.
//
// ★ It is read after the channel closes, and the flag is set before the
// close — so the answer describes this stream, not a later one.
func (s *subscription) Truncated() bool { return s.truncated.Load() }

// Close detaches the subscriber.
//
// ★ The subscription must be closed when its reader is gone: a publisher
//
//	that pushes into a closed channel would take the run down with it.
func (s *subscription) Close() {
	if s.p != nil {
		s.p.remove(s)
		return
	}
	s.close()
}

// push hands one event to the subscriber.
//
// ★ It never blocks: a full queue means the subscriber is not reading.
func (s *subscription) push(ev contract.RunEvent) bool {
	select {
	case s.ch <- ev:
		return true
	default:
		return false
	}
}

func (s *subscription) close() {
	s.once.Do(func() { close(s.ch) })
}

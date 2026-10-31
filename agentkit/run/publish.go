package run

import (
	"sync"

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
// ★ It matches the window: a subscriber that joins late has the window
//
//	replayed into its queue, and the queue has to hold it.
const subscriberQueue = windowSize

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

	// onProgress is called when an event is published.
	//
	// ★ It is the run's liveness signal: the lease is renewed by
	//	progress, and by nothing else.
	onProgress func()
}

func newPublisher() *publisher {
	return &publisher{subs: map[*subscription]struct{}{}}
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
func (p *publisher) Subscribe() *subscription {
	sub := &subscription{p: p, ch: make(chan contract.RunEvent, subscriberQueue)}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		sub.close()
		return sub
	}
	for _, ev := range p.window {
		sub.ch <- ev
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
// ★ The window goes with it: the run is over, and a late subscriber gets
//
//	nothing rather than a tail that no longer has a run to belong to.
func (p *publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.window = nil
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
}

// Events is the subscriber's channel.
//
// ★ It closes when the run ends — that is how the renderer learns that the
//
//	stream is over.
func (s *subscription) Events() <-chan contract.RunEvent { return s.ch }

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

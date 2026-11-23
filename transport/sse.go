package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/http1/resp"
	"github.com/hertz-contrib/sse"
)

// DefaultKeepalive is the interval between comment frames on an idle stream.
const DefaultKeepalive = 15 * time.Second

// The response headers an SSE stream carries.
//
// ★ They are set here rather than by the sse package's own stream, because
//
//	that stream flushes after every event — and one flush is one write to
//	the socket. ★ A burst of twenty deltas would be twenty writes.
const (
	cacheControl = "Cache-Control"
	noCache      = "no-cache"
)

// The queue between the producers and the writer.
const (
	// queueDepth is how many frames may wait for the writer.
	queueDepth = 64
	// maxBatch is how many frames one write may carry.
	maxBatch = 64
)

// The answers a producer gets when the frame cannot go out.
var (
	// errStreamBroken says the connection is gone.
	errStreamBroken = errors.New("the stream is broken")
	// errStreamClosed says the stream was closed before the frame went out.
	errStreamClosed = errors.New("the stream is closed")
)

// frame is one item in the queue.
//
// ★ A nil event is a barrier: it carries no bytes and only says "everything
//
//	before me has gone out".
type frame struct {
	event *sse.Event
	ack   chan error
}

// SSEStream publishes newline-delimited payloads as SSE data frames.
// A renderer writes plain JSONL to it; the SSEStream buffers partial lines and
// publishes one frame per complete line, so a frame never carries half a
// record.
//
// ★ **One writer owns the connection.** The producers — the renderer, the
//
//	keepalive, the flush — hand frames to a writer goroutine, and that
//	goroutine is the only thing that touches the response writer. ★ The
//	reason is hertz's connection writer: it keeps a buffer list that
//	`net.Buffers.WriteTo` **consumes**, so a second writer can empty the
//	list underneath the first one and the process dies inside Go's writev
//	with "index out of range [0] with length 0". ★ One writer makes that
//	impossible by construction, not by discipline.
type SSEStream struct {
	// w is the response writer. ★ Only the writer goroutine touches it.
	w network.ExtWriter

	// frames is the queue from the producers to the writer.
	frames chan frame

	// broken is the connection's verdict: a write failed.
	broken atomic.Bool
	// brokenCh is closed when the writer gives up, so a producer blocked
	// on a full queue learns that nobody is going to drain it.
	brokenCh chan struct{}
	// closedCh is closed when the writer goroutine is gone.
	closedCh chan struct{}
	// done is closed when the writer goroutine has returned.
	done chan struct{}

	// mu is the producer's lock. ★ It guards the line buffer and the
	// queue's lifecycle, and it keeps the frames in the order they were
	// written.
	mu     sync.Mutex
	buf    []byte
	closed bool

	closeOnce sync.Once
}

// New opens an SSE stream on the request context.
//
// ★ The writer is ours, not the sse package's: the frames are encoded into
//
//	it and flushed once per batch. ★ One flush is one write to the socket,
//	so a flush per event is a write per event — and the model's output
//	arrives in bursts.
func New(c *app.RequestContext) *SSEStream {
	w := resp.NewChunkedBodyWriter(&c.Response, c.GetWriter())
	setHeaders(&c.Response)
	c.Response.HijackWriter(w)
	return newStream(w)
}

// NewWithWriter opens an SSE stream onto a caller-supplied writer instead of the
// connection. Tests use it to capture frames without a live socket.
func NewWithWriter(c *app.RequestContext, w network.ExtWriter) *SSEStream {
	setHeaders(&c.Response)
	c.Response.HijackWriter(w)
	return newStream(w)
}

// setHeaders marks the response as an event stream.
//
// ★ The sse package's own stream sets these too, but this stream does not
//
//	use it: the writer is ours, so the headers are ours as well.
func setHeaders(r *protocol.Response) {
	r.Header.SetContentType(sse.ContentType)
	if r.Header.Get(cacheControl) == "" {
		r.Header.Set(cacheControl, noCache)
	}
}

// newStream starts the writer goroutine.
func newStream(w network.ExtWriter) *SSEStream {
	s := &SSEStream{
		w:        w,
		frames:   make(chan frame, queueDepth),
		brokenCh: make(chan struct{}),
		closedCh: make(chan struct{}),
		done:     make(chan struct{}),
	}
	go s.write()
	return s
}

// Broken reports whether publishing has already failed. Once true the producer
// may stop caring about write errors and simply keep draining.
func (s *SSEStream) Broken() bool {
	return s.broken.Load()
}

// Write implements io.Writer so a renderer can be handed the stream directly.
//
// ★ The frames are queued, not written. ★ The writer goroutine owns the
//
//	connection, and the queue is what lets it merge a burst into one
//	write. ★ The lock here is the producer's: it guards the line buffer
//	and the queue, and it keeps the frames in the order they arrived.
func (s *SSEStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return len(p), errStreamClosed
	}
	s.buf = append(s.buf, p...)
	for {
		idx := bytes.IndexByte(s.buf, '\n')
		if idx < 0 {
			break
		}
		line := s.buf[:idx]
		s.buf = s.buf[idx+1:]
		if len(line) == 0 {
			continue
		}
		if err := s.send(&sse.Event{Data: append([]byte{}, line...)}); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

// Publish emits one JSON-encoded record.
func (s *SSEStream) Publish(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.Write(append(data, '\n'))
	return err
}

// Flush publishes any buffered partial line and waits until every frame
// queued before it has gone out.
//
// ★ It is a barrier: when it returns, the bytes are on the wire. ★ The
//
//	handler flushes at the end of the response, so this is what says the
//	last frame left before the response is finalized.
func (s *SSEStream) Flush() {
	s.mu.Lock()
	if len(s.buf) > 0 {
		line := append([]byte{}, s.buf...)
		s.buf = s.buf[:0]
		// ★ The partial line is queued under the lock: a frame that
		//   arrives after it must not overtake it.
		_ = s.send(&sse.Event{Data: line})
	}
	s.mu.Unlock()
	// ★ A barrier: the frames queued before it — the partial line
	//   included — have to be out before this returns.
	_ = s.sendWait(nil)
}

// Ping publishes one keepalive frame.
//
// ★ It is what makes a client start reading. ★ A fetch does not resolve
//
//	until the first byte arrives, so a channel that says nothing for the
//	length of the keepalive interval would leave the client waiting — and
//	a client that is not reading yet is a client that has not heard the
//	facts.
//
// ★ The frame carries no payload: it is a comment on the wire, not a fact.
//
// ★ It waits for the write. ★ The keepalive is the disconnect probe: the
//
//	only portable way to learn that the client went away is a write that
//	fails, and a write that has not happened cannot have failed.
func (s *SSEStream) Ping() {
	if s.broken.Load() {
		return
	}
	_ = s.sendWait(&sse.Event{})
}

// Close stops the writer goroutine.
//
// ★ Whatever is queued goes out first: the writer drains the queue before
//
//	it leaves. ★ The caller must close before the response is finalized —
//	hertz hands the connection back to its pool right after, and a frame
//	that goes out then lands on a socket that now belongs to another
//	request.
func (s *SSEStream) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.frames)
		s.mu.Unlock()
	})
	<-s.done
}

// Keepalive emits comment frames until done closes or the stream breaks.
//
// ★ The frame is queued like every other producer's, so the writer
//
//	goroutine stays the only thing that touches the connection. ★ A
//	second writer would empty the buffer list underneath it, and the
//	process would die inside Go's writev with "index out of range [0]
//	with length 0".
func (s *SSEStream) Keepalive(done <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.Ping()
			if s.Broken() {
				return
			}
		}
	}
}

// StartKeepalive starts the keepalive goroutine and returns the function
// that stops it.
//
// ★ The stop **waits** for the goroutine to be gone. ★ The response is
//
//	finalized when the handler returns, and hertz hands the connection
//	back to its pool right after — so a frame that goes out after that
//	lands on a socket that now belongs to another request.
func (s *SSEStream) StartKeepalive(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		s.Keepalive(done, interval)
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// ── the writer ──────────────────────────────────────────────────────────────

// write is the writer goroutine: the only thing that touches the connection.
//
// ★ It drains what is already queued and writes the whole batch with one
//
//	flush — one write to the socket for a burst of frames. ★ There is no
//	delay: the batch is whatever has arrived by now, and the first frame
//	goes out at once.
func (s *SSEStream) write() {
	defer close(s.done)
	defer close(s.closedCh)
	for {
		item, ok := <-s.frames
		if !ok {
			// ★ The queue is closed and empty: everything queued went
			//   out, and the writer leaves.
			return
		}
		batch := []frame{item}
		// ★ The frames that are already queued ride along on the same
		//   write.
	drain:
		for len(batch) < maxBatch {
			select {
			case f, ok := <-s.frames:
				if !ok {
					break drain
				}
				batch = append(batch, f)
			default:
				break drain
			}
		}
		err := s.publish(batch)
		for _, f := range batch {
			if f.ack != nil {
				f.ack <- err
			}
		}
		if err != nil {
			s.fail(err)
			return
		}
	}
}

// publish writes one batch.
//
// ★ Every frame is encoded and the writer is flushed once — one write to
//
//	the socket for the whole batch.
func (s *SSEStream) publish(batch []frame) error {
	for _, f := range batch {
		if f.event == nil {
			// ★ A barrier carries no bytes.
			continue
		}
		if err := sse.Encode(s.w, f.event); err != nil {
			return err
		}
	}
	return s.w.Flush()
}

// fail records the broken connection and releases everyone waiting on it.
func (s *SSEStream) fail(err error) {
	s.broken.Store(true)
	close(s.brokenCh)
	// ★ The frames that are still queued will never go out. Their
	//   producers must be told, or a barrier would wait for ever.
	for {
		select {
		case f := <-s.frames:
			if f.ack != nil {
				f.ack <- err
			}
		default:
			return
		}
	}
}

// ── the producers ───────────────────────────────────────────────────────────

// send queues a frame. The producer's lock is held.
//
// ★ The send may block: the queue is the backpressure, and a frame that
//
//	carries a fact may not be dropped.
//
// ★ A stream that is known to be broken is refused before the send. The
//
//	select alone would not do it: the queue has room and the broken
//	channel is closed, and the select would pick one of them at random —
//	half the time the frame goes into a queue nobody drains, and the
//	producer is told nothing.
func (s *SSEStream) send(ev *sse.Event) error {
	if s.broken.Load() {
		return errStreamBroken
	}
	select {
	case s.frames <- frame{event: ev}:
		return nil
	case <-s.brokenCh:
		return errStreamBroken
	}
}

// sendWait queues a frame and waits for it to go out.
//
// ★ The producer's lock is **not** held while waiting: the wait is the
//
//	writer's turn, and the writer does not take that lock. ★ Holding it
//	would keep the other producers out for the length of a write.
func (s *SSEStream) sendWait(ev *sse.Event) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errStreamClosed
	}
	if s.broken.Load() {
		s.mu.Unlock()
		return errStreamBroken
	}
	f := frame{event: ev, ack: make(chan error, 1)}
	select {
	case s.frames <- f:
	case <-s.brokenCh:
		s.mu.Unlock()
		return errStreamBroken
	}
	s.mu.Unlock()

	select {
	case err := <-f.ack:
		return err
	case <-s.brokenCh:
		return errStreamBroken
	case <-s.closedCh:
		return errStreamClosed
	}
}

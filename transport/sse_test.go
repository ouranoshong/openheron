package transport

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/http1/resp"
)

// recordingWriter is a connection writer that records what went through it.
//
// ★ It stands in for hertz's connection writer: the frames go out through
//
//	one of those, and it keeps a buffer list that a second writer empties
//	underneath it — the list can be empty by the time the write reaches
//	the socket.
type recordingWriter struct {
	mu       sync.Mutex
	inside   int
	overlaps int
	writes   int
	flushes  int
	buf      bytes.Buffer

	// delay is how long a flush takes. ★ A slow socket is what lets a
	//   burst pile up in the queue, which is what the batching test
	//   needs.
	delay time.Duration
	// failOnFlush is the flush that fails. ★ Zero means none fails: the
	//   failure is the socket going away, and it is the flush that
	//   finds out.
	failOnFlush int
	// failErr is what the failing flush returns.
	failErr error
}

func (w *recordingWriter) enter() {
	w.mu.Lock()
	w.inside++
	if w.inside > 1 {
		w.overlaps++
	}
	w.mu.Unlock()
}

func (w *recordingWriter) leave() {
	w.mu.Lock()
	w.inside--
	w.mu.Unlock()
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.enter()
	defer w.leave()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	w.buf.Write(p)
	return len(p), nil
}

func (w *recordingWriter) Flush() error {
	w.enter()
	defer w.leave()
	// ★ The socket write is the slow part. ★ The writes that go into the
	//   buffer are not: a frame costs one flush, not one write.
	time.Sleep(w.delay)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
	if w.failOnFlush != 0 && w.flushes == w.failOnFlush {
		if w.failErr != nil {
			return w.failErr
		}
		return errors.New("the connection is gone")
	}
	return nil
}

func (w *recordingWriter) Finalize() error { return nil }

func (w *recordingWriter) counts() (overlaps, writes, flushes int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overlaps, w.writes, w.flushes
}

func (w *recordingWriter) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// newTestStream opens a stream onto a recording writer.
func newTestStream(t *testing.T) (*SSEStream, *recordingWriter) {
	t.Helper()
	w := &recordingWriter{}
	s := NewWithWriter(app.NewContext(0), w)
	t.Cleanup(s.Close)
	return s, w
}

// TestOneWriterOwnsTheConnection proves that the keepalive and the renderer
// never write to the connection at the same time.
//
// ★ Two writers on one hertz connection empty the buffer list the flush
//
//	reads: the list can be empty by the time the write reaches the
//	socket, and the process dies inside Go's writev with "index out of
//	range [0] with length 0". ★ One writer makes that impossible by
//	construction — this test is what keeps it that way.
func TestOneWriterOwnsTheConnection(t *testing.T) {
	w := &recordingWriter{delay: 5 * time.Millisecond}
	s := NewWithWriter(app.NewContext(0), w)
	defer s.Close()

	stop := s.StartKeepalive(time.Millisecond)
	defer stop()

	// The renderer writes while the keepalive ticks.
	const frames = 40
	for i := 0; i < frames; i++ {
		if _, err := s.Write([]byte(`{"type":"TEXT_MESSAGE_CONTENT"}` + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s.Flush()

	overlaps, _, flushes := w.counts()
	if overlaps > 0 {
		t.Fatalf("the keepalive and the renderer wrote to the connection %d times at once", overlaps)
	}
	if flushes == 0 {
		t.Fatal("nothing was flushed")
	}
}

// TestBurstIsMergedIntoOneWrite proves that a burst of frames costs fewer
// writes than frames.
//
// ★ Every frame used to be its own flush, and every flush is a write to
//
//	the socket. ★ The writer drains what is queued and writes the whole
//	batch at once — that is the whole point of the single writer.
func TestBurstIsMergedIntoOneWrite(t *testing.T) {
	w := &recordingWriter{delay: 20 * time.Millisecond}
	s := NewWithWriter(app.NewContext(0), w)
	defer s.Close()

	const frames = 50
	for i := 0; i < frames; i++ {
		if _, err := s.Write([]byte(fmt.Sprintf(`{"type":"TEXT_MESSAGE_CONTENT","delta":"%d"}`+"\n", i))); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s.Flush()

	_, _, flushes := w.counts()
	if flushes >= frames/2 {
		t.Fatalf("the burst of %d frames took %d writes; it was not merged", frames, flushes)
	}
}

// TestFlushIsABarrier proves that when Flush returns, the frames are out.
//
// ★ The handler flushes at the end of the response, so this is what says
//
//	the last frame left before the response is finalized.
func TestFlushIsABarrier(t *testing.T) {
	w := &recordingWriter{delay: 5 * time.Millisecond}
	s := NewWithWriter(app.NewContext(0), w)
	defer s.Close()

	const frames = 10
	for i := 0; i < frames; i++ {
		if _, err := s.Write([]byte(fmt.Sprintf(`{"type":"TEXT_MESSAGE_CONTENT","delta":"%d"}`+"\n", i))); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s.Flush()

	text := w.text()
	for i := 0; i < frames; i++ {
		want := fmt.Sprintf(`"delta":"%d"`, i)
		if !strings.Contains(text, want) {
			t.Fatalf("frame %d did not go out before the flush returned; got %q", i, text)
		}
	}
}

// TestFramesKeepTheirOrder proves that the queue does not reorder the frames.
func TestFramesKeepTheirOrder(t *testing.T) {
	w := &recordingWriter{delay: time.Millisecond}
	s := NewWithWriter(app.NewContext(0), w)
	defer s.Close()

	const frames = 25
	for i := 0; i < frames; i++ {
		if _, err := s.Write([]byte(fmt.Sprintf(`{"type":"TEXT_MESSAGE_CONTENT","delta":"%d"}`+"\n", i))); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s.Flush()

	text := w.text()
	at := 0
	for i := 0; i < frames; i++ {
		want := fmt.Sprintf(`"delta":"%d"`, i)
		idx := strings.Index(text[at:], want)
		if idx < 0 {
			t.Fatalf("frame %d is out of order or missing; got %q", i, text)
		}
		at += idx + len(want)
	}
}

// TestBrokenConnectionPropagates proves that a dead connection reaches the
// producers.
//
// ★ The producers wait on a queue that nobody drains once the writer is
//
//	gone, so a failure has to say so: a producer that waits for ever is
//	a run that never finishes.
func TestBrokenConnectionPropagates(t *testing.T) {
	w := &recordingWriter{failOnFlush: 1}
	s := NewWithWriter(app.NewContext(0), w)
	defer s.Close()

	if _, err := s.Write([]byte(`{"type":"TEXT_MESSAGE_CONTENT"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.Flush()

	if !s.Broken() {
		t.Fatal("a failed write must mark the stream broken")
	}
	// ★ The next frame must be refused, not queued for a writer that is
	//   gone.
	done := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte(`{"type":"TEXT_MESSAGE_CONTENT"}` + "\n"))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a write on a broken stream must fail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a write on a broken stream hung")
	}
}

// TestBlockedProducerIsReleasedWhenTheWriterDies proves that a producer
// waiting on a full queue learns that nobody is going to drain it.
//
// ★ The queue is the backpressure, and a full queue is the normal state
//
//	of a slow client. ★ When the connection dies instead, that wait has
//	to end — otherwise the run that is filling the queue never finishes.
func TestBlockedProducerIsReleasedWhenTheWriterDies(t *testing.T) {
	w := &recordingWriter{delay: 50 * time.Millisecond, failOnFlush: 1}
	s := NewWithWriter(app.NewContext(0), w)
	defer s.Close()

	done := make(chan error, 1)
	go func() {
		// ★ More frames than the queue holds: the producer blocks.
		for i := 0; i < queueDepth*4; i++ {
			if _, err := s.Write([]byte(`{"type":"TEXT_MESSAGE_CONTENT"}` + "\n")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the producer must be told that the writer is gone")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the producer hung on a queue nobody drains")
	}
	if !s.Broken() {
		t.Fatal("a failed write must mark the stream broken")
	}
}

// TestCloseDrainsTheQueueAndStopsTheWriter proves that Close waits for the
// writer and that the queued frames go out.
//
// ★ The handler closes before the response is finalized, and hertz hands
//
//	the connection back to its pool right after — a frame that goes out
//	then lands on another request's socket.
func TestCloseDrainsTheQueueAndStopsTheWriter(t *testing.T) {
	w := &recordingWriter{delay: 5 * time.Millisecond}
	s := NewWithWriter(app.NewContext(0), w)

	const frames = 20
	for i := 0; i < frames; i++ {
		if _, err := s.Write([]byte(fmt.Sprintf(`{"type":"TEXT_MESSAGE_CONTENT","delta":"%d"}`+"\n", i))); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s.Close()

	text := w.text()
	for i := 0; i < frames; i++ {
		want := fmt.Sprintf(`"delta":"%d"`, i)
		if !strings.Contains(text, want) {
			t.Fatalf("frame %d did not go out before the close returned; got %q", i, text)
		}
	}
	// ★ After the close, the writer is gone: nothing more may go out.
	_, writes, _ := w.counts()
	time.Sleep(50 * time.Millisecond)
	_, after, _ := w.counts()
	if after != writes {
		t.Fatalf("the writer wrote %d bytes after the close returned", after-writes)
	}
}

// chunkedDecode turns HTTP chunked bytes into the bytes they carry.
//
// ★ The header ends at the first empty line, and every chunk is
//
//	"<hex size>\r\n<data>\r\n".
func chunkedDecode(t *testing.T, raw []byte) string {
	t.Helper()
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	if idx < 0 {
		t.Fatalf("the response has no header terminator: %q", raw)
	}
	rest := raw[idx+4:]
	var out bytes.Buffer
	for {
		nl := bytes.Index(rest, []byte("\r\n"))
		if nl < 0 {
			return out.String()
		}
		size, err := strconv.ParseInt(string(rest[:nl]), 16, 64)
		if err != nil {
			t.Fatalf("the chunk header is not a hex size: %q", rest[:nl])
		}
		rest = rest[nl+2:]
		if size == 0 {
			return out.String()
		}
		if int64(len(rest)) < size+2 {
			t.Fatalf("the chunk is short: want %d bytes, have %d", size, len(rest))
		}
		out.Write(rest[:size])
		rest = rest[size+2:]
	}
}

// TestFramesReachTheWireInSSEFraming proves that the bytes the stream hands
// to hertz are the SSE frames the client parses.
//
// ★ The api tests replace the stream's writer with a test double, so
//
//	hertz's own chunked body writer is never exercised there. ★ This is
//	that path: the frames go through hertz's writer and come out as HTTP
//	chunks, and the SSE parser sees their concatenation.
func TestFramesReachTheWireInSSEFraming(t *testing.T) {
	var raw bytes.Buffer
	r := &protocol.Response{}
	setHeaders(r)
	w := resp.NewChunkedBodyWriter(r, network.NewWriter(&raw))
	s := newStream(w)
	defer s.Close()

	const payload = `{"type":"TEXT_MESSAGE_CONTENT","delta":"hi"}`
	if _, err := s.Write([]byte(payload + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.Flush()

	head, _, _ := bytes.Cut(raw.Bytes(), []byte("\r\n\r\n"))
	if !strings.Contains(string(head), "text/event-stream") {
		t.Fatalf("the response is not an event stream: %q", head)
	}
	got := chunkedDecode(t, raw.Bytes())
	want := "data:" + payload + "\n\n"
	if got != want {
		t.Fatalf("the wire carries %q, want %q", got, want)
	}
}

// TestStreamSetsTheSSEHeaders proves that the stream sets the headers a
// client needs.
//
// ★ The sse package's own stream sets them too, but this stream does not
//
//	use it — the writer is ours, so the headers are ours as well.
func TestStreamSetsTheSSEHeaders(t *testing.T) {
	c := app.NewContext(0)
	s := New(c)
	defer s.Close()

	if got := c.Response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("the content type is %q, want %q", got, "text/event-stream")
	}
	if got := c.Response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("the cache control is %q, want %q", got, "no-cache")
	}
}

// TestKeepaliveStopsBeforeTheCallerReturns proves that the stop waits for
// the goroutine to be gone.
//
// ★ The response is finalized when the handler returns, and hertz hands
//
//	the connection back to its pool right after — so a frame that goes
//	out after that lands on a socket that now belongs to another
//	request.
func TestKeepaliveStopsBeforeTheCallerReturns(t *testing.T) {
	w := &recordingWriter{}
	s := NewWithWriter(app.NewContext(0), w)

	stop := s.StartKeepalive(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	stop()
	s.Close()

	_, writes, _ := w.counts()
	time.Sleep(50 * time.Millisecond)
	_, after, _ := w.counts()
	if after != writes {
		t.Fatalf("the keepalive wrote %d bytes after the stop returned", after-writes)
	}
}

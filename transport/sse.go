package transport

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/hertz-contrib/sse"
)

// DefaultKeepalive is the interval between comment frames on an idle stream.
const DefaultKeepalive = 15 * time.Second

// SSEStream publishes newline-delimited payloads as SSE data frames.
// A renderer writes plain JSONL to it; the SSEStream buffers partial lines and
// publishes one frame per complete line, so a frame never carries half a
// record.
type SSEStream struct {
	stream *sse.Stream

	mu     sync.Mutex
	buf    []byte
	broken bool
}

// New opens an SSE stream on the request context.
func New(c *app.RequestContext) *SSEStream {
	return &SSEStream{stream: sse.NewStream(c)}
}

// NewWithWriter opens an SSE stream onto a caller-supplied writer instead of the
// connection. Tests use it to capture frames without a live socket.
func NewWithWriter(c *app.RequestContext, w network.ExtWriter) *SSEStream {
	return &SSEStream{stream: sse.NewStreamWithWriter(c, w)}
}

// Broken reports whether publishing has already failed. Once true the producer
// may stop caring about write errors and simply keep draining.
func (s *SSEStream) Broken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.broken
}

// Write implements io.Writer so a renderer can be handed the stream directly.
func (s *SSEStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.broken {
		return len(p), nil
	}
	s.buf = append(s.buf, p...)
	for {
		idx := -1
		for i, b := range s.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := s.buf[:idx]
		s.buf = s.buf[idx+1:]
		if len(line) == 0 {
			continue
		}
		if err := s.stream.Publish(&sse.Event{Data: append([]byte{}, line...)}); err != nil {
			s.broken = true
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

// Flush publishes any buffered partial line.
func (s *SSEStream) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 || s.broken {
		return
	}
	if err := s.stream.Publish(&sse.Event{Data: append([]byte{}, s.buf...)}); err != nil {
		s.broken = true
	}
	s.buf = s.buf[:0]
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
func (s *SSEStream) Ping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return
	}
	if err := s.stream.Publish(&sse.Event{}); err != nil {
		s.broken = true
	}
}

// Keepalive emits comment frames until done closes or the stream breaks.
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
			if s.Broken() {
				return
			}
			if err := s.stream.Publish(&sse.Event{}); err != nil {
				s.mu.Lock()
				s.broken = true
				s.mu.Unlock()
				return
			}
		}
	}
}

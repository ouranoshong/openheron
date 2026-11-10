package obs

import (
	"fmt"
	"io"
	"sync"
)

// FileEnv names the environment override for the log file. It sits beside
// LevelEnv: an operator who has to see one run's log in a file should not have
// to edit the configuration to do it.
const FileEnv = "OPENHERON_LOG_FILE"

// Sink is the writer the process logger writes through.
//
// ★ The logger itself cannot be replaced. hlog.SetLogger and eino's global
//
//	handlers are registered once, before the configuration is read, and the
//	file path is only known afterwards — so the logger stays the same object
//	and its destination moves underneath it. That is this type's whole job.
//
// ★ Writes are serialised by the handler's mutex, but Set runs on the main
//
//	goroutine while a turn may be logging, so the swap is guarded here too.
type Sink struct {
	mu       sync.RWMutex
	w        io.Writer
	fallback io.Writer
}

// NewSink returns a sink writing to w. w is also the fallback: when a later
// destination fails, the sink returns to where it started rather than going
// silent. In the binary that is stderr.
func NewSink(w io.Writer) *Sink {
	return &Sink{w: w, fallback: w}
}

// Write passes the record to the current destination.
//
// ★ A destination that fails once is replaced by the fallback for the rest of
//
//	the process. Retrying a full disk on every line turns one error into a
//	flood, and the line that failed is still worth keeping — it is written to
//	the fallback before this returns.
func (s *Sink) Write(p []byte) (int, error) {
	s.mu.RLock()
	w := s.w
	s.mu.RUnlock()

	n, err := w.Write(p)
	if err == nil {
		return n, nil
	}
	if s.degrade(err) {
		return s.fallback.Write(p)
	}
	return n, err
}

// Set points the sink at w.
//
// ★ The previous writer is **not** closed here: it is a file the composition
//
//	root opened, and closing a file the logger no longer references is that
//	caller's business. The sink owns the route, not the resource.
func (s *Sink) Set(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w = w
}

// degrade moves the sink to the fallback and reports whether this call was the
// one that did it, so the notice below appears once rather than per record.
func (s *Sink) degrade(cause error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == s.fallback {
		return false
	}
	s.w = s.fallback
	// ★ Written straight to the fallback, not through the logger: the logger
	//   is what just failed, and a warn record would be one more thing to
	//   route through the broken destination.
	fmt.Fprintf(s.fallback, "[obs] the log destination failed (%v); writing to the fallback from here\n", cause)
	return true
}

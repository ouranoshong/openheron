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
//
// ★ The sink holds **two** destinations, not one. The log's route is where
//
//	the records go, and the console is where the process was started. The
//	route may leave the console — `log.file` with `log.console: false`
//	does exactly that — and a notice still has to reach the terminal, so
//	the console is kept apart from the route.
type Sink struct {
	mu      sync.RWMutex
	w       io.Writer
	console io.Writer
	file    io.Writer
}

// NewSink returns a sink writing to w. w is also the console: the process
// talks to the terminal it was started on, and a destination that fails later
// returns the sink to it rather than going silent. In the binary that is
// stderr.
func NewSink(w io.Writer) *Sink {
	return &Sink{w: w, console: w}
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
		return s.console.Write(p)
	}
	return n, err
}

// Set points the log at w.
//
// ★ The previous writer is **not** closed here: it is a file the composition
//
//	root opened, and closing a file the logger no longer references is that
//	caller's business. The sink owns the route, not the resource.
//
// ★ The file pointer is not touched: Set moves the log's route only, and
//
//	Install is what records a file. A caller that installs a file and then
//	swaps the route has said two different things, and the notice route
//	follows the file.
func (s *Sink) Set(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w = w
}

// Install moves the log to a file.
//
// ★ keepConsole keeps the terminal in the log's route: every record goes to
//
//	both. Without it the log is the file alone — that is what
//	`log.console: false` asks for, and it is the reason the file is
//	remembered apart from the route.
//
// ★ The file is remembered because a notice has to reach the terminal even
//
//	when the log has left it, and the file even when the log still covers
//	the terminal. One notice, two destinations, never the same line twice
//	on the terminal.
func (s *Sink) Install(file io.Writer, keepConsole bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = file
	if keepConsole {
		s.w = io.MultiWriter(s.console, file)
		return
	}
	s.w = file
}

// NoticeWriter returns the writer a notice goes to.
//
// ★ It is the console and the log file, and **not** the log's route: a
//
//	notice that travelled the route would be filtered away by the level,
//	and it would double up on the terminal whenever the log still covers
//	it.
func (s *Sink) NoticeWriter() io.Writer {
	return noticeWriter{sink: s}
}

// Notice writes a notice to the console and to the log file.
//
// ★ The console comes first: a file that cannot be written must not cost
//
//	the operator the line. The file's failure is reported on the console
//	and costs this notice its copy there — the log's own route is what
//	degrades, not the notice.
func (s *Sink) Notice(p []byte) (int, error) {
	s.mu.RLock()
	console, file := s.console, s.file
	s.mu.RUnlock()

	n, err := console.Write(p)
	if err != nil {
		return n, err
	}
	if file != nil {
		if _, ferr := file.Write(p); ferr != nil {
			fmt.Fprintf(console, "[obs] the notice did not reach the log file (%v)\n", ferr)
		}
	}
	return n, nil
}

// noticeWriter routes a notice through the sink.
type noticeWriter struct {
	sink *Sink
}

func (w noticeWriter) Write(p []byte) (int, error) {
	return w.sink.Notice(p)
}

// degrade moves the sink to the console and reports whether this call was the
// one that did it, so the notice below appears once rather than per record.
func (s *Sink) degrade(cause error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == s.console {
		return false
	}
	s.w = s.console
	// ★ The file is dropped with the route: the destination that failed is
	//   the file, and a notice that kept writing to it would report the
	//   same failure on every line.
	s.file = nil
	// ★ Written straight to the console, not through the logger: the logger
	//   is what just failed, and a warn record would be one more thing to
	//   route through the broken destination.
	fmt.Fprintf(s.console, "[obs] the log destination failed (%v); writing to the fallback from here\n", cause)
	return true
}

package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// timeFormat is hertz's timestamp shape: the date, the time, microseconds.
const timeFormat = "2006/01/02 15:04:05.000000"

// Handler renders records the way hertz's logger does.
//
// A line is the date, the time with microseconds, the caller's file and
// line, the level, the message and the attributes:
//
//	2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
//
// ★ slog's own text handler cannot do this. Its envelope is a list of
// attributes (`time=… level=… msg=…`) and the source is just another
// attribute; the envelope openheron wants is the standard library's log
// package shape, so the handler builds it by hand.
//
// ★ The file and the line are the **caller's**. slog captures the program
// counter of the call site in the record, and that is what this prints — a
// log line pointing into obs itself would be useless.
type Handler struct {
	w  io.Writer
	mu *sync.Mutex
	// level is read through an interface, not captured as a value: the
	// process's level has to move after the handler is built (the config is
	// read later than the logger is assembled), and every copy made by With()
	// has to move with it.
	level slog.Leveler
	// attrs are the ones With() collected; they lead the record's own.
	attrs []slog.Attr
	// group is the open WithGroup() prefix.
	group string
}

// NewHandler builds a handler that writes to w and lets levels at or above
// level through.
//
// ★ The fixed level is wrapped in a LevelVar rather than stored: callers that
// only need one level keep the old signature, and the handler keeps a single
// code path. Use NewHandlerWithLevel when the level has to move.
func NewHandler(w io.Writer, level slog.Level) *Handler {
	lv := new(slog.LevelVar)
	lv.Set(level)
	return NewHandlerWithLevel(w, lv)
}

// NewHandlerWithLevel builds a handler whose threshold is read from level on
// every record, so a LevelVar can move it after construction.
func NewHandlerWithLevel(w io.Writer, level slog.Leveler) *Handler {
	if level == nil {
		level = new(slog.LevelVar)
	}
	return &Handler{w: w, mu: &sync.Mutex{}, level: level}
}

// Enabled reports whether a level makes it into the output.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle renders one record.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	at := r.Time
	if at.IsZero() {
		at = time.Now()
	}
	b.WriteString(at.Format(timeFormat))
	b.WriteByte(' ')
	if file, line, ok := sourceOf(r.PC); ok {
		b.WriteString(file)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(line))
		b.WriteString(": ")
	}
	b.WriteByte('[')
	b.WriteString(levelTag(r.Level))
	b.WriteString("] ")
	b.WriteString(escapeMessage(r.Message))

	for _, a := range h.attrs {
		h.appendAttr(&b, a)
	}
	// ★ The open group applies to the record's own attributes too — slog
	// applies it when the record is handled, not when it is built.
	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(&b, h.resolve(a))
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write([]byte(b.String()))
	return err
}

// WithAttrs returns a handler that also writes the given attributes.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	out := h.clone()
	for _, a := range attrs {
		out.attrs = append(out.attrs, h.resolve(a))
	}
	return out
}

// WithGroup returns a handler that prefixes the attributes written after it
// with the group name.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	out := h.clone()
	if out.group == "" {
		out.group = name
	} else {
		out.group = out.group + "." + name
	}
	return out
}

// clone copies the handler. The writer and the lock are shared: they belong
// to the logger, not to the copy.
func (h *Handler) clone() *Handler {
	out := *h
	out.attrs = append([]slog.Attr(nil), h.attrs...)
	return &out
}

// resolve applies the open group to an attribute's key.
func (h *Handler) resolve(a slog.Attr) slog.Attr {
	if h.group == "" {
		return a
	}
	a.Key = h.group + "." + a.Key
	return a
}

// appendAttr writes one attribute as `key=value`.
func (h *Handler) appendAttr(b *strings.Builder, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	b.WriteByte(' ')
	b.WriteString(a.Key)
	b.WriteByte('=')
	appendValue(b, a.Value)
}

// appendValue writes a value the way slog's text handler does: a string
// that would be ambiguous on the line gets quoted, everything else is
// written as it reads.
func appendValue(b *strings.Builder, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if needsQuoting(s) {
			b.WriteString(strconv.Quote(s))
			return
		}
		b.WriteString(s)
	case slog.KindTime:
		b.WriteString(v.Time().Format(time.RFC3339Nano))
	case slog.KindDuration:
		b.WriteString(v.Duration().String())
	case slog.KindAny:
		b.WriteString(fmt.Sprintf("%+v", v.Any()))
	default:
		b.WriteString(v.String())
	}
}

// needsQuoting reports whether a string has to be quoted to stay readable
// as one field of a line.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r == ' ' || r == '=' || r == '"' || r == '\\' || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

// escapeMessage keeps a record on one line: a message with a newline in it
// would otherwise split into two lines that do not parse.
func escapeMessage(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	return strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(s)
}

// sourceOf resolves the program's counter to the file and line of the call
// site.
func sourceOf(pc uintptr) (string, int, bool) {
	if pc == 0 {
		return "", 0, false
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if frame.File == "" {
		return "", 0, false
	}
	return filepath.Base(frame.File), frame.Line, true
}

// levelTag names the level the way hertz does.
func levelTag(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "Debug"
	case l < slog.LevelWarn:
		return "Info"
	case l < slog.LevelError:
		return "Warn"
	default:
		return "Error"
	}
}

// Package hertz adapts the openheron logger to hertz's hlog.FullLogger.
//
// ★ hertz has no option that takes a logger: hlog.SetLogger is the only way
// in, it sets a process-global, and it is not safe to call once the server is
// running. The composition root installs this adapter exactly once, before
// hserver.New.
package hertz

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"

	"github.com/ouranoshong/openheron/agentkit/obs"
)

// Logger is an hlog.FullLogger backed by a *slog.Logger.
type Logger struct {
	log *slog.Logger
}

var _ hlog.FullLogger = (*Logger)(nil)

// New wraps log. A nil logger is accepted and discards everything, so a test
// never has to pass a real one.
func New(log *slog.Logger) *Logger {
	if log == nil {
		log = obs.Discard()
	}
	return &Logger{log: log}
}

// emit writes one record.
//
// ★ The record is built by hand so the caller's program counter can be
// supplied: slog.Logger.Info would capture the PC of *this* package, and every
// hertz line would point at hertz.go instead of at the code that logged it.
// Enabled is checked here because Handler.Handle bypasses Logger's own gate.
func (l *Logger) emit(ctx context.Context, level slog.Level, msg string) {
	if l.log == nil || !l.log.Enabled(ctx, level) {
		return
	}
	r := slog.NewRecord(time.Now(), level, msg, callerPC())
	_ = l.log.Handler().Handle(ctx, r)
}

// callerPC walks out of this package so the line points at the code that
// logged it.
//
// ★ Walking frames instead of a fixed runtime.Callers skip is deliberate: Go
// inlines Info and emit, and a fixed skip silently lands in testing.tRunner
// the moment the compiler decides to inline one more level.
func callerPC() uintptr {
	var pcs [16]uintptr
	n := runtime.Callers(2, pcs[:])
	if n == 0 {
		return 0
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		fn := f.Function
		adapter := strings.Contains(fn, "/hertz.(*Logger).") ||
			strings.HasSuffix(fn, "/hertz.callerPC") ||
			strings.HasSuffix(fn, "/hertz.emit")
		if !adapter {
			return f.PC
		}
		if !more {
			return 0
		}
	}
}

// Trace maps to debug.
func (l *Logger) Trace(v ...interface{}) {
	l.emit(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}

// Debug maps to debug.
func (l *Logger) Debug(v ...interface{}) {
	l.emit(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}

// Info maps to info.
func (l *Logger) Info(v ...interface{}) {
	l.emit(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}

// Notice has no openheron equivalent; it maps to info.
func (l *Logger) Notice(v ...interface{}) {
	l.emit(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}

// Warn maps to warn.
func (l *Logger) Warn(v ...interface{}) {
	l.emit(context.Background(), slog.LevelWarn, fmt.Sprint(v...))
}

// Error maps to error.
func (l *Logger) Error(v ...interface{}) {
	l.emit(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

// Fatal maps to error and does NOT exit.
//
// ★ hlog's package-level Fatal wrapper is what exits; a logger that exits on
// its own would kill a test process that only wanted to observe the line.
func (l *Logger) Fatal(v ...interface{}) {
	l.emit(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

// Tracef maps to debug.
func (l *Logger) Tracef(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelDebug, fmt.Sprintf(format, v...))
}

// Debugf maps to debug.
func (l *Logger) Debugf(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelDebug, fmt.Sprintf(format, v...))
}

// Infof maps to info.
func (l *Logger) Infof(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelInfo, fmt.Sprintf(format, v...))
}

// Noticef has no openheron equivalent; it maps to info.
func (l *Logger) Noticef(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelInfo, fmt.Sprintf(format, v...))
}

// Warnf maps to warn.
func (l *Logger) Warnf(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelWarn, fmt.Sprintf(format, v...))
}

// Errorf maps to error.
func (l *Logger) Errorf(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelError, fmt.Sprintf(format, v...))
}

// Fatalf maps to error and does NOT exit. See Fatal.
func (l *Logger) Fatalf(format string, v ...interface{}) {
	l.emit(context.Background(), slog.LevelError, fmt.Sprintf(format, v...))
}

// CtxTracef maps to debug and prefixes the run id from ctx.
func (l *Logger) CtxTracef(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelDebug, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// CtxDebugf maps to debug and prefixes the run id from ctx.
func (l *Logger) CtxDebugf(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelDebug, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// CtxInfof maps to info and prefixes the run id from ctx.
func (l *Logger) CtxInfof(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelInfo, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// CtxNoticef has no openheron equivalent; it maps to info.
func (l *Logger) CtxNoticef(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelInfo, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// CtxWarnf maps to warn and prefixes the run id from ctx.
func (l *Logger) CtxWarnf(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelWarn, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// CtxErrorf maps to error and prefixes the run id from ctx.
func (l *Logger) CtxErrorf(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelError, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// CtxFatalf maps to error, does not exit, and prefixes the run id from ctx.
func (l *Logger) CtxFatalf(ctx context.Context, format string, v ...interface{}) {
	l.emit(ctx, slog.LevelError, obs.Prefix(ctx)+fmt.Sprintf(format, v...))
}

// SetLevel is a no-op.
//
// ★ The process level is the LevelVar the composition root holds. If hertz
// could move it, a framework call would change the level of every other line.
func (l *Logger) SetLevel(hlog.Level) {}

// SetOutput is a no-op. The writer belongs to the handler the composition
// root built.
func (l *Logger) SetOutput(io.Writer) {}

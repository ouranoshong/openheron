package hertz

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/hlog"

	"github.com/ouranoshong/openheron/agentkit/obs"
)

func fixture(t *testing.T, level slog.Level) (*bytes.Buffer, *Logger) {
	t.Helper()
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(level)
	return &buf, New(obs.New(&buf, lv))
}

func TestAllMethodsWrite(t *testing.T) {
	buf, l := fixture(t, slog.LevelDebug)
	ctx := context.Background()

	l.Trace("MARK_TRACE")
	l.Debug("MARK_DEBUG")
	l.Info("MARK_INFO")
	l.Notice("MARK_NOTICE")
	l.Warn("MARK_WARN")
	l.Error("MARK_ERROR")
	l.Fatal("MARK_FATAL")

	l.Tracef("%s", "MARK_TRACEF")
	l.Debugf("%s", "MARK_DEBUGF")
	l.Infof("%s", "MARK_INFOF")
	l.Noticef("%s", "MARK_NOTICEF")
	l.Warnf("%s", "MARK_WARNF")
	l.Errorf("%s", "MARK_ERRORF")
	l.Fatalf("%s", "MARK_FATALF")

	l.CtxTracef(ctx, "%s", "MARK_CTXTRACEF")
	l.CtxDebugf(ctx, "%s", "MARK_CTXDEBUGF")
	l.CtxInfof(ctx, "%s", "MARK_CTXINFOF")
	l.CtxNoticef(ctx, "%s", "MARK_CTXNOTICEF")
	l.CtxWarnf(ctx, "%s", "MARK_CTXWARNF")
	l.CtxErrorf(ctx, "%s", "MARK_CTXERRORF")
	l.CtxFatalf(ctx, "%s", "MARK_CTXFATALF")

	out := buf.String()
	for _, want := range []string{
		"MARK_TRACE", "MARK_DEBUG", "MARK_INFO", "MARK_NOTICE",
		"MARK_WARN", "MARK_ERROR", "MARK_FATAL",
		"MARK_TRACEF", "MARK_DEBUGF", "MARK_INFOF", "MARK_NOTICEF",
		"MARK_WARNF", "MARK_ERRORF", "MARK_FATALF",
		"MARK_CTXTRACEF", "MARK_CTXDEBUGF", "MARK_CTXINFOF", "MARK_CTXNOTICEF",
		"MARK_CTXWARNF", "MARK_CTXERRORF", "MARK_CTXFATALF",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the adapter lost %q:\n%s", want, out)
		}
	}
}

func TestLevelMapping(t *testing.T) {
	buf, l := fixture(t, slog.LevelInfo)

	l.Debug("MARK_HIDDEN")
	l.Info("MARK_INFO")
	l.Notice("MARK_NOTICE")
	l.Warn("MARK_WARN")
	l.Error("MARK_ERROR")
	l.Fatal("MARK_FATAL")

	out := buf.String()
	if strings.Contains(out, "MARK_HIDDEN") {
		t.Fatalf("a debug line leaked at info level:\n%s", out)
	}
	for _, want := range []struct {
		marker string
		tag    string
	}{
		{"MARK_INFO", "[Info]"},
		{"MARK_NOTICE", "[Info]"},
		{"MARK_WARN", "[Warn]"},
		{"MARK_ERROR", "[Error]"},
		{"MARK_FATAL", "[Error]"},
	} {
		if !strings.Contains(out, want.tag+" "+want.marker) {
			t.Fatalf("%s is not %s:\n%s", want.marker, want.tag, out)
		}
	}
}

func TestCallerPCIsTheCallSite(t *testing.T) {
	buf, l := fixture(t, slog.LevelInfo)
	l.Info("MARK_PC")
	out := buf.String()
	if !strings.Contains(out, "adapter_test.go:") {
		t.Fatalf("the line does not point at the caller:\n%s", out)
	}
	if strings.Contains(out, "hertz.go:") {
		t.Fatalf("the line points at the adapter:\n%s", out)
	}
}

func TestCtxMethodsCarryTheRunPrefix(t *testing.T) {
	buf, l := fixture(t, slog.LevelInfo)
	l.CtxInfof(obs.WithRunID(context.Background(), "run-1"), "MARK_CTX")
	if !strings.Contains(buf.String(), "[run=run-1] MARK_CTX") {
		t.Fatalf("the run prefix is missing:\n%s", buf.String())
	}
}

func TestControlIsANoOp(t *testing.T) {
	buf, l := fixture(t, slog.LevelInfo)
	l.SetLevel(hlog.LevelDebug)
	l.SetOutput(io.Discard)
	l.Info("MARK_STILL_WRITES")
	if !strings.Contains(buf.String(), "MARK_STILL_WRITES") {
		t.Fatalf("SetOutput moved the writer:\n%s", buf.String())
	}
}

func TestNilLoggerDoesNotPanic(t *testing.T) {
	l := New(nil)
	l.Info("MARK_NIL")
	l.Fatalf("%s", "MARK_NIL_FATAL")
}

package obs

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		bad  bool
	}{
		{"", slog.LevelInfo, false},
		{"info", slog.LevelInfo, false},
		{"INFO", slog.LevelInfo, false},
		{"  Info  ", slog.LevelInfo, false},
		{"debug", slog.LevelDebug, false},
		{"WARN", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"trace", slog.LevelInfo, true},
		{"verbose", slog.LevelInfo, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
		if (err != nil) != c.bad {
			t.Errorf("ParseLevel(%q) error = %v, want bad=%v", c.in, err, c.bad)
		}
	}
}

func TestDiscardNeverReturnsNil(t *testing.T) {
	l := Discard()
	if l == nil {
		t.Fatal("Discard returned nil")
	}
	l.Info("this line must not reach a writer")
	l.Error("nor this one")
}

func TestLevelVarMovesExistingAndDerivedLoggers(t *testing.T) {
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelInfo)

	log := New(&buf, lv).With("run", "abc").WithGroup("g")

	log.Debug("hidden-at-info")
	log.Info("visible-at-info")
	out := buf.String()
	if strings.Contains(out, "hidden-at-info") {
		t.Fatalf("debug line leaked at info: %q", out)
	}
	if !strings.Contains(out, "visible-at-info") {
		t.Fatalf("info line missing: %q", out)
	}

	lv.Set(slog.LevelError)
	buf.Reset()
	log.Warn("hidden-at-error")
	log.Error("visible-at-error")
	out = buf.String()
	if strings.Contains(out, "hidden-at-error") {
		t.Fatalf("warn line leaked at error: %q", out)
	}
	if !strings.Contains(out, "visible-at-error") {
		t.Fatalf("error line missing: %q", out)
	}
}

func TestNewWithNilLevelVarDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, nil)
	log.Info("still works")
	if !strings.Contains(buf.String(), "still works") {
		t.Fatalf("nil LevelVar lost the record: %q", buf.String())
	}
}

func TestLevelVarIsConcurrencySafe(t *testing.T) {
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	log := New(&buf, lv)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				log.Info("record")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				lv.Set(slog.LevelDebug)
				lv.Set(slog.LevelError)
			}
		}()
	}
	wg.Wait()
}

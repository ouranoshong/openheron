package obs

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSinkRoutesToTheCurrentDestination(t *testing.T) {
	var first, second bytes.Buffer
	sink := NewSink(&first)

	log := slog.New(NewHandler(sink, slog.LevelDebug))
	log.Info("before")

	sink.Set(&second)
	log.Info("after")

	if !strings.Contains(first.String(), "before") {
		t.Fatalf("the first destination must keep what was written before the swap: %q", first.String())
	}
	if strings.Contains(first.String(), "after") {
		t.Fatalf("the first destination must not see later records: %q", first.String())
	}
	if !strings.Contains(second.String(), "after") {
		t.Fatalf("the second destination must see later records: %q", second.String())
	}
}

// ★ The reason the sink exists: the logger object is registered with hertz and
//
//	eino before the configuration is read, so the same logger has to follow the
//	destination. This is the test that says so.
func TestTheSameLoggerFollowsTheSink(t *testing.T) {
	var boot, file bytes.Buffer
	sink := NewSink(&boot)
	log := slog.New(NewHandler(sink, slog.LevelDebug))

	log.Info("bootstrap")
	sink.Set(&file)
	log.Info("configured")

	if strings.Contains(boot.String(), "configured") {
		t.Fatalf("the bootstrap destination kept receiving records: %q", boot.String())
	}
	if !strings.Contains(file.String(), "configured") {
		t.Fatalf("the file never received the record: %q", file.String())
	}
}

func TestSinkFallsBackOnceAndKeepsTheRecord(t *testing.T) {
	var fallback bytes.Buffer
	sink := NewSink(&fallback)

	failing := &failingWriter{err: errors.New("disk full")}
	sink.Set(failing)

	log := slog.New(NewHandler(sink, slog.LevelDebug))
	log.Info("first")
	log.Info("second")

	got := fallback.String()
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("the record that failed and the ones after it must reach the fallback: %q", got)
	}
	if n := strings.Count(got, "the log destination failed"); n != 1 {
		t.Fatalf("a broken destination must be reported once, got %d notices:\n%s", n, got)
	}
	if failing.calls != 1 {
		t.Fatalf("a failed destination must not be retried, got %d writes", failing.calls)
	}
}

// ★ Set runs on the main goroutine while a turn logs; -race is the assertion.
func TestSinkSwapsUnderConcurrentWrites(t *testing.T) {
	var a, b bytes.Buffer
	sink := NewSink(&a)
	log := slog.New(NewHandler(sink, slog.LevelDebug))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			log.Info("concurrent")
		}
	}()
	for i := 0; i < 50; i++ {
		if i%2 == 0 {
			sink.Set(&b)
		} else {
			sink.Set(&a)
		}
	}
	wg.Wait()

	if a.Len()+b.Len() == 0 {
		t.Fatal("records were lost across the swaps")
	}
}

type failingWriter struct {
	err   error
	calls int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.calls++
	return 0, f.err
}

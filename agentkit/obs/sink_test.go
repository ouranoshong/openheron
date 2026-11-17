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

// ★ The notice route: the console and the log file, whatever the log's route
//
//	is. This is the test that says the address still reaches the terminal
//	when the log has moved to a file.
func TestNoticeReachesTheConsoleAndTheFile(t *testing.T) {
	var console, file bytes.Buffer
	sink := NewSink(&console)
	log := slog.New(NewHandler(sink, slog.LevelDebug))

	sink.Install(&file, false)
	log.Info("a record")
	NewNotice(sink.NoticeWriter()).Info("a notice")

	if strings.Contains(console.String(), "a record") {
		t.Fatalf("the log left the console behind:\n%s", console.String())
	}
	if !strings.Contains(file.String(), "a record") {
		t.Fatalf("the record never reached the file:\n%s", file.String())
	}
	if !strings.Contains(console.String(), "a notice") {
		t.Fatalf("the notice never reached the console:\n%s", console.String())
	}
	if !strings.Contains(file.String(), "a notice") {
		t.Fatalf("the notice never reached the file:\n%s", file.String())
	}
}

// ★ One notice, one line on the terminal: the log's route covers the console
//
//	here, and a second copy would say the same thing twice.
func TestNoticeIsNotDuplicatedWhenTheLogCoversTheConsole(t *testing.T) {
	var console, file bytes.Buffer
	sink := NewSink(&console)
	sink.Install(&file, true)
	log := slog.New(NewHandler(sink, slog.LevelDebug))

	log.Info("a record")
	NewNotice(sink.NoticeWriter()).Info("a notice")

	if n := strings.Count(console.String(), "a notice"); n != 1 {
		t.Fatalf("the notice appeared %d times on the console:\n%s", n, console.String())
	}
	if !strings.Contains(file.String(), "a notice") {
		t.Fatalf("the notice never reached the file:\n%s", file.String())
	}
}

// ★ A notice is not a log record: the level does not filter it.
func TestNoticeIsNotFilteredByTheLevel(t *testing.T) {
	var console bytes.Buffer
	sink := NewSink(&console)
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelError)
	log := slog.New(NewHandlerWithLevel(sink, lv))
	log.Info("a record")

	NewNotice(sink.NoticeWriter()).Info("a notice")

	if strings.Contains(console.String(), "a record") {
		t.Fatalf("the record was not filtered by the level:\n%s", console.String())
	}
	if !strings.Contains(console.String(), "a notice") {
		t.Fatalf("the level filtered the notice:\n%s", console.String())
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

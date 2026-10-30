package obs

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The envelope is hertz's: the date, the time with microseconds, the
// caller's file and line, the level, then the message.
var lineShape = regexp.MustCompile(
	`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{6} \S+\.go:\d+: \[(Debug|Info|Warn|Error)\] `)

// render logs one record through the handler and returns the line.
func render(t *testing.T, level slog.Level, msg string, args ...any) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelDebug))
	logger.Log(context.Background(), level, msg, args...)
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("the line does not end with a newline: %q", out)
	}
	return strings.TrimSuffix(out, "\n")
}

func TestTheLineHasHertzsShape(t *testing.T) {
	line := render(t, slog.LevelInfo, "[main] openheron", "version", "dev")

	if !lineShape.MatchString(line) {
		t.Fatalf("the line does not have the expected shape: %q", line)
	}
	if !strings.Contains(line, "[Info] [main] openheron version=dev") {
		t.Fatalf("the message and the attributes are wrong: %q", line)
	}
}

// ★ The file and the line are the caller's, not the handler's. A log line
// pointing into obs itself would tell the reader nothing.
func TestTheSourceIsTheCallSite(t *testing.T) {
	line := render(t, slog.LevelInfo, "hello")

	if !strings.Contains(line, "handler_test.go:") {
		t.Fatalf("the line does not point at the call site: %q", line)
	}
	if strings.Contains(line, "handler.go:") {
		t.Fatalf("the line points into the handler: %q", line)
	}
}

func TestTheLevelTags(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "[Debug]"},
		{slog.LevelInfo, "[Info]"},
		{slog.LevelWarn, "[Warn]"},
		{slog.LevelError, "[Error]"},
	}
	for _, c := range cases {
		line := render(t, c.level, "message")
		if !strings.Contains(line, c.want+" ") {
			t.Fatalf("level %v: the tag is wrong: %q", c.level, line)
		}
	}
}

// The level decides whether a line shows up — that is the whole point of
// having one sink.
func TestTheLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelInfo))
	logger.Debug("quiet")
	logger.Info("loud")

	out := buf.String()
	if strings.Contains(out, "quiet") {
		t.Fatalf("a debug line must not reach an info-level output: %q", out)
	}
	if !strings.Contains(out, "loud") {
		t.Fatalf("the info line is missing: %q", out)
	}
}

// The message is written as it reads — no quotes around it.
func TestTheMessageIsNotQuoted(t *testing.T) {
	line := render(t, slog.LevelInfo, "[kb] DistanceThreshold is ignored")

	if !strings.Contains(line, "] [kb] DistanceThreshold is ignored") {
		t.Fatalf("the message was quoted or mangled: %q", line)
	}
}

// One record is one line: a message with a newline in it must not split
// the line into two that do not parse.
func TestANewlineInTheMessageIsEscaped(t *testing.T) {
	line := render(t, slog.LevelInfo, "first\nsecond")

	if strings.Count(line, "\n") != 0 {
		t.Fatalf("the line was split: %q", line)
	}
	if !strings.Contains(line, `first\nsecond`) {
		t.Fatalf("the newline was not escaped: %q", line)
	}
}

// The attributes are `key=value`, and a value that would be ambiguous on
// the line gets quoted.
func TestTheAttributesAreKeyValue(t *testing.T) {
	line := render(t, slog.LevelInfo, "message",
		"count", 3,
		"enabled", true,
		"reason", "connection refused",
		"plain", "value")

	for _, want := range []string{
		"count=3",
		"enabled=true",
		`reason="connection refused"`,
		"plain=value",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the attribute %q is missing or wrong: %q", want, line)
		}
	}
}

// The group prefix works the way slog says it does.
func TestAGroupPrefixesItsAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelDebug)).WithGroup("request")
	logger.Info("message", "id", "abc")

	out := buf.String()
	if !strings.Contains(out, "request.id=abc") {
		t.Fatalf("the group did not prefix the attribute: %q", out)
	}
}

// The attributes collected by With() lead the record's own.
func TestWithAttrsLeadsTheRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelDebug)).With("run", "abc")
	logger.Info("message", "id", "xyz")

	out := buf.String()
	if !strings.Contains(out, "run=abc id=xyz") {
		t.Fatalf("the attributes are in the wrong order: %q", out)
	}
}

// Several goroutines log at once: the lines must not tangle.
func TestConcurrentWritesAreWholeLines(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelDebug))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			logger.Info("message", "n", n)
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 32 {
		t.Fatalf("got %d lines, want 32", len(lines))
	}
	for _, line := range lines {
		if !lineShape.MatchString(line) {
			t.Fatalf("a line was tangled: %q", line)
		}
	}
}

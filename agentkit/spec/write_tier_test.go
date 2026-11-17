package spec

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// loggedService builds a service whose log goes into a buffer.
func loggedService(t *testing.T, f *fixture) (*Service, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	s, err := NewService(f.root, filepath.Dir(f.root),
		WithServiceLogger(slog.New(slog.NewTextHandler(buf, nil))))
	if err != nil {
		t.Fatal(err)
	}
	s.SetSecretStore(f.secrets)
	return s, buf
}

// ★ A write that widens the tier table is **said**, not refused.
//
// The file that provides a tool owns the exceptions for that tool — that
// is the tree's own rule — so the write is legitimate. But a table that
// widened on its own is exactly the fact an operator has to be able to
// find afterwards, and a silent widening is how a session ends up running
// tools nobody chose.
func TestAWriteThatWidensTheTierTableIsReported(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, buf := loggedService(t, f)

	if _, _, err := s.Write("tools/http.yaml",
		[]byte("methods: [get]\nallows: [request_get]\n"), ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "the tier table was widened") {
		t.Fatalf("the write must be reported, got %q", out)
	}
	if !strings.Contains(out, "request_get") {
		t.Fatalf("the report must name the tool, got %q", out)
	}
}

// ★ A write that does not widen the table says nothing. A line that fires
//
//	on every change is a line nobody reads.
func TestAWriteThatDoesNotWidenTheTierTableIsQuiet(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, buf := loggedService(t, f)

	if _, _, err := s.Write("tools/http.yaml",
		[]byte("methods: [get]\n"), ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if out := buf.String(); strings.Contains(out, "the tier table was widened") {
		t.Fatalf("a write that widened nothing must stay quiet, got %q", out)
	}
}

// ★ A table that **shrank** is not the thing an operator has to look for —
//
//	it is the safe direction.
func TestATableThatShrankIsNotReported(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\nallows: [request_get]\n")
	s, buf := loggedService(t, f)

	if _, _, err := s.Write("tools/http.yaml",
		[]byte("methods: [get]\n"), ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if out := buf.String(); strings.Contains(out, "the tier table was widened") {
		t.Fatalf("a table that shrank must stay quiet, got %q", out)
	}
}

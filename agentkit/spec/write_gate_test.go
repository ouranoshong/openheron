package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeErrKind(err error) WriteErrorKind {
	var we *WriteError
	if !errors.As(err, &we) {
		return ""
	}
	return we.Kind
}

func TestValidatePathRejectsEscapes(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"../secrets.yaml",
		"policy/../../etc/passwd",
		"policy/../llm/x.yaml",
		"/etc/passwd",
		"/tools/http.yaml",
		"state/anything.yaml",
		"nope/x.yaml",
		"skills/foo/SKILL.md",
	}
	for _, rel := range bad {
		if _, err := ValidatePath(rel); err == nil {
			t.Errorf("ValidatePath(%q) = nil error, want rejection", rel)
		} else if writeErrKind(err) != ErrPathInvalid {
			t.Errorf("ValidatePath(%q) kind = %v, want ErrPathInvalid", rel, err)
		}
	}
}

func TestValidatePathRejectsNonEditableSection(t *testing.T) {
	_, err := ValidatePath("server/config.yaml")
	if err == nil {
		t.Fatal("server/config.yaml must not be writable")
	}
	if writeErrKind(err) != ErrPathInvalid {
		t.Fatalf("kind = %v, want ErrPathInvalid", err)
	}
	if !strings.Contains(err.Error(), "restart") {
		t.Fatalf("the error must explain why, got %q", err)
	}
}

func TestValidatePathEnforcesTheToolsFileNameWhitelist(t *testing.T) {
	if _, err := ValidatePath("tools/http.yaml"); err != nil {
		t.Fatalf("http.yaml must be valid: %v", err)
	}
	if _, err := ValidatePath("tools/kb.yaml"); err != nil {
		t.Fatalf("kb.yaml must be valid: %v", err)
	}
	if _, err := ValidatePath("tools/whatever.yaml"); err == nil {
		t.Fatal("an unknown tools file must be rejected")
	}
	// ★ The `policy/` partition is gone: a path into it is refused like any
	//   other unknown section.
	if _, err := ValidatePath("policy/whatever.yaml"); err == nil {
		t.Fatal("a path into a retired partition must be rejected")
	}
}

func TestValidatePathAcceptsTheEditableSections(t *testing.T) {
	good := []string{
		"tools/http.yaml",
		"llm/deepseek.yaml",
		"agents/main.md",
		"agents/my-agent.md",
	}
	for _, rel := range good {
		if _, err := ValidatePath(rel); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", rel, err)
		}
	}
}

func TestValidatePathRejectsDotfiles(t *testing.T) {
	for _, rel := range []string{"policy/.hidden.yaml", "llm/.env"} {
		if _, err := ValidatePath(rel); err == nil {
			t.Errorf("ValidatePath(%q) = nil error, want rejection", rel)
		}
	}
}

// ── revision ──────────────────────────────────────────────────────────────

func TestRevisionIsStableAndContentAddressed(t *testing.T) {
	a := Revision([]byte("methods: [get]\nallows: [ls]\n"))
	b := Revision([]byte("methods: [get]\nallows: [ls]\n"))
	c := Revision([]byte("methods: [get]\nallows: [ls, glob]\n"))

	if a == "" || len(a) != revisionLen {
		t.Fatalf("revision %q has the wrong length", a)
	}
	if a != b {
		t.Fatalf("same content gave different revisions: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("different content must give different revisions")
	}
}

func TestWriteRejectsLiteralSecret(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	_, _, err := s.Write("llm/openai.yaml",
		[]byte("type: openai\nbase_url: https://x.example\napi_key: sk-realsecret\nmodels:\n  - id: fast\n"), "")

	if err == nil {
		t.Fatal("a literal api_key must be rejected")
	}
	if writeErrKind(err) != ErrSecretLiteral {
		t.Fatalf("kind = %v, want ErrSecretLiteral", err)
	}
	if !strings.Contains(f.read(t, "llm/openai.yaml"), "${TEST_LLM_KEY}") {
		t.Fatal("the original file was overwritten by a rejected write")
	}
}

func TestWriteAcceptsSecretReferences(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	secretFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(secretFile, []byte("sk-from-file"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ label, key string }{
		{"env ref", "${TEST_LLM_KEY}"},
		{"at-file ref", `"@file:` + secretFile + `"`},
		{"no auth", ""},
	}
	for _, c := range cases {
		content := "type: openai\nbase_url: https://x.example\n" +
			"api_key: " + c.key + "\nmodels:\n  - id: fast\n"
		if _, _, err := s.Write("llm/openai.yaml", []byte(content), ""); err != nil {
			t.Errorf("%s (api_key: %s) was rejected: %v", c.label, c.key, err)
		}
	}
}

func TestCheckSecretOnlyGuardsTheTopLevelAPIKey(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	content := `# a comment mentioning api_key: sk-not-a-real-leak
type: openai
base_url: https://x.example
extra:
  api_key: this-is-a-vendor-field
  other: plain-value
models:
  - id: fast
`
	if _, _, err := s.Write("llm/openai.yaml", []byte(content), ""); err != nil {
		t.Fatalf("indented/commented api_key must not be treated as the credential: %v", err)
	}
}

func TestWriteLandsAndReportsPostWriteRevision(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	content := []byte("methods: [get]\nallows: [read_file, write_file]\n")
	rev, cfg, err := s.Write("tools/http.yaml", content, "")
	if err != nil {
		t.Fatal(err)
	}

	if rev != Revision(content) {
		t.Fatalf("rev = %q, want %q", rev, Revision(content))
	}
	if got := f.read(t, "tools/http.yaml"); got != string(content) {
		t.Fatalf("on disk = %q, want %q", got, content)
	}
	if got := cfg.AllowedTools(); len(got) != 2 || got[0] != "read_file" {
		t.Fatalf("the returned config must reflect the write: %v", got)
	}
}

func TestWriteLeavesNoTempFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if _, _, err := s.Write("tools/http.yaml", []byte("methods: [get]\nallows: [execute]\n"), ""); err != nil {
		t.Fatal(err)
	}
	// ★ The temp file lands **next to the file being written**, so looking
	//   for it in another directory would pass no matter what the write did.
	if _, err := os.Stat(filepath.Join(f.root, "tools", "http.yaml.tmp")); err == nil {
		t.Fatal("a temp file was left behind")
	}
}

func TestWriteCreatesFirstConfigInAnEmptyProject(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.root); err == nil {
		t.Fatal("precondition: the config root must not exist yet")
	}
	if _, _, err := s.WriteMany([]File{
		{Rel: "llm/openai.yaml", Content: []byte(minimalLLM)},
		{Rel: "agents/main.md", Content: []byte("---\nmodel: openai/fast\n---\n你是助手。\n")},
		{Rel: "tools/http.yaml", Content: []byte("methods: [get]\nallows: [execute]\n")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, "tools/http.yaml"); !strings.Contains(got, "execute") {
		t.Fatalf("content = %q", got)
	}
	if !f.exists(t, "agents/main.md") {
		t.Fatal("the main agent must have been written too")
	}
}

func TestWriteRejectsStaleRevision(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\nallows: [execute]\n")
	s := f.service(t)

	stale := Revision([]byte("methods: [get]\nallows: [execute]\n"))
	f.write(t, "tools/http.yaml", "methods: [get]\nallows: [execute, write_file]\n")

	_, _, err := s.Write("tools/http.yaml", []byte("methods: [get]\nallows: [ls]\n"), stale)
	if err == nil {
		t.Fatal("a stale baseRev must be rejected")
	}
	if writeErrKind(err) != ErrConflict {
		t.Fatalf("kind = %v, want ErrConflict", err)
	}
	if got := f.read(t, "tools/http.yaml"); !strings.Contains(got, "write_file") {
		t.Fatalf("a rejected write must not overwrite: %q", got)
	}
}

func TestWriteAcceptsCurrentRevision(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	original := "methods: [get]\nallows: [execute]\n"
	f.write(t, "tools/http.yaml", original)
	s := f.service(t)

	if _, _, err := s.Write("tools/http.yaml",
		[]byte("methods: [get]\nallows: [execute, write_file]\n"), Revision([]byte(original))); err != nil {
		t.Fatalf("a matching baseRev must be accepted: %v", err)
	}
}

func TestWriteOnMissingFileTreatsEmptyRevisionAsCreate(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if _, _, err := s.Write("tools/http.yaml", []byte("methods: [get]\nallows: [execute]\n"), ""); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRejectsDanglingReference(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	_, err := s.Delete("llm/openai.yaml")
	if err == nil {
		t.Fatal("deleting the only referenced provider must be rejected")
	}
	if writeErrKind(err) != ErrMergeRejected {
		t.Fatalf("kind = %v, want ErrMergeRejected", err)
	}
	if f.exists(t, "llm/openai.yaml") != true {
		t.Fatal("a rejected delete must not remove the file")
	}
}

func TestDeleteLands(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "llm/unused.yaml", minimalLLM)
	s := f.service(t)

	cfg, err := s.Delete("llm/unused.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["unused"]; ok {
		t.Fatal("the returned config must reflect the delete")
	}
	if f.exists(t, "llm/unused.yaml") {
		t.Fatal("the file is still on disk")
	}
}

func TestDeleteMissingFileIsAnError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if _, err := s.Delete("llm/never-existed.yaml"); err == nil {
		t.Fatal("deleting a file that is not there must be an error")
	}
}

func TestReadFileReportsMissingAsEmptyNotError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	content, rev, err := s.ReadFile("llm/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if content == nil || rev == "" {
		t.Fatalf("an existing file must return content and a revision (rev=%q)", rev)
	}

	content, rev, err = s.ReadFile("tools/http.yaml")
	if err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if content != nil || rev != "" {
		t.Fatalf("a missing file must return nil/empty, got %q/%q", content, rev)
	}
}

// ★ Reading and writing ask different questions, and the read is allowed to be
// the looser one: `server/` cannot be written (it is not hot-reloaded), and
// that is a fact about writing. A reader that cannot look at the configuration
// the process is running from is a reader with a hole in it — and the hole was
// exactly one third of the tree, reported as "changes would need a restart"
// for a call that changes nothing.
func TestReadFileReadsAPartitionItMayNotWrite(t *testing.T) {
	f := newFixture(t)
	f.write(t, "server/config.yaml", "listen_addr: \":9999\"\n")
	s := f.service(t)

	content, rev, err := s.ReadFile("server/config.yaml")
	if err != nil {
		t.Fatalf("server/ is readable: %v", err)
	}
	if !strings.Contains(string(content), "9999") || rev == "" {
		t.Fatalf("content = %q, revision = %q", content, rev)
	}

	// ★ The write gate did not move: the same path is still refused.
	if _, _, err := s.Write("server/config.yaml", []byte("listen_addr: \":1\"\n"), rev); err == nil {
		t.Fatal("server/ must still refuse to be written")
	}
	if _, err := s.Delete("server/config.yaml"); err == nil {
		t.Fatal("server/ must still refuse to be deleted")
	}
}

func TestReadFileRejectsBadPaths(t *testing.T) {
	f := newFixture(t)
	s := f.service(t)

	// ★ The shape checks are shared with the writer: a path that leaves the
	//   root, or names a partition that does not exist, is not readable either.
	for _, rel := range []string{"../escape.yaml", "/etc/passwd", "policy/whatever.yaml", "tools/whatever.yaml"} {
		if _, _, err := s.ReadFile(rel); err == nil {
			t.Fatalf("ReadFile(%q) must be refused", rel)
		}
	}
}

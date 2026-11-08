package spec

import (
	"strings"
	"testing"
)

// findIssue returns the first issue of the given severity, or nil.
func findIssue(d *Diagnostics, severity string) *Issue {
	for i := range d.Issues {
		if d.Issues[i].Severity == severity {
			return &d.Issues[i]
		}
	}
	return nil
}

func mustDiagnose(t *testing.T, s *Service, rel, content string) *Diagnostics {
	t.Helper()
	d, err := s.Diagnostics(rel, []byte(content))
	if err != nil {
		t.Fatalf("Diagnostics(%s): %v", rel, err)
	}
	return d
}

// The whole point of replacing a single error string with issues: the
// reader is told which line, and the parser already knew.
func TestDiagnosticsPointAtTheLineOfAnUnknownKey(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md",
		"---\nmodel: openai/fast\nbogus_key: 1\n---\nbody\n")

	if d.OK {
		t.Fatal("an unknown key must not validate")
	}
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Line != 3 {
		t.Fatalf("line = %d, want 3 — the key is the third line of the file: %+v", iss.Line, d.Issues)
	}
	if iss.Path != "agents/main.md" {
		t.Fatalf("path = %q, want the file being edited", iss.Path)
	}
	if !strings.Contains(iss.Message, "bogus_key") {
		t.Fatalf("the message must name the key: %q", iss.Message)
	}
	// ★ The wrappers add "config: parse agents/main.md: main: frontmatter:".
	//   True, and noise in front of the sentence the user needs.
	if strings.Contains(iss.Message, "config: parse") || strings.HasPrefix(iss.Message, "yaml:") {
		t.Fatalf("the message still carries the loader's wrappers: %q", iss.Message)
	}
}

// ★ The frontmatter is decoded as a block of its own, so its line numbers
// are the block's. They coincide with the file's only while the `---` is on
// line 1 — which is why this case has to exist: the shift is invisible until
// a file opens with a blank line, and then it is silently wrong.
func TestDiagnosticsShiftLinesForAFrontmatterThatDoesNotStartTheFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// Two blank lines, then the delimiter: the file's line 5.
	content := "\n\n---\nmodel: openai/fast\nbogus_key: 1\n---\nbody\n"

	d := mustDiagnose(t, s, "agents/main.md", content)
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Line != 5 {
		t.Fatalf("line = %d, want 5 — two blank lines precede the `---`, and the key is the fifth line of the file:\n%s",
			iss.Line, content)
	}
}

func TestDiagnosticsReportEveryBadKeyNotJustTheFirst(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "tools/http.yaml", "methods: [get]\nbogus_one: 1\nbogus_two: 2\n")

	var lines []int
	for _, iss := range d.Issues {
		if iss.Severity == SeverityError {
			lines = append(lines, iss.Line)
		}
	}
	if len(lines) != 2 || lines[0] != 2 || lines[1] != 3 {
		t.Fatalf("lines = %v, want [2 3] — yaml reports them all, so all of them are shown", lines)
	}
}

func TestDiagnosticsReportASyntaxErrorLine(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "tools/http.yaml", "methods: [\n")
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Line != 1 {
		t.Fatalf("line = %d, want 1 (%+v)", iss.Line, d.Issues)
	}
	if iss.Message == "" || strings.HasPrefix(iss.Message, "yaml:") {
		t.Fatalf("the message must be the sentence, without the yaml: prefix: %q", iss.Message)
	}
}

// A cross-file rule has no line to point at, and saying "line 0" would be a
// lie about a position. Line 0 means "no line".
func TestDiagnosticsReportACrossFileRuleWithoutALine(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "agents/main.md", "---\nmodel: openai/nope\n---\nbody\n")
	if d.OK {
		t.Fatal("an undeclared model must not validate")
	}
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	if iss.Line != 0 {
		t.Fatalf("line = %d, want 0 — the rule is about the tree, not a position", iss.Line)
	}
	if !strings.Contains(iss.Message, "openai/nope") {
		t.Fatalf("the message must name the model: %q", iss.Message)
	}
}

// ★ Hints and errors are different severities of the same answer, and they
// arrive together: a file can say something its author did not mean while
// also breaking a rule. loadWithMany would have thrown the hints away here,
// because it drops the config it built when a rule fails — hence Diagnostics
// runs the parse and the rules itself.
//
// ★ A hint still needs a file that parsed: the notices are read off the
// config object, and a file that does not decode never becomes one. That is
// the boundary, and it is why this case breaks a cross-file rule rather than
// carrying an unknown key.
func TestDiagnosticsCarryHintsAlongsideErrors(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// A typo'd tool name is a hint; the undeclared model is an error.
	d := mustDiagnose(t, s, "agents/main.md",
		"---\nmodel: openai/nope\ntools: [ls, lsx]\n---\nbody\n")

	if d.OK {
		t.Fatal("an unknown key must not validate")
	}
	if findIssue(d, SeverityError) == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	hint := findIssue(d, SeverityHint)
	if hint == nil {
		t.Fatalf("the typo'd tool name must still be reported as a hint: %+v", d.Issues)
	}
	if !strings.Contains(hint.Message, "lsx") {
		t.Fatalf("the hint must name the tool: %q", hint.Message)
	}
	if hint.Line != 0 {
		t.Fatalf("hint line = %d; a hint names its file in the message, not a line", hint.Line)
	}
}

// A valid file produces no error issues — only hints, if any.
func TestDiagnosticsOnAValidFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "tools/http.yaml", "methods: [get]\n")
	if !d.OK {
		t.Fatalf("a valid file must validate: %+v", d.Issues)
	}
	if findIssue(d, SeverityError) != nil {
		t.Fatalf("no error issues expected: %+v", d.Issues)
	}
	if d.ConfigVersion == "" {
		t.Fatal("the answer must carry the fingerprint the client echoes back")
	}
}

// A literal key is not a parse problem and not a cross-file rule: it is
// refused for a reason that belongs to the repository. It still has a line,
// and the editor shows it while the user is typing.
func TestDiagnosticsPointAtALiteralAPIKey(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	d := mustDiagnose(t, s, "llm/openai.yaml",
		"type: openai\nbase_url: https://x\napi_key: sk-literal\nmodels: []\n")

	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("a literal key must be an error: %+v", d.Issues)
	}
	if iss.Line != 3 {
		t.Fatalf("line = %d, want 3 (%+v)", iss.Line, d.Issues)
	}
	if d.OK {
		t.Fatal("ok must be false")
	}
}

// A path outside the tree cannot be diagnosed at all, so it stays an error
// rather than an issue: there is no file to attach an issue to.
func TestDiagnosticsRejectAnInvalidPath(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if _, err := s.Diagnostics("../etc/passwd", []byte("x: 1\n")); err == nil {
		t.Fatal("a path outside the configuration root must be refused")
	}
}

// Check is the whole-tree form: the file is on disk, and the question is
// whether the tree loads.
func TestCheckReportsTheTreeOnDisk(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	if d := s.Check(); !d.OK {
		t.Fatalf("the seeded tree must be clean: %+v", d.Issues)
	}

	f.write(t, "tools/http.yaml", "bogus_key: 1\n")
	d := s.Check()
	if d.OK {
		t.Fatal("an unknown key on disk must be reported")
	}
	iss := findIssue(d, SeverityError)
	if iss == nil {
		t.Fatalf("no error issue: %+v", d.Issues)
	}
	// ★ The path, not the bare name: two partitions can hold a file called
	//   http.yaml, and the reader needs to know which one.
	if iss.Line != 1 {
		t.Fatalf("line = %d, want 1", iss.Line)
	}
	if !strings.Contains(iss.Message, "bogus_key") {
		t.Fatalf("message = %q", iss.Message)
	}
}

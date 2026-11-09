package spec

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The two severities a Diagnostic may carry.
//
// ★ The split is not about how bad the problem is but about **who decides**:
//
//	an error means the configuration will not load, and there is nothing to
//	discuss; a hint means it loads, and the author is told something they
//	probably did not intend. Merging the two would force a caller to parse
//	prose to find out whether it may save the file.
const (
	SeverityError = "error"
	SeverityHint  = "hint"
)

// Issue is one thing wrong with a configuration file, and where it is.
type Issue struct {
	// Path is relative to the configuration root. Empty when the problem is
	// not attributable to one file — a cross-file rule names the files in its
	// message instead of pretending to point at one.
	Path string `json:"path,omitempty"`

	// Line is 1-based, and 0 when the issue is about the file as a whole or
	// about a combination of files. ★ 0 is not "line 0": it means "no line",
	// and a client must not render a marker for it.
	Line int `json:"line"`

	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Diagnostics is the answer to "would this tree, or this file, load?".
type Diagnostics struct {
	OK            bool    `json:"ok"`
	Issues        []Issue `json:"issues"`
	ConfigVersion string  `json:"configVersion"`
}

// Diagnostics reports what is wrong with a candidate file.
//
// The path and the content are checked apart from the tree first, because
// those two failures are not about loading at all: a path outside the
// configuration root, or an API key written out in plain text, is refused
// before anything is read. They are returned as an error, which the HTTP
// layer turns into a 400 — they are not "issues" a user could fix by
// editing what they typed into this form.
//
// What remains is the trial merge, and it fails in three shapes that a
// reader needs told apart:
//
//  1. the YAML did not decode — yaml.v3 says which line, and it survives
//     being carried up here;
//  2. the tree no longer holds together — a cross-file rule, no line;
//  3. it loads, but something in it is probably not what was meant —
//     `Deferred()` already collects those, and they become hints.
//
// ★ The trial is run at the `loadOnly` + `validate` level rather than
// through loadWithMany, because loadWithMany discards the config it built
// when validate fails — and the hints live on that config. A diagnostics
// endpoint that goes quiet about hints the moment one rule breaks would
// report strictly less than the loader knows.
func (s *Service) Diagnostics(rel string, content []byte) (*Diagnostics, error) {
	cat, err := ValidatePath(rel)
	if err != nil {
		return nil, err
	}
	trial, d := s.diagnose(rel, map[string]*mutation{rel: {Rel: rel, Content: content}})
	if cat.Dir == DirLLM {
		if line := secretLiteralLine(content); line > 0 {
			// ★ Ahead of the rest, not instead of it: the key makes the file
			//   unsavable, but hiding the other issues behind it would send
			//   the user round the loop twice.
			d.OK = false
			d.Issues = append([]Issue{{
				Path: rel, Line: line, Severity: SeverityError,
				Message: secretLiteralMessage,
			}}, d.Issues...)
		}
	}
	// ★ The gate the write path has, in the editor's own terms: a change
	//   that would take a **working** agent out of the tree is refused, and
	//   the user has to hear it while they are typing — not when they press
	//   Save.
	//
	//   The issue is on the file being edited: it is **this** change that
	//   is refused. The agent that would be lost is named in the message.
	lost := s.agentsLostByChange(trial, nil)
	if len(lost) > 0 {
		d.OK = false
		issues := make([]Issue, 0, len(lost))
		for _, id := range lost {
			issues = append(issues, Issue{
				Path:     rel,
				Severity: SeverityError,
				Message:  lostAgentMessage(id, trial),
			})
		}
		// ★ **After** the file's own problems, not before them. The file's
		//   own problem is what the user can fix right here; the lost agent
		//   is the consequence. A reader who sees the consequence first
		//   cannot act on it, and a reader who sees only the consequence
		//   cannot act on it either.
		d.Issues = append(d.Issues, issues...)
	}
	return d, nil
}

// Check reports what is wrong with the tree as it is on disk.
//
// ★ Whole-tree checking is fail-fast: the loader stops at the first file it
// cannot read, so this reports that one error plus whatever hints were
// collected before it. Reporting *every* broken file would mean turning the
// loader from "return an error" into "accumulate issues", which is a larger
// change than the one this answers — and editing a configuration is one file
// at a time anyway.
func (s *Service) Check() *Diagnostics {
	_, d := s.diagnose("", nil)
	return d
}

func (s *Service) diagnose(rel string, ms map[string]*mutation) (*Config, *Diagnostics) {
	d := &Diagnostics{OK: true, Issues: []Issue{}}

	cfg, err := s.loadOnly(ms)
	if err != nil {
		d.OK = false
		d.Issues = append(d.Issues, parseIssues(rel, err)...)
		d.ConfigVersion = s.Fingerprint()
		return nil, d
	}
	if err := cfg.validate(); err != nil {
		d.OK = false
		// ★ The file the error is about, not the file being edited. The
		//   editor used to be told that the file in front of it was broken
		//   because **another** file was: an agent whose model is not
		//   declared is a problem of that agent's file, and saying so is
		//   the difference between a message the user can act on and one
		//   they cannot.
		d.Issues = append(d.Issues, Issue{
			Path:     fileErrorPath(err),
			Severity: SeverityError,
			Message:  err.Error(),
		})
		// ★ The tree did not settle, so there is nothing to compare the
		//   disk against: the error above says what is wrong, and the gate
		//   would only say it a second way.
		d.ConfigVersion = s.Fingerprint()
		return nil, d
	}
	for _, n := range cfg.Notices() {
		// ★ A notice belongs to the file it names. Showing it under the
		//   file being edited is the same mistake as the error above.
		d.Issues = append(d.Issues, Issue{
			Path:     n.Path,
			Severity: SeverityHint,
			Message:  n.Message,
		})
	}
	d.ConfigVersion = s.Fingerprint()
	return cfg, d
}

// lineRe matches what yaml.v3 puts in front of the line it objects to, in
// both shapes it uses: "  line 2: field x not found" inside a TypeError's
// list, and "yaml: line 3: could not find expected ':'" as a bare error.
var lineRe = regexp.MustCompile(`(?:^|\s)line (\d+): (.*)$`)

// parseShift records how many lines were cut off before the block the YAML
// decoder was given.
//
// ★ An agent file is decoded from its frontmatter, not from the whole file,
//
//	so "line 2" means the second line **of the frontmatter** — the opening
//	`---` is line 1 of the file and an empty line 1 of the block, so the two
//	happen to agree until someone leaves a blank line above the `---`. Then
//	they silently disagree, and a line number that is quietly off by two is
//	worse than none: it sends the reader to the wrong place with full
//	confidence.
type parseShift struct {
	lines int
	err   error
}

func (e *parseShift) Error() string { return e.err.Error() }

func (e *parseShift) Unwrap() error { return e.err }

// parseIssues turns a load failure into issues a reader can act on.
func parseIssues(rel string, err error) []Issue {
	shift := 0
	var ps *parseShift
	if errors.As(err, &ps) {
		shift = ps.lines
	}

	// ★ The file the error names, not the file that was edited. A draft can
	//   be perfectly good while another file of the tree is broken, and the
	//   reader has to be sent to the broken one.
	if p := fileErrorPath(err); p != "" {
		rel = p
	}

	base := err
	for {
		next := errors.Unwrap(base)
		if next == nil {
			break
		}
		base = next
	}

	// ★ The innermost error, not the outermost message: the wrappers add
	//   "config: parse agents/main.md: main: frontmatter:" — true, and noise
	//   in front of the one sentence the user needs.
	var te *yaml.TypeError
	if errors.As(base, &te) {
		out := make([]Issue, 0, len(te.Errors))
		for _, line := range te.Errors {
			out = append(out, yamlIssue(rel, line, shift))
		}
		return out
	}
	return []Issue{yamlIssue(rel, base.Error(), shift)}
}

func yamlIssue(rel, text string, shift int) Issue {
	msg := strings.TrimSpace(text)
	line := 0
	if m := lineRe.FindStringSubmatch(msg); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			line = n
		}
		msg = m[2]
	}
	msg = strings.TrimPrefix(msg, "yaml: ")
	msg = strings.TrimSpace(msg)
	// ★ An empty message would render as a marker with nothing beside it;
	//   say what happened instead of leaving the row blank.
	if msg == "" {
		msg = "the file could not be read as YAML"
	}
	return Issue{Path: rel, Line: line + shift, Severity: SeverityError, Message: msg}
}

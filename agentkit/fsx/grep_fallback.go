package fsx

// ★ The built-in search engine: what the grep tool falls back to when
//   ripgrep is not installed.
//
// ★ Why there is a fallback at all: the grep tool is the one tool that has
//   to work on a machine with nothing installed, and ripgrep is not one of
//   the things Windows ships.
//
// ★ Why not the system's grep: this tool's output is **data** — a path, a
//   line number, a line of text — and grep's output is **text for a human**.
//   `path:line:content` cannot be parsed back reliably: a Windows drive
//   letter puts a colon inside the path, a content line may carry `12:` of
//   its own, the file name only appears with -r/-H, and the context lines
//   use a different separator. The shape changes with the dialect too
//   (GNU, BSD, busybox). ★ ripgrep's `--json` gives structured events,
//   which is why the tool reads that instead of the text.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk/filesystem"
)

// The notices ride on the match list, because that is the only channel the
// backend has: the tool renders whatever comes back, and a search that
// quietly used a different engine is exactly the kind of result that reads
// as complete when it is not.
const (
	// grepEngineNotice says which engine ran and what it cannot do.
	grepEngineNotice = "… ripgrep (rg) was not found; this search used the built-in engine " +
		"(Go regular expressions, no .gitignore rules, no file-type table)"

	// grepLiteralNotice says the pattern was not a regular expression.
	grepLiteralNotice = "… the pattern is not a valid regular expression; " +
		"it was searched for literally"
)

const (
	// grepBinarySniff is how many bytes decide whether a file is binary.
	//
	// ★ A NUL byte is the signal, and it is the one ripgrep uses: a text
	//   file does not carry one, and a binary file usually does.
	grepBinarySniff = 8 * 1024

	// grepMaxWholeBytes bounds the file the multiline mode reads whole.
	//
	// ★ A multiline pattern matches **across** lines, so the file has to
	//   be in memory — and a huge generated file would be a memory
	//   incident in a search that is already a fallback.
	grepMaxWholeBytes = 8 * 1024 * 1024
)

// grepFileTypes maps a file type name to the extensions that carry it.
//
// ★ A subset of ripgrep's table, and deliberately so: a type name this
//
//	table does not know is an **error**, not a silent mismatch. A search
//	that quietly looked at the wrong files is worse than one that says it
//	cannot do it.
var grepFileTypes = map[string][]string{
	"bat":   {"*.bat", "*.cmd"},
	"c":     {"*.c", "*.h"},
	"cpp":   {"*.cpp", "*.cc", "*.cxx", "*.hpp", "*.hh", "*.hxx"},
	"css":   {"*.css", "*.scss", "*.sass", "*.less"},
	"dart":  {"*.dart"},
	"erl":   {"*.erl"},
	"ex":    {"*.ex", "*.exs"},
	"go":    {"*.go"},
	"hs":    {"*.hs"},
	"html":  {"*.html", "*.htm"},
	"java":  {"*.java"},
	"js":    {"*.js", "*.jsx", "*.mjs", "*.cjs"},
	"json":  {"*.json"},
	"kt":    {"*.kt", "*.kts"},
	"lua":   {"*.lua"},
	"make":  {"Makefile", "*.mk"},
	"md":    {"*.md", "*.markdown"},
	"php":   {"*.php"},
	"ps1":   {"*.ps1"},
	"py":    {"*.py", "*.pyi"},
	"rb":    {"*.rb"},
	"rs":    {"*.rs"},
	"scala": {"*.scala"},
	"sh":    {"*.sh", "*.bash", "*.zsh"},
	"sql":   {"*.sql"},
	"swift": {"*.swift"},
	"toml":  {"*.toml"},
	"ts":    {"*.ts", "*.tsx", "*.mts", "*.cts"},
	"vim":   {"*.vim"},
	"xml":   {"*.xml"},
	"yaml":  {"*.yaml", "*.yml"},
}

// errGrepStop ends the walk: the match cap was reached.
var errGrepStop = errors.New("grep: the match cap was reached")

// grepEngine is one prepared search: the pattern, the filters and the
// flags the request carried.
type grepEngine struct {
	// re is the compiled pattern. It is nil when the pattern was not a
	// valid regular expression — then the search is literal.
	re *regexp.Regexp
	// literal is the pattern as text, used when re is nil.
	literal string
	// caseInsensitive mirrors the request's -i.
	caseInsensitive bool
	// multiline mirrors the request's -U: the pattern may span lines.
	multiline bool
	// glob filters the files by path; it is empty when there is no filter.
	glob string
	// exts is the file-type filter as extensions; it is empty when there
	// is no filter.
	exts []string
}

// newGrepEngine prepares the search.
//
// ★ A pattern that is not a regular expression does not fail the call: it
//
//	is searched for literally, and the result says so. The model asked
//	for a string, and a string is what it gets.
func newGrepEngine(req *filesystem.GrepRequest) (*grepEngine, error) {
	e := &grepEngine{
		caseInsensitive: req.CaseInsensitive,
		multiline:       req.EnableMultiline,
	}

	// ★ The type filter wins over the glob, the same way the ripgrep
	//   command line does: it is the narrower of the two.
	if req.FileType != "" {
		exts, ok := grepFileTypes[strings.ToLower(req.FileType)]
		if !ok {
			return nil, fmt.Errorf("the built-in search engine does not know the file type %q; "+
				"use a glob instead", req.FileType)
		}
		e.exts = exts
	} else if req.Glob != "" {
		e.glob = req.Glob
	}

	pattern := req.Pattern
	flags := ""
	if e.caseInsensitive {
		flags += "i"
	}
	if e.multiline {
		flags += "s"
	}
	if flags != "" {
		// ★ The flags go **outside** the pattern, and the pattern is
		//   wrapped: `(?s)a|b` would leave the second branch without the
		//   flag, and a group changes nothing that a backreference could
		//   count on — there are none in this dialect.
		pattern = "(?" + flags + ")(?:" + pattern + ")"
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		e.literal = req.Pattern
		return e, nil
	}
	e.re = re
	return e, nil
}

// matches reports whether one line matches.
func (e *grepEngine) matches(text string) bool {
	if e.re == nil {
		if e.caseInsensitive {
			return strings.Contains(strings.ToLower(text), strings.ToLower(e.literal))
		}
		return strings.Contains(text, e.literal)
	}
	return e.re.MatchString(text)
}

// accepts reports whether a file is searched, given its path relative to
// the project root.
func (e *grepEngine) accepts(rel string) bool {
	if len(e.exts) > 0 {
		base := path.Base(rel)
		for _, ext := range e.exts {
			if ok, _ := doublestar.Match(ext, base); ok {
				return true
			}
		}
		return false
	}
	if e.glob != "" {
		// ★ A glob without a separator matches the **file name**, the way
		//   ripgrep's --glob does: "*.go" is every Go file at any depth.
		target := rel
		if !strings.Contains(e.glob, "/") {
			target = path.Base(rel)
		}
		ok, _ := doublestar.Match(e.glob, target)
		return ok
	}
	return true
}

// grepSearch is one search in progress: the state the walk and the
// per-file search share.
type grepSearch struct {
	local  *Local
	engine *grepEngine
	req    *filesystem.GrepRequest
	// matches are the results so far.
	matches []filesystem.GrepMatch
	// capped reports that the match cap was reached.
	capped bool
	// skippedWhole counts the files the multiline mode could not read
	// whole.
	skippedWhole int
}

// grepBuiltin searches with the built-in engine.
//
// ★ The caps and the budget are the same ones the ripgrep path uses, and
//
//	the messages are the same words: a fallback that failed differently
//	would be a second set of rules for the model to learn.
func (s *Local) grepBuiltin(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	if req.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	base, err := s.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	engine, err := newGrepEngine(req)
	if err != nil {
		return nil, err
	}

	g := &grepSearch{local: s, engine: engine, req: req}
	// ★ The deadline is the walk's budget. It starts when the walk
	//   starts, not when the tool was called: the time spent resolving
	//   the path is the tool's, the time spent walking is the walk's.
	deadline := time.Now().Add(s.walkBudget)
	timedOut := false

	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			// ★ The walk ran out of time. Stopping here is better than
			//   blocking the turn: the model gets what was found and a
			//   note to narrow the path.
			timedOut = true
			return filepath.SkipAll
		}
		if walkErr != nil {
			// An unreadable subtree skips itself; it does not abort the
			// whole search.
			return nil
		}

		if d.IsDir() {
			if s.ignored(d) || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(base, p)
			if relErr == nil && strings.Count(filepath.ToSlash(rel), "/") >= maxGlobDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			// ★ A symlink is not followed: ripgrep does not follow them
			//   either, and a link out of the tree would read files this
			//   search is not supposed to reach.
			return nil
		}

		// ★ Hidden files are skipped, the way ripgrep skips them. The
		//   check is against the path **relative to the search root**:
		//   a directory the model named explicitly is still searched.
		relBase, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		relBase = filepath.ToSlash(relBase)
		if relBase == "." {
			return nil
		}
		if hiddenInPath(relBase) {
			return nil
		}

		rel, relErr := filepath.Rel(s.root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !engine.accepts(rel) {
			return nil
		}
		return g.file(p, rel)
	})

	if errors.Is(walkErr, errGrepStop) {
		walkErr = nil
		g.capped = true
	}
	if walkErr != nil {
		return nil, walkErr
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("the search hit its %s budget; narrow the path or the pattern", s.walkBudget)
	}
	if timedOut {
		// ★ The search ran out of its budget. Say what happened: a raw
		//   "exit status 1" is something the model cannot act on.
		return nil, fmt.Errorf("the search hit its %s budget; narrow the path or the pattern", s.walkBudget)
	}

	if g.capped {
		// The cap is the same one the ripgrep path uses, and the note is
		// the same words.
		grepNote(g.matches, grepCapNote())
	}
	// ★ The notices ride on the last match, because the backend has no
	//   other channel — and they are added **only when the search found
	//   something**. A search with no results must stay "no results": the
	//   renderer counts the paths it is given, so a note on its own would
	//   turn "nothing found" into "found one file".
	if len(g.matches) > 0 {
		grepNote(g.matches, g.notice())
	} else if g.engine.re == nil {
		// ★ The pattern was not a regular expression, and the literal
		//   search found nothing. Saying "no matches" would be answering a
		//   question nobody asked: the pattern was never interpreted the
		//   way it was written. Say what happened instead.
		return nil, fmt.Errorf("the pattern %q is not a valid regular expression and nothing matched it "+
			"literally; escape it, or install ripgrep", g.req.Pattern)
	}
	return g.matches, nil
}

// notice is the line that tells the model what it is looking at.
func (g *grepSearch) notice() string {
	parts := make([]string, 0, 2)
	if g.engine.re == nil {
		parts = append(parts, grepLiteralNotice)
	}
	parts = append(parts, grepEngineNotice)
	return strings.Join(parts, "\n")
}

// file searches one file.
func (g *grepSearch) file(p, rel string) error {
	binary, err := grepIsBinary(p)
	if err != nil {
		// An unreadable file skips itself; it does not abort the search.
		return nil
	}
	if binary {
		return nil
	}
	if g.engine.multiline {
		return g.whole(p, rel)
	}
	return g.lines(p, rel)
}

// grepLine is one line of a file, with its number.
type grepLine struct {
	no   int
	text string
}

// lines searches a file line by line.
//
// ★ The file is streamed, not read whole: a generated file can be larger
//
//	than the memory this process should spend on a fallback search.
func (g *grepSearch) lines(p, rel string) error {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// ★ Each line carries a whole source line, so the default 64 KiB
	//   limit would stop the scan on a generated or minified file.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)

	before := g.req.BeforeLines
	after := g.req.AfterLines

	var ring []grepLine
	pending := 0
	lastEmitted := 0
	lineNo := 0

	emit := func(no int, text string) error {
		if no <= lastEmitted {
			// ★ A line that is already out is not out twice: two matches
			//   next to each other share their context.
			return nil
		}
		lastEmitted = no
		g.matches = append(g.matches, filesystem.GrepMatch{Path: rel, Line: no, Content: text})
		if len(g.matches) >= maxGrepMatches {
			return errGrepStop
		}
		return nil
	}

	for scanner.Scan() {
		lineNo++
		text := scanner.Text()
		switch {
		case g.engine.matches(text):
			for _, l := range ring {
				if err := emit(l.no, l.text); err != nil {
					return err
				}
			}
			if err := emit(lineNo, text); err != nil {
				return err
			}
			pending = after
		case pending > 0:
			if err := emit(lineNo, text); err != nil {
				return err
			}
			pending--
		}
		if before > 0 {
			ring = append(ring, grepLine{no: lineNo, text: text})
			if len(ring) > before {
				ring = ring[1:]
			}
		}
	}
	return nil
}

// whole searches a file with a pattern that may span lines.
//
// ★ The file is read whole, because a multiline pattern matches across
//
//	the lines — and the read is bounded, because a huge generated file
//	would be a memory incident.
func (g *grepSearch) whole(p, rel string) error {
	info, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if info.Size() > grepMaxWholeBytes {
		g.skippedWhole++
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	content := string(data)

	locs := g.engine.re.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")

	before := g.req.BeforeLines
	after := g.req.AfterLines
	lastEmitted := 0

	emit := func(no int, text string) error {
		if no <= lastEmitted {
			return nil
		}
		lastEmitted = no
		g.matches = append(g.matches, filesystem.GrepMatch{Path: rel, Line: no, Content: text})
		if len(g.matches) >= maxGrepMatches {
			return errGrepStop
		}
		return nil
	}

	cursor, cursorLine := 0, 1
	for _, loc := range locs {
		start := g.lineAt(content, &cursor, &cursorLine, loc[0])
		end := g.lineAt(content, &cursor, &cursorLine, loc[1])

		for no := start - before; no < start; no++ {
			if no < 1 || no > len(lines) {
				continue
			}
			if err := emit(no, lines[no-1]); err != nil {
				return err
			}
		}
		if err := emit(start, content[loc[0]:loc[1]]); err != nil {
			return err
		}
		for no := end + 1; no <= end+after; no++ {
			if no < 1 || no > len(lines) {
				continue
			}
			if err := emit(no, lines[no-1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// lineAt answers the line number of an offset, walking forward from the
// last answer.
//
// ★ The matches come in order, so the line number can be carried over
//
//	instead of counting the newlines from the start every time.
func (g *grepSearch) lineAt(content string, cursor, line *int, offset int) int {
	for *cursor < offset {
		idx := strings.IndexByte(content[*cursor:], '\n')
		if idx < 0 {
			break
		}
		*cursor += idx + 1
		*line++
	}
	return *line
}

// hiddenInPath reports whether any component of a relative path is hidden.
func hiddenInPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// grepIsBinary reports whether a file looks binary.
//
// ★ A NUL byte in the first few kilobytes is the signal, and it is the
//
//	one ripgrep uses.
func grepIsBinary(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, grepBinarySniff)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		if errors.Is(err, fs.ErrClosed) || errors.Is(err, os.ErrClosed) {
			return false, err
		}
		// A file that cannot be read is not a binary file; it is a file
		// this search cannot see.
		return false, err
	}
	return strings.IndexByte(string(buf[:n]), 0) >= 0, nil
}

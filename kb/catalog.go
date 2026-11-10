package kb

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	CatalogFile = "catalog.md"
	SidecarFile = "catalog.gob"

	catalogHeader = "# Knowledge Base Catalog"
	// catalogHeaderLegacy is the title of catalogs written before the header
	// was translated. Those files still live on disk, so they must keep parsing.
	catalogHeaderLegacy = "# 知识库目录"
	entryPrefix         = "## "
	quotePrefix         = "> "
)

const (
	keyTitle     = "title"
	keyMTime     = "mtime"
	keySize      = "size"
	keySections  = "sections"
	keyTruncated = "sections_truncated"
	keyLinks     = "links"
	keyStale     = "stale"
)

const maxSectionsStored = 200

const sectionSep = " (L"

type Header struct {
	Corpus       string
	Docs         int
	Revision     int64
	Writer       string
	IndexVersion int
	Model        string
	Dim          int
	Created      time.Time
	Updated      time.Time
}

type Entry struct {
	Path string

	Title   string
	Summary string

	Sections          []Section
	SectionsTruncated int

	Links []string
	MTime int64
	Size  int64

	Stale bool
}

type Section struct {
	Title string
	Line  int
}

func (s Section) String() string {
	if s.Line <= 0 {
		return s.Title
	}
	return s.Title + sectionSep + strconv.Itoa(s.Line) + ")"
}

type Catalog struct {
	Header   Header
	Entries  []Entry
	Warnings []string
}

func (c *Catalog) Get(path string) (Entry, bool) {
	for _, e := range c.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

func (c *Catalog) Sort() {
	sort.Slice(c.Entries, func(i, j int) bool { return c.Entries[i].Path < c.Entries[j].Path })
}

func RenderCatalog(c Catalog) []byte {
	entries := normalizeEntries(c.Entries)
	var b strings.Builder

	b.WriteString(catalogHeader)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "> corpus_dir: %s  ·  %s  ·  rev %d  ·  writer %s  ·  created at %s\n",
		orNone(c.Header.Corpus), docCount(len(entries)), c.Header.Revision,
		orNone(c.Header.Writer), stamp(c.Header.Created))
	b.WriteString("> Maintained by `openheron kb index`. **The summary line can be edited directly** " +
		"— it takes effect immediately and the vector is recomputed automatically.\n")
	b.WriteString("> The remaining `key: value` lines are derived and will be overwritten.\n")
	fmt.Fprintf(&b, "> extraction rule v%d", c.Header.IndexVersion)
	if c.Header.Model != "" {
		fmt.Fprintf(&b, "  ·  model %s (dim %d)", c.Header.Model, c.Header.Dim)
	}
	fmt.Fprintf(&b, "  ·  updated at %s\n", stamp(c.Header.Updated))

	for _, e := range entries {
		b.WriteString("\n")
		b.WriteString(entryPrefix)
		b.WriteString(e.Path)
		b.WriteString("\n\n")

		if s := oneLine(e.Summary); s != "" {
			b.WriteString(quotePrefix)
			b.WriteString(s)
			b.WriteString("\n\n")
		}

		b.WriteString(keyTitle + ": " + oneLine(e.Title) + "\n")
		fmt.Fprintf(&b, "%s: %d\n", keyMTime, e.MTime)
		fmt.Fprintf(&b, "%s: %d\n", keySize, e.Size)
		b.WriteString(keySections + ": " + renderSections(e.Sections, e.SectionsTruncated) + "\n")
		if e.SectionsTruncated > 0 {
			fmt.Fprintf(&b, "%s: %d\n", keyTruncated, e.SectionsTruncated)
		}
		b.WriteString(keyLinks + ": " + strings.Join(e.Links, ", ") + "\n")
		fmt.Fprintf(&b, "%s: %t\n", keyStale, e.Stale)
	}
	return []byte(b.String())
}

func normalizeEntries(in []Entry) []Entry {
	out := make([]Entry, len(in))
	for i, e := range in {
		out[i] = e
		out[i].Links = sortedUnique(e.Links)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func sortedUnique(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return dedupAdjacent(out)
}

func ParseCatalog(b []byte) (Catalog, error) {
	var c Catalog
	c.Warnings = nil

	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	seenHeader := false
	var cur *Entry
	curPath := ""

	flush := func() {
		if cur != nil {
			c.Entries = append(c.Entries, *cur)
		}
		cur, curPath = nil, ""
	}

	for i, line := range lines {
		ln := i + 1

		if !seenHeader {
			if isCatalogHeader(line) {
				seenHeader = true
			}
			continue
		}

		if strings.HasPrefix(line, entryPrefix) {
			flush()
			p := strings.TrimSpace(strings.TrimPrefix(line, entryPrefix))
			if p == "" {
				c.Warnings = append(c.Warnings,
					fmt.Sprintf("line %d: `## ` with no path, section ignored", ln))
				continue
			}
			if strings.ContainsAny(p, "\r\n") {
				c.Warnings = append(c.Warnings,
					fmt.Sprintf("line %d: path %q contains a line break, section ignored", ln, p))
				continue
			}
			cur = &Entry{Path: p}
			curPath = p
			continue
		}

		if cur == nil {
			if strings.HasPrefix(strings.TrimSpace(line), ">") {
				parseHeaderLine(&c.Header, line)
			}
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			if cur.Summary != "" {
				c.Warnings = append(c.Warnings, fmt.Sprintf(
					"%s: extra summary line at %d kept the first one: %s", curPath, ln, oneLine(trimmed)))
				continue
			}
			s := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			cur.Summary = s
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		k = strings.TrimSpace(k)
		if !ok || !looksLikeKey(k) {
			continue
		}
		applyEntryKey(cur, k, v, ln, curPath, &c.Warnings)
	}
	flush()

	if !seenHeader {
		return Catalog{}, fmt.Errorf("kb: not a catalog file: %q or %q not found",
			catalogHeader, catalogHeaderLegacy)
	}
	c.Sort()
	c.Header.Docs = len(c.Entries)
	return c, nil
}

func applyEntryKey(e *Entry, k, v string, ln int, path string, warnings *[]string) {
	switch k {
	case keyTitle:
		e.Title = strings.TrimSpace(v)
	case keyMTime:
		e.MTime = parseI64(strings.TrimSpace(v), ln, k, path, warnings)
	case keySize:
		e.Size = parseI64(strings.TrimSpace(v), ln, k, path, warnings)
	case keySections:
		s, n := parseSections(strings.TrimSpace(v))
		e.Sections = s
		e.SectionsTruncated = n
	case keyTruncated:
		if t := parseInt(strings.TrimSpace(v), ln, k, path, warnings); t > 0 {
			e.SectionsTruncated = t
		}
	case keyLinks:
		e.Links = splitList(strings.TrimSpace(v))
	case keyStale:
		b := strings.TrimSpace(v)
		switch b {
		case "true":
			e.Stale = true
		case "false":
			e.Stale = false
		default:
			*warnings = append(*warnings, fmt.Sprintf(
				"%s: %s: expected true or false, got %q (treated as false)", path, k, b))
		}
	default:
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: line %d: unknown key %q ignored", path, ln, k))
	}
}

func parseHeaderLine(h *Header, line string) {
	for _, part := range strings.Split(stripQuote(line), "  ·  ") {
		applyHeaderSegment(h, strings.TrimSpace(part))
	}
}

func applyHeaderSegment(h *Header, part string) {
	switch {
	case part == "":
		return
	case strings.HasPrefix(part, "rev "):
		h.Revision = int64(parseInt(strings.TrimPrefix(part, "rev "), 0, "rev", "", nil))
		return
	case strings.HasPrefix(part, "writer "):
		h.Writer = strings.TrimSpace(strings.TrimPrefix(part, "writer "))
		return
	}
	for _, k := range headerKeyPairs {
		for _, prefix := range k.prefixes {
			if !strings.HasPrefix(part, prefix) {
				continue
			}
			k.parse(h, strings.TrimSpace(strings.TrimPrefix(part, prefix)))
			return
		}
	}
}

// headerKeyPairs pairs every catalog.md header key with the spellings that
// came before it. All of them are accepted, so a catalog.md written by an
// older build keeps its corpus, revision, model, dim and timestamps instead
// of silently falling back to zero values.
//
// ★ The corpus key has **four** spellings: the current `corpus_dir`, the
//
//	`corpus` the header carried before the key was renamed (2026-11-06), and
//	the two Chinese ones the header went through. A catalog.md on disk is
//	not rewritten by a rename, so all of them have to parse — the header is
//	a file format, not a configuration surface.
var headerKeyPairs = []struct {
	prefixes []string
	parse    func(h *Header, value string)
}{
	{[]string{"corpus_dir:", "corpus:", "语料目录:", "语料:"}, func(h *Header, v string) {
		h.Corpus = v
	}},
	{[]string{"extraction rule v", "抽取规则 v"}, func(h *Header, v string) {
		h.IndexVersion = parseInt(v, 0, "IndexVersion", "", nil)
	}},
	{[]string{"model ", "模型 "}, func(h *Header, v string) {
		model, dim, ok := parseModelDim(v)
		h.Model = model
		if ok {
			h.Dim = dim
		}
	}},
	{[]string{"updated at ", "更新于 "}, func(h *Header, v string) {
		h.Updated = parseStamp(v)
	}},
	{[]string{"created at ", "建于 "}, func(h *Header, v string) {
		h.Created = parseStamp(v)
	}},
}

// parseModelDim splits a "name (dim N)" model segment.
func parseModelDim(s string) (model string, dim int, ok bool) {
	i := strings.Index(s, " (dim ")
	if i < 0 {
		return s, 0, false
	}
	dim = parseInt(strings.TrimSuffix(s[i+len(" (dim "):], ")"), 0, "dim", "", nil)
	return s[:i], dim, true
}

// isCatalogHeader reports whether the line is a catalog title, in either the
// current English spelling or the legacy Chinese one.
func isCatalogHeader(line string) bool {
	s := strings.TrimSpace(line)
	return s == catalogHeader || s == catalogHeaderLegacy
}

// docCount renders the document count of the catalog header.
func docCount(n int) string {
	if n == 1 {
		return "1 document"
	}
	return fmt.Sprintf("%d documents", n)
}

func stripQuote(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, ">") {
		s = strings.TrimSpace(strings.TrimPrefix(s, ">"))
	}
	return strings.TrimSpace(s)
}

func looksLikeKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func renderSections(ss []Section, truncated int) string {
	parts := make([]string, 0, len(ss)+1)
	for _, s := range ss {
		parts = append(parts, s.String())
	}
	out := strings.Join(parts, " · ")
	if truncated > 0 {
		out += fmt.Sprintf(" · [+%d]", truncated)
	}
	return out
}

func parseSections(v string) ([]Section, int) {
	var out []Section
	truncated := 0
	if v == "" {
		return nil, 0
	}
	for _, part := range strings.Split(v, " · ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, ok := strings.CutPrefix(part, "[+"); ok {
			if i := strings.Index(n, "]"); i >= 0 {
				if t := parseInt(strings.TrimSpace(n[:i]), 0, "sections_truncated", "", nil); t > 0 {
					truncated = t
				}
			}
			continue
		}
		out = append(out, parseSection(part))
	}
	return out, truncated
}

func parseSection(part string) Section {
	if !strings.HasSuffix(part, ")") {
		return Section{Title: part}
	}
	i := strings.LastIndex(part, sectionSep)
	if i <= 0 {
		return Section{Title: part}
	}
	line := parseInt(part[i+len(sectionSep):len(part)-1], 0, "section line", "", nil)
	if line <= 0 {
		return Section{Title: part}
	}
	return Section{Title: strings.TrimSpace(part[:i]), Line: line}
}

func splitList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return dedupAdjacent(out)
}

func dedupAdjacent(ss []string) []string {
	out := ss[:0]
	for i, s := range ss {
		if i > 0 && s == ss[i-1] {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\n") {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}

func orNone(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format(time.RFC3339)
}

var nowFn = time.Now

func newWriterToken() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%06x", time.Now().UnixNano()&0xffffff)
	}
	return hex.EncodeToString(b[:])
}

func parseStamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseI64(s string, ln int, key, path string, warnings *[]string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		warnBadInt(warnings, ln, key, path, s)
		return 0
	}
	return n
}

func parseInt(s string, ln int, key, path string, warnings *[]string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		warnBadInt(warnings, ln, key, path, s)
		return 0
	}
	return n
}

func warnBadInt(warnings *[]string, ln int, key, path, raw string) {
	if warnings == nil {
		return
	}
	if ln > 0 && path != "" {
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: line %d: %s: %q is not an integer, treated as 0", path, ln, key, raw))
		return
	}
	*warnings = append(*warnings, fmt.Sprintf("%s: %q is not an integer, treated as 0", key, raw))
}

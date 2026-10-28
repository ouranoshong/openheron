package kb

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

func Extract(rel, body string) Entry {
	lines, inCode := splitLines(body)

	e := Entry{
		Path:  rel,
		Title: extractTitle(body, lines, inCode),
	}
	e.Summary = extractSummary(body, lines, inCode)
	sections, truncated := extractSections(lines, inCode)
	e.Sections, e.SectionsTruncated = sections, truncated
	e.Links = extractLinks(body, rel)
	return e
}

func (e Entry) EmbedText() string {
	var b strings.Builder
	if e.Title != "" {
		b.WriteString(e.Title)
		b.WriteString("\n")
	}
	if e.Summary != "" {
		b.WriteString(e.Summary)
		b.WriteString("\n")
	}
	for _, s := range e.Sections {
		b.WriteString(s.Title)
		b.WriteString("\n")
	}
	if len(e.Links) > 0 {
		b.WriteString("links: ")
		b.WriteString(strings.Join(e.Links, " "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitLines(body string) (lines []string, inCode []bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if body == "" {
		return nil, nil
	}
	lines = strings.Split(body, "\n")
	inCode = make([]bool, len(lines))
	open := false
	for i, line := range lines {
		if isFence(line) {
			open = !open
			continue
		}
		inCode[i] = open
	}
	return lines, inCode
}

func isFence(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

var (
	frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---`)
	descRe        = regexp.MustCompile(`(?m)^description:\s*(.+?)\s*$`)
	h1Re          = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	headingRe     = regexp.MustCompile(`^(#{1,4})[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	mdLinkRe      = regexp.MustCompile(`\]\(\s*<?([^)>\s]+(?:\.md))>?[^)]*\)`)
	tldrHeadings  = []string{"0. 一句话", "一句话", "tldr", "tl;dr", "summary", "概要", "摘要"}
)

func extractTitle(body string, lines []string, inCode []bool) string {
	if m := frontmatterRe.FindStringSubmatch(body); m != nil {
		if t := matchField(m[1], "title"); t != "" {
			return t
		}
	}
	for i, line := range lines {
		if inCode[i] {
			continue
		}
		if m := h1Re.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func extractSummary(body string, lines []string, inCode []bool) string {
	if m := frontmatterRe.FindStringSubmatch(body); m != nil {
		if d := matchField(m[1], "description"); d != "" {
			return d
		}
	}

	if s := sectionSummary(lines, inCode); s != "" {
		return s
	}

	if s := quoteSummary(lines, inCode); s != "" {
		return s
	}

	return ""
}

func matchField(fm, key string) string {
	for _, line := range strings.Split(fm, "\n") {
		if line != strings.TrimLeft(line, " \t") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if v != "" {
			return v
		}
	}
	return ""
}

func sectionSummary(lines []string, inCode []bool) string {
	for i, line := range lines {
		if inCode[i] {
			continue
		}
		m := headingRe.FindStringSubmatch(line)
		if m == nil || len(m[1]) < 2 {
			continue
		}
		if !isTldrHeading(m[2]) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if inCode[j] {
				continue
			}
			t := strings.TrimSpace(lines[j])
			if t == "" || isStructural(t) {
				continue
			}
			return t
		}
	}
	return ""
}

func isStructural(t string) bool {
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "|") {
		return true
	}
	if t == "---" || t == "***" || t == "___" {
		return true
	}
	for _, b := range []string{"-", "*", "+"} {
		rest, ok := strings.CutPrefix(t, b)
		if !ok {
			continue
		}
		if rest == "" || isBlank(rune(rest[0])) {
			return true
		}
	}
	if i := strings.Index(t, "."); i > 0 && allDigits(t[:i]) {
		if rest := t[i+1:]; rest != "" && isBlank(rune(rest[0])) {
			return true
		}
	}
	if i := strings.Index(t, ")"); i > 0 && allDigits(t[:i]) {
		if rest := t[i+1:]; rest != "" && isBlank(rune(rest[0])) {
			return true
		}
	}
	return false
}

func isTldrHeading(title string) bool {
	t := normHeading(title)
	for _, a := range tldrHeadings {
		if t == a {
			return true
		}
	}
	return false
}

func normHeading(title string) string {
	t := strings.TrimSpace(strings.ToLower(title))
	for _, p := range []string{"§", "一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"} {
		if strings.HasPrefix(t, p) {
			t = strings.TrimSpace(strings.TrimPrefix(t, p))
			break
		}
	}
	i := strings.Index(t, ".")
	if i > 0 && allDigits(t[:i]) {
		t = strings.TrimSpace(t[i+1:])
	}
	return strings.Join(strings.Fields(t), " ")
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func quoteSummary(lines []string, inCode []bool) string {
	seen := false
	for i, line := range lines {
		if inCode[i] {
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, ">") {
			if seen {
				return ""
			}
			continue
		}
		seen = true
		if s := trimStatusPrefix(trimQuotes(t)); s != "" {
			return s
		}
	}
	return ""
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, ">") {
		s = strings.TrimSpace(strings.TrimPrefix(s, ">"))
	}
	return strings.TrimSpace(strings.TrimSuffix(s, ">"))
}

const statusLabel = "状态"

func trimStatusPrefix(s string) string {
	rest, ok := strings.CutPrefix(s, statusLabel)
	if !ok {
		return s
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimLeft(rest, "：:")
	rest = strings.TrimSpace(rest)

	if body, ok := strings.CutPrefix(rest, "**"); ok {
		if _, tail, found := strings.Cut(body, "**"); found {
			rest = tail
		}
	}

	if i := strings.IndexAny(rest, "；;"); i >= 0 {
		return trimLead(rest[i:])
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimRight(rest, "。")
	return trimLead(rest)
}

func trimLead(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "；;。·—-– ")
	return strings.TrimSpace(s)
}

func extractSections(lines []string, inCode []bool) ([]Section, int) {
	var out []Section
	total := 0
	for i, line := range lines {
		if inCode[i] {
			continue
		}
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if len(m[1]) < 2 {
			continue
		}
		total++
		if len(out) >= maxSectionsStored {
			continue
		}
		out = append(out, Section{Title: strings.TrimSpace(m[2]), Line: i + 1})
	}
	return out, total - len(out)
}

func extractLinks(body, rel string) []string {
	matches := mdLinkRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		l, ok := resolveLink(m[1], rel)
		if !ok || l == rel || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func resolveLink(target, from string) (string, bool) {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return "", false
	}
	if strings.Contains(target, "://") || strings.HasPrefix(target, "//") {
		return "", false
	}
	if len(target) > 1 && target[1] == ':' {
		return "", false
	}

	rel := target
	if strings.HasPrefix(target, "/") {
		rel = strings.TrimPrefix(target, "/")
	} else if from != "" {
		rel = path.Clean(path.Dir(from) + "/" + target)
	}
	rel = strings.TrimPrefix(rel, "./")

	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if rel == "." || rel == "" {
		return "", false
	}
	return rel, true
}

func metaStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if e == nil {
				continue
			}
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}

func metaInt(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case uint64:
		return int64(t)
	default:
		return 0
	}
}

func isBlank(r rune) bool { return unicode.IsSpace(r) }

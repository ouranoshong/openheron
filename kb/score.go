package kb

import (
	"math"
	"strings"
	"unicode"
)

const (
	wIdentSubstring = 1.0
	wIdentWord      = 0.8
	wProseSubstring = 0.4
)

type queryTerms struct {
	terms []string
}

func parseQuery(q string) queryTerms {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			add(b.String())
			b.Reset()
		}
	}
	for _, r := range q {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			add(string(r))
		case isIdentRune(r):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return queryTerms{terms: out}
}

func scoreDoc(q queryTerms, e Entry, vec []float32, qvec []float32) float64 {
	lex := lexScore(q, e)
	if len(vec) == 0 || len(qvec) == 0 || len(vec) != len(qvec) {
		return lex
	}
	cos := cosine(vec, qvec)
	if lex == 0 {
		return cos
	}
	return 0.5*lex + 0.5*cos
}

func lexScore(q queryTerms, e Entry) float64 {
	if len(q.terms) == 0 {
		return 0
	}

	identText := e.Title + "\n" + sectionTitles(e) + linkText(e)
	outline := e.EmbedText()
	proseText := e.Title + "\n" + e.Summary

	var sum float64
	for _, t := range q.terms {
		switch {
		case strings.Contains(identText, t):
			sum += wIdentSubstring
		case hasIdentWord(outline, t):
			sum += wIdentWord
		case strings.Contains(proseText, t):
			sum += wProseSubstring
		}
	}
	return sum / float64(len(q.terms))
}

func linkText(e Entry) string {
	if len(e.Links) == 0 {
		return ""
	}
	return strings.Join(e.Links, " ") + "\n"
}

func sectionTitles(e Entry) string {
	var b strings.Builder
	for _, s := range e.Sections {
		b.WriteString(s.Title)
		b.WriteString("\n")
	}
	return b.String()
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func hasIdentWord(text, word string) bool {
	if word == "" {
		return false
	}
	start := 0
	for {
		i := strings.Index(text[start:], word)
		if i < 0 {
			return false
		}
		lo := start + i
		hi := lo + len(word)
		if (lo == 0 || !isIdentRune(rune(text[lo-1]))) &&
			(hi >= len(text) || !isIdentRune(rune(text[hi]))) {
			return true
		}
		start = lo + 1
		if start >= len(text) {
			return false
		}
	}
}

func cosine(a, b []float32) float64 {
	n := len(a)
	if n == 0 || len(b) != n {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func normalize(v []float32) []float32 {
	var sq float64
	for _, x := range v {
		sq += float64(x) * float64(x)
	}
	if sq == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sq))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

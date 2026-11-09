package spec

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

func expandValue(v string) (value string, resolved bool, err error) {
	v = strings.TrimSpace(v)

	switch {
	case strings.HasPrefix(v, "@file:"):
		p := strings.TrimSpace(strings.TrimPrefix(v, "@file:"))
		if p == "" {
			return "", false, fmt.Errorf("@file: missing path after the prefix")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", false, fmt.Errorf("read secret file: %w", err)
		}
		return strings.TrimSpace(string(b)), true, nil

	case strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"):
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return "", false, fmt.Errorf("${} is missing a variable name")
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", false, nil
		}
		return strings.TrimSpace(val), true, nil

	default:
		return v, true, nil
	}
}

func envVarName(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return strings.TrimSpace(v[2 : len(v)-1])
	}
	return ""
}

// splitFrontmatter separates an agent file into its frontmatter block and its
// body, and reports the line of the file the block starts on.
//
// ★ The third return value exists for line numbers. The frontmatter is
// decoded as a block of its own, so a YAML error counts from the block —
// and the block starts at line 1 only when the `---` does. A file that
// opens with a blank line shifts every line by one, silently.
func splitFrontmatter(data []byte) (fm []byte, body string, startLine int, err error) {
	raw := string(data)
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, "", 0, nil
	}
	if !strings.HasPrefix(text, "---") {
		return nil, text, 0, nil
	}

	startLine = 1 + strings.Count(trimmedLeft(raw), "\n")

	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", startLine, fmt.Errorf("frontmatter closing delimiter not found")
	}
	fm = []byte(rest[:end])
	body = strings.TrimLeft(rest[end+4:], "\n")
	return fm, body, startLine, nil
}

// trimmedLeft is what strings.TrimSpace cut from the front of raw: counting
// its newlines is the whole of the line shift.
func trimmedLeft(raw string) string {
	return raw[:len(raw)-len(strings.TrimLeftFunc(raw, unicode.IsSpace))]
}

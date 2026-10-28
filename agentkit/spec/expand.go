package spec

import (
	"fmt"
	"os"
	"strings"
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

func splitFrontmatter(data []byte) ([]byte, string, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, "", nil
	}
	if !strings.HasPrefix(text, "---") {
		return nil, text, nil
	}

	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter closing delimiter not found")
	}
	fm := rest[:end]
	body := strings.TrimLeft(rest[end+4:], "\n")
	return []byte(fm), body, nil
}

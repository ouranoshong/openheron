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

// derivedSecretRef reads the name out of a "${NAME}" reference, when that
// name is one of the derived secrets.
//
// ★ Only the derived names qualify. A reference to anything else keeps its
//
//	old meaning — a plain environment lookup — and a name that merely
//	looks like one of them is not turned into a keyring lookup.
func derivedSecretRef(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "${") || !strings.HasSuffix(v, "}") {
		return "", false
	}
	name := strings.TrimSpace(v[2 : len(v)-1])
	if name == "" {
		return "", false
	}
	if _, ok := SecretUserFor(name); !ok {
		return "", false
	}
	return name, true
}

// secretNotice says why a name held no key.
//
// ★ The two places are named separately: a user who exported the variable
//
//	and a user who stored the key in the keyring are doing different
//	things, and the sentence has to tell them which one is missing. "No key"
//	on its own sends them looking in the wrong place.
func secretNotice(name string, sec Secret) string {
	switch {
	case sec.Empty:
		return fmt.Sprintf("%s is set but empty", name)
	case sec.Unavailable:
		return fmt.Sprintf("%s is not set and the keyring is not available on this machine", name)
	default:
		user, _ := SecretUserFor(name)
		return fmt.Sprintf("%s is not set and the keyring holds no %s", name, user)
	}
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

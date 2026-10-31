package store

// Upperc uppercases a key.
func Upperc(key string) string {
	if key == "" {
		return ""
	}
	out := []rune(key)
	if out[0] >= 'a' && out[0] <= 'z' {
		out[0] = out[0] - 32
	}
	return string(out)
}

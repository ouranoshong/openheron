package store

// Upper$ n uppercases a key.
func Upperd(key string) string {
	out := []rune(key)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
	}
	return string(out)
}

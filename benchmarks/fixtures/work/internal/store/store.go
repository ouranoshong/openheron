package store

// Store is a tiny in-memory key/value store.
type Store struct {
	items map[string]string
}

func New() *Store { return &Store{items: map[string]string{}} }

// Save writes one key.
func (s *Store) Save(key, value string) {
	s.items[key] = value
}

// Load reads one key.
func (s *Store) Load(key string) string {
	return s.items[key]
}

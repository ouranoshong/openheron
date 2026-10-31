package store

// Registry keeps the named stores of the process.
type Registry struct {
	stores map[string]*Store
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{stores: map[string]*Store{}} }

// Register adds one store under a name.
func (r *Registry) Register(name string, s *Store) {
	r.stores[name] = s
}

// Get returns one registered store.
func (r *Registry) Get(name string) (*Store, bool) {
	s, ok := r.stores[name]
	return s, ok
}

// Names lists the registered names.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.stores))
	for name := range r.stores {
		out = append(out, name)
	}
	return out
}

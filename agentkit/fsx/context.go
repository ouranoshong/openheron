package fsx

import "context"

// roundRootKey is the key the round's project root travels under.
//
// ★ It is a **private type**, so no other package can put a value under it
//
//	by accident — and the value is only meaningful to this package.
type roundRootKey struct{}

// WithProjectRoot returns a context that carries the project root of the
// round in flight.
//
// ★ The root is **per round**, not per process: a session belongs to a
// project, and a project is a directory. ★ The context is the only way the
// tools learn it — the filesystem backend is built once and shared by every
// round, so a root baked into it would be the root of the last round that
// used it.
//
// ★ The context is also the only channel that reaches **every** consumer:
// the framework hands the same context to the model, to the sub-agents and
// to every tool call, so one value settles the prompt and the write
// boundary at once.
func WithProjectRoot(ctx context.Context, root string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, roundRootKey{}, root)
}

// ProjectRoot returns the project root the context carries.
//
// ★ An empty string means "no root in the context": the caller falls back
// to the backend's own root — the default project root of the process.
func ProjectRoot(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	root, _ := ctx.Value(roundRootKey{}).(string)
	return root
}

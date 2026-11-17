// Package authz carries the session's authorization mode and the paths it
// trusts, from the round that starts to the tool call that asks.
//
// ★ It is a leaf: it holds no policy of its own, it only carries facts. The
//
//	policy — which writes may run without a nod — belongs to the gate
//	(agentkit/build), and the paths it trusts are the code's own facts.
//
// ★ Why a context and not a field on the agent: the agent is **pooled by
//
//	fingerprint** (agentkit/build) and shared by every session, so a mode
//	baked into it would leak from one session into every other. The context
//	is per round, so the mode in it is per session.
package authz

import (
	"context"

	"github.com/ouranoshong/openheron/contract"
)

// modeKey is the key the mode travels under.
//
// ★ It is a **private type**, so no other package can put a value under it
//
//	by accident — and the value is only meaningful to this package.
type modeKey struct{}

// workspaceKey is the key the session's workspace travels under.
//
// ★ It is a private type for the same reason: the workspace is the session's
//
//	own upload directory, and nothing else may put a path under this key.
type workspaceKey struct{}

// WithMode returns a context that carries the round's mode.
//
// ★ An empty mode becomes the default: the context always carries an answer,
//
//	and the answer is "ask" when nobody said otherwise.
func WithMode(ctx context.Context, mode contract.AuthorizationMode) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if mode == "" {
		mode = contract.AuthzDefault
	}
	return context.WithValue(ctx, modeKey{}, mode)
}

// ModeOf returns the mode the context carries.
//
// ★ An absent mode is the **default**: a context that never went through
//
//	WithMode is a context nobody set a mode on, and the safe answer is the
//	one that asks.
func ModeOf(ctx context.Context) contract.AuthorizationMode {
	if ctx == nil {
		return contract.AuthzDefault
	}
	m, _ := ctx.Value(modeKey{}).(contract.AuthorizationMode)
	if m == "" {
		return contract.AuthzDefault
	}
	return m
}

// WithSessionWorkspace returns a context that carries the session's own
// workspace — the directory its uploads live in.
//
// ★ It is **per session**, like the project root: the filesystem backend is
//
//	built once and shared, so a directory baked into it would be the
//	directory of the last session that used it.
func WithSessionWorkspace(ctx context.Context, dir string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workspaceKey{}, dir)
}

// SessionWorkspace returns the session's workspace the context carries.
//
// ★ An empty string means "no workspace in the context": the caller falls
//
//	back to the default project root of the process.
func SessionWorkspace(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	dir, _ := ctx.Value(workspaceKey{}).(string)
	return dir
}

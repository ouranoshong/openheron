package helpers

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
)

// ToolFailure is the fact that one tool call failed.
//
// ★ It is a fact about a **call**, not about the run: the model reads the
//
//	failure as text, and the interface needs to know that this call did not
//	do what it said it would.
//
// ★ The call id is the framework's own — the same id the wire's tool call
//
//	carries — so the two ends can be joined without guessing.
type ToolFailure struct {
	// ToolID is the tool call's id.
	ToolID string
	// ToolName is the tool's name.
	ToolName string
	// Error is the failure's own text.
	Error string
}

// ToolFailures collects the failures of one round.
//
// ★ The collector is **per round**: the tool call that fails and the
//
//	derivation that reports it are two ends of one round's pipe, and the
//	round is what they share.
//
// ★ The context is the only channel between them — the same one the project
//
//	root and the authorization mode travel in (`fsx.WithProjectRoot`,
//	`authz.WithMode`). ★ The framework hands one context to the model, to the
//	sub-agents and to every tool call, so a value put there reaches the tool
//	and nothing else has to be wired.
type ToolFailures struct {
	mu     sync.Mutex
	byCall map[string]ToolFailure
}

// toolFailuresKey is a private type, so no other package can put a value
// under it by accident.
type toolFailuresKey struct{}

// NewToolFailures returns an empty collector.
func NewToolFailures() *ToolFailures {
	return &ToolFailures{byCall: map[string]ToolFailure{}}
}

// WithToolFailures returns a context that carries the round's collector.
//
// ★ A nil collector is still carried: the context then reads as "this round
//
//	collects nothing", and the middleware's write is a no-op instead of a
//	nil dereference on a tool call that failed.
func WithToolFailures(ctx context.Context, f *ToolFailures) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolFailuresKey{}, f)
}

// ToolFailuresFrom returns the collector the context carries, or nil when it
// carries none.
func ToolFailuresFrom(ctx context.Context) *ToolFailures {
	if ctx == nil {
		return nil
	}
	f, _ := ctx.Value(toolFailuresKey{}).(*ToolFailures)
	return f
}

// Record remembers that a call failed.
//
// ★ A failure without a call id is dropped: it cannot be joined with the
//
//	call it belongs to, and a failure nobody can place is a fact nobody can
//	use.
func (f *ToolFailures) Record(failure ToolFailure) {
	if f == nil || failure.ToolID == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byCall == nil {
		f.byCall = map[string]ToolFailure{}
	}
	f.byCall[failure.ToolID] = failure
}

// Take returns the failure of a call and forgets it.
//
// ★ It forgets it: the fact is reported **once** — the derivation that
//
//	publishes it also stamps it on the archived message, and a second report
//	would be a second truth about the same call.
func (f *ToolFailures) Take(id string) (ToolFailure, bool) {
	if f == nil || id == "" {
		return ToolFailure{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	failure, ok := f.byCall[id]
	if ok {
		delete(f.byCall, id)
	}
	return failure, ok
}

// recordToolFailure remembers that the call named by tCtx failed.
//
// ★ This is the only place that knows the call failed: the layer above sees
//
//	a string, and the model sees the same string. ★ The call id comes from
//	the framework's own tool context, so it is the id the wire and the
//	transcript use.
func recordToolFailure(ctx context.Context, tCtx *adk.ToolContext, err error) {
	if err == nil {
		return
	}
	failures := ToolFailuresFrom(ctx)
	if failures == nil {
		return
	}
	id, name := "", ""
	if tCtx != nil {
		id, name = tCtx.CallID, tCtx.Name
	}
	failures.Record(ToolFailure{ToolID: id, ToolName: name, Error: err.Error()})
}

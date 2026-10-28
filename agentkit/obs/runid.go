package obs

import "context"

type runIDKey struct{}

func WithRunID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

func RunID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(runIDKey{}).(string)
	return id
}

func Prefix(ctx context.Context) string {
	id := RunID(ctx)
	if id == "" {
		return ""
	}
	return "[run=" + id + "] "
}

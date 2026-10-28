package obs

import "context"

// runIDKey 是 runId 在 ctx 里的位置。
//
// ★ 用一个**私有的空结构体**当键，而不是字符串：
//
//	字符串键会与别的包撞车（谁都能往 ctx 里塞一个 "runId"），
//	私有类型保证只有本包能读写这个值。
type runIDKey struct{}

// WithRunID 把本轮的标识放进 ctx。
//
// ★ 它是**这一轮**的标识，不是请求的标识：一次 HTTP 请求里可能有
//
//	多轮（审批恢复就是新的一轮），而每轮都有自己的日志前缀。
//
// ★ 空 id 会被忽略 —— 塞一个空串进去只会让日志出现 `[run=]`，
//
//	那种前缀比没有前缀更糟（看起来像有 id，其实没有）。
func WithRunID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

// RunID 取出本轮的标识；没有则返回空串。
func RunID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(runIDKey{}).(string)
	return id
}

// Prefix 返回本轮的日志前缀，形如 `[run=abc123] `。
//
// ★ 没有 id 时返回空串 —— 前缀必须能让人一眼看出「有没有 runId」，
//
//	而不是给出一个空壳。
//
// ★ 它是**前缀**而不是一个字段，因为这个仓库的日志约定是
//
//	`[来源] 内容`（见 [agentkit/run.Runtime.logf]）。前缀能被 grep 到，
//	字段要靠解析器。
func Prefix(ctx context.Context) string {
	id := RunID(ctx)
	if id == "" {
		return ""
	}
	return "[run=" + id + "] "
}

package filestore

import "context"

// Teardown stops a session's runtime state before its files are removed.
type Teardown interface {
	Cleanup(ctx context.Context, sessionID string) error
}

// Options carries the runtime dependencies injected after construction.
// Teardown may be nil at construction time: the Runtime that Teardown refers to
// is itself built with a reference to this manager.
type Options struct {
	Teardown Teardown
	// CheckpointLegacyRoot 是**老版**的 checkpoint 目录（<dataDir>/checkpoints）。
	//
	// ★ 只读回退：读 checkpoint 时新路径没有就试老路径 —— 老会话里还等着
	//   人审批的那一轮，恢复不了就等于让用户卡住。
	//
	//   可以为空（那时没有回退）。
	CheckpointLegacyRoot string
}

package run

import "errors"

var (
	// ErrSuperseded is returned by Turn.Wait when a newer turn took over.
	ErrSuperseded = errors.New("turn superseded")
	// ErrStartTimeout is returned when the turn did not become ready in time.
	ErrStartTimeout = errors.New("turn start timeout")
	// ErrNoPendingInterrupt rejects an approval that has no pending record.
	ErrNoPendingInterrupt = errors.New("no pending interrupt")

	// ErrNoCheckpoint rejects a resume whose checkpoint is gone.
	//
	// ★ 它回答的是「框架为什么没走恢复路径」：一轮被答复的轮次却发现没有
	//
	//	checkpoint 可恢复，说明状态已经没了（被隔离成 .corrupt、或被新一轮
	//	提问取代）。这时候必须**干脆地拒绝**，而不是让轮次起来又等超时。
	ErrNoCheckpoint = errors.New("no checkpoint to resume")
	// ErrSessionNotFound signals an unknown session id.
	ErrSessionNotFound = errors.New("session not found")

	// errInterrupted is internal: end the loop and record the pending approval.
	errInterrupted = errors.New("interrupted")
)

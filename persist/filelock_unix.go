//go:build !windows

package persist

import (
	"os"
	"syscall"
)

// ★ unix 实现：flock(2)。
//
//	flock 是**建议锁**：别的进程不主动 flock 也能读写同一个文件。
//	在本项目里这没问题 —— 会碰这些文件的写者都走 [FileLock]，没有裸写。
//
//	阻塞形态被刻意避开：flock(fd, LOCK_EX) 的阻塞调用**没法被 ctx 打断**，
//	于是「Ctrl-C 取消一次排队」会变成「进程卡在那里直到对方跑完」，
//	而对方可能正在跑一次十分钟的索引。非阻塞 + 轮询换来的是可取消，
//	代价是每次多一个 syscall —— 而排队场景本来就不常见。
//
// ★ 平台差异只有这一处：windows 那边是 [persist/filelock_windows.go] 的
// LockFileEx。两边的**语义必须一致**（整文件、非阻塞、忙时返回
// [errLockBusy]），上层逻辑在 [persist/filelock.go] 里是同一份。

// lockFile 尝试对 f 加排他锁。
//
// ★ 锁住的是**整个文件**（[0, 2^64)），不是某个字节区间 —— 锁文件里
//
//	写的是 pid，别的进程读它只需要能看到内容，不要求独占某一段。
//
// ★ 拿不到时返回 [errLockBusy]（不是别的错误）：上层靠这个区分
// 「别人正忙着」与「锁坏了」，前者可以等，后者要立刻报错。
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return errLockBusy
	}
	return err
}

// unlockFile 释放 [lockFile] 加上的锁。
//
// ★ 用 LOCK_UN 而不是关 fd：关掉之后下一个等待者要重新 open，而重新 open
//
//	与 flock 之间有一段窗口 —— 对「谁先谁后」有要求的地方那段窗口就是 bug。
//	fd 留到进程退出由内核收。
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

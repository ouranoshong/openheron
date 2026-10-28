//go:build windows

package persist

import (
	"os"

	"golang.org/x/sys/windows"
)

// ★ Windows 实现：LockFileEx / UnlockFileEx。
//
// ★ 与 unix 那边的**语义必须逐条对齐**（见 [persist/filelock_unix.go]）：
//
//	1. 锁住整个文件（偏移 0、长度 2^64-1），不是某个字节区间；
//	2. **非阻塞**：LOCKFILE_FAIL_IMMEDIATELY —— 拿不到立刻返回，
//	   不排队（上层要的是「可以等」而不是「必须等」）；
//	3. 拿不到时返回 [errLockBusy]，与 unix 那边同一个哨兵；
//	4. 释放用 UnlockFileEx 而不是关 fd。
//
// ★ 与 flock 的两处**真实**差别（都对我们有利或不涉及）：
//
//	- LockFileEx 是**强制锁**：别的进程读写被锁区间会失败。flock 是建议锁。
//	  本项目里会碰这些文件的写者都走 [FileLock]，没有裸写，所以强制锁
//	  只会更严，不会误伤。
//	- Windows 上锁随**持锁的那个句柄**关闭而释放；unix 上关掉**任意**
//	  一个同文件描述符都会释放 flock。这不是我们用得到的方向（我们只关
//	  自己的句柄），但记下来以免以后有人踩。
//
// ★ 这个实现**没有在 Windows 上跑过**（开发机是 linux）。它能编译，
//   语义与 unix 版逐条对齐，但首次在 Windows 上用知识库索引前，
//   值得手动验一次：起一个 `openheron kb index`，同时在另一个终端再起一个，
//   第二个应当立刻报「被别的进程占着」而不是两个都跑起来。

// lockFile 尝试对 f 加排他锁。
func lockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	// 长度取 2^64-1 ⇒ 整个文件。字节数是 uint32 对，所以高低各取满。
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), ol)
	if err == nil {
		return nil
	}
	if err == windows.ERROR_LOCK_VIOLATION {
		return errLockBusy
	}
	return err
}

// unlockFile 释放 [lockFile] 加上的锁。
func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), ol)
}

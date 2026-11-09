//go:build windows

package persist

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	// ★ One byte, not the whole file: the pid is written at holderOffset and
	//   is meant to stay readable while the lock is held.
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, ol)
	if err == nil {
		return nil
	}
	if err == windows.ERROR_LOCK_VIOLATION {
		return errLockBusy
	}
	return err
}

func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}

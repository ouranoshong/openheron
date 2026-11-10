package persist

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// renameWithRetry replaces dst with the finished temp file.
//
// ★ On Windows MoveFileEx fails with ERROR_ACCESS_DENIED while any handle
//
//	still has dst open — even a reader that is about to close it. On Linux
//	rename over an open file is legal, which is why this only shows up there.
//	The competing reader is a short os.ReadFile, so a handful of short waits
//	is enough; the retries stop as soon as the error is not that one.
func renameWithRetry(old, newPath string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.Rename(old, newPath); err == nil {
			return nil
		}
		if !isTransientRenameError(err) {
			return err
		}
		time.Sleep(time.Duration(5+attempt*5) * time.Millisecond)
	}
	return err
}

func AtomicWrite(dst string, data []byte, tmpSuffix string) error {
	if tmpSuffix == "" {
		return fmt.Errorf("persist: empty tmp suffix for %s", dst)
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("persist: create %s: %w", dir, err)
	}

	tmp := dst + tmpSuffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("persist: create %s: %w", tmp, err)
	}

	cleanup := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}

	if _, err := f.Write(data); err != nil {
		return cleanup(fmt.Errorf("persist: write %s: %w", tmp, err))
	}
	if err := f.Sync(); err != nil {
		return cleanup(fmt.Errorf("persist: sync %s: %w", tmp, err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: close %s: %w", tmp, err)
	}

	if err := renameWithRetry(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: rename %s -> %s: %w", tmp, dst, err)
	}

	syncDir(dir)
	return nil
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

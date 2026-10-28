package persist

import (
	"fmt"
	"os"
	"path/filepath"
)

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

	if err := os.Rename(tmp, dst); err != nil {
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

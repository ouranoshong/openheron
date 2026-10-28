package persist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteReplacesWholeFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "a.txt")

	if err := AtomicWrite(dst, []byte("first"), ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(dst, []byte("second-longer"), ".tmp"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second-longer" {
		t.Fatalf("content = %q, want %q", got, "second-longer")
	}
}

func TestAtomicWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "a.txt")
	if err := AtomicWrite(dst, []byte("x"), ".tmp"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want just a.txt", names)
	}
}

func TestAtomicWriteCreatesMissingDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "user", ".openheron", "policy")
	dst := filepath.Join(dir, "permissions.yaml")

	if err := AtomicWrite(dst, []byte("ask: [execute]\n"), ".tmp"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ask: [execute]\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestAtomicWriteRejectsEmptySuffix(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "a.txt")
	err := AtomicWrite(dst, []byte("x"), "")
	if err == nil {
		t.Fatal("an empty tmp suffix must be rejected")
	}
	if !strings.Contains(err.Error(), "tmp suffix") {
		t.Fatalf("error = %q", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("nothing should have been written")
	}
}

func TestAtomicWriteFailsWhenPathIsADirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := AtomicWrite(target, []byte("x"), ".tmp"); err == nil {
		t.Fatal("writing over a directory must fail")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("target must still be a directory (err=%v)", err)
	}
	if _, err := os.Stat(target + ".tmp"); err == nil {
		t.Fatal("a temp file was left behind")
	}
}

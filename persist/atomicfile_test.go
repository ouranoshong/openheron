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

// TestAtomicWriteLeavesNoTempFile 守住「失败不留残骸」的那一半。
//
// 残留 tmp 不会被下一次写入破坏（同名会被 O_TRUNC 覆盖），但它会让人
// 以为「保存过一次」，而一个看起来存在的临时文件是最难解释的现象之一。
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
	// 配置写入要能在「用户级目录压根不存在」时建出第一个文件 ——
	// 新项目的第一次配置就是这个形状。
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
	// 没有后缀 ⇒ 临时文件与目标同名 ⇒ rename 变成自己覆盖自己。
	// 那种情况下「原子」是假的，而失败点会推迟到第一次写入。
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
	// 失败不许把目标目录变成文件，也不许留下残骸。
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("target must still be a directory (err=%v)", err)
	}
	if _, err := os.Stat(target + ".tmp"); err == nil {
		t.Fatal("a temp file was left behind")
	}
}

package filestore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const testID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// newTestStore 造一个会话的 checkpoint 存取器。
func newTestStore(t *testing.T) *fileCheckpointStore {
	t.Helper()
	return newfileCheckpointStore(filepath.Join(t.TempDir(), "sessions"), testID, "")
}

// ckptPath 是这个会话的 checkpoint 在盘上的位置。
//
// ★ 它住在会话自己的 checkpoints/ 子目录里：<sessions>/<id>/checkpoints/<id>.ckpt
func ckptPath(root, id string) string {
	return filepath.Join(root, id, ckptDir, id+ckptExt)
}

func TestGetMissing(t *testing.T) {
	fs := newTestStore(t)
	data, ok, err := fs.Get(context.Background(), testID)
	if err != nil || ok || data != nil {
		t.Fatalf("want (nil,false,nil), got (%v,%v,%v)", data, ok, err)
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	fs := newTestStore(t)
	want := []byte("gob-payload-\x00\x01\xff")
	if err := fs.Set(context.Background(), testID, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := fs.Get(context.Background(), testID)
	if err != nil || !ok {
		t.Fatalf("want ok=true, got ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: %q != %q", got, want)
	}
}

func TestSetOverwrites(t *testing.T) {
	fs := newTestStore(t)
	ctx := context.Background()
	if err := fs.Set(ctx, testID, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set(ctx, testID, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, _, _ := fs.Get(ctx, testID)
	if string(got) != "second" {
		t.Fatalf("want second, got %q", got)
	}
}

func TestDeleteThenExists(t *testing.T) {
	fs := newTestStore(t)
	ctx := context.Background()
	if err := fs.Set(ctx, testID, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := fs.Delete(ctx, testID); err != nil {
		t.Fatal(err)
	}
	_, exists, err := fs.Get(ctx, testID)
	if err != nil || exists {
		t.Fatalf("want found=false, got %v %v", exists, err)
	}
}

// The framework calls Delete repeatedly and discards the result.
func TestDeleteIsIdempotent(t *testing.T) {
	fs := newTestStore(t)
	ctx := context.Background()
	if err := fs.Delete(ctx, testID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := fs.Delete(ctx, testID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

// Corruption must never brick a session: a broken checkpoint is quarantined
// and reported as absent, so the next Submit starts fresh instead of dying in
// the same place forever.
func TestCorruptionIsQuarantinedNotFatal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	fs := newfileCheckpointStore(dir, testID, "")
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"bad magic", []byte("XXXX\x01\x00\x00\x00\x00\x00\x00\x00\x03abc")},
		{"truncated header", []byte("CK")},
		{"length mismatch", []byte("CKPT\x01\x00\x00\x00\x00\x00\x00\x00\x09abc")},
		{"bad version", []byte("CKPT\x09\x00\x00\x00\x00\x00\x00\x00\x03abc")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := ckptPath(dir, testID)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.content, 0o644); err != nil {
				t.Fatal(err)
			}

			data, ok, err := fs.Get(ctx, testID)
			if err != nil {
				t.Fatalf("corruption must not surface as an error: %v", err)
			}
			if ok || data != nil {
				t.Fatalf("want absent, got (%v,%v)", data, ok)
			}
			if fileExists(path+ckptBadExt) == false {
				t.Fatal("expected a quarantined .corrupt artefact")
			}
			if fileExists(path) {
				t.Fatal("corrupt file should have been moved aside")
			}
			os.Remove(path + ckptBadExt)
		})
	}
}

// An empty file is a benign "no checkpoint" for the framework and must survive
// untouched rather than being quarantined.
func TestEmptyFileIsPassedThrough(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	fs := newfileCheckpointStore(dir, testID, "")
	path := ckptPath(dir, testID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	data, ok, err := fs.Get(context.Background(), testID)
	if err != nil || !ok || len(data) != 0 {
		t.Fatalf("want (empty,true,nil), got (%v,%v,%v)", data, ok, err)
	}
	if fileExists(path + ckptBadExt) {
		t.Fatal("empty file must not be quarantined")
	}
}

func TestCheckpointStoreRejectsUnsafeIDs(t *testing.T) {
	fs := newTestStore(t)
	ids := []string{"", "../../etc/passwd", "/abs/path", "a/b", "not-a-uuid", testID + "/x"}
	for _, id := range ids {
		if _, _, err := fs.Get(context.Background(), id); err == nil {
			t.Errorf("Get(%q) should have been rejected", id)
		}
		if err := fs.Set(context.Background(), id, []byte("x")); err == nil {
			t.Errorf("Set(%q) should have been rejected", id)
		}
		if err := fs.Delete(context.Background(), id); err == nil {
			t.Errorf("Delete(%q) should have been rejected", id)
		}
		if _, _, err := fs.Get(context.Background(), id); err == nil {
			t.Errorf("Get(%q) should have been rejected", id)
		}
	}
}

func TestAtomicWriteLeavesNoTmp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	fs := newfileCheckpointStore(dir, testID, "")
	if err := fs.Set(context.Background(), testID, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if fileExists(ckptPath(dir, testID) + ckptTmpExt) {
		t.Fatal("temp file left behind")
	}
}

func TestConcurrentAccess(t *testing.T) {
	fs := newTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = fs.Set(ctx, testID, []byte{byte(i)})
			_, _, _ = fs.Get(ctx, testID)
			_, _, _ = fs.Get(ctx, testID)
			if i%4 == 0 {
				_ = fs.Delete(ctx, testID)
			}
		}(i)
	}
	wg.Wait()
}

func TestEncodeDecodeHeader(t *testing.T) {
	if got, want := len(encodeCheckPoint(nil)), ckptHdrSize; got != want {
		t.Fatalf("empty payload should encode to %d bytes, got %d", want, got)
	}
	if _, err := decodeCheckPoint(nil); err == nil {
		t.Fatal("expected errEmptyCheckPoint")
	}
	payload := []byte("hello")
	out, err := decodeCheckPoint(encodeCheckPoint(payload))
	if err != nil || !bytes.Equal(out, payload) {
		t.Fatalf("header round trip failed: %v %q", err, out)
	}
}

// ★ 老布局的 checkpoint（<dataDir>/checkpoints/<id>.ckpt）要**读得到** ——
//
//	那一轮还等着人审批，读不到就再也恢复不了了。
func TestLegacyCheckpointIsStillReadable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	legacy := filepath.Join(t.TempDir(), "checkpoints")
	fs := newfileCheckpointStore(root, testID, legacy)
	ctx := context.Background()

	// 老文件：老布局是「一个会话一个平铺文件」。
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(legacy, testID+".ckpt")
	if err := os.WriteFile(old, encodeCheckPoint([]byte("legacy payload")), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok, err := fs.Get(ctx, testID)
	if err != nil || !ok {
		t.Fatalf("the legacy checkpoint must be readable, got ok=%v err=%v", ok, err)
	}
	if string(got) != "legacy payload" {
		t.Fatalf("payload = %q", got)
	}
	_, exists, err := fs.Get(ctx, testID)
	if err != nil || !exists {
		t.Fatalf("the legacy checkpoint must be visible through Get too, got %v %v", exists, err)
	}

	// 写只写新路径 —— 老文件不该被改写。
	if err := fs.Set(ctx, testID, []byte("new payload")); err != nil {
		t.Fatal(err)
	}
	if !fileExists(ckptPath(root, testID)) {
		t.Fatal("a new checkpoint must land in the session's own directory")
	}
}

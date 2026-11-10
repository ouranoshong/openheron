package filestore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newSession opens a session with a workspace of its own.
func newUploadSession(t *testing.T) *FileSession {
	t.Helper()
	fm := newManager(t, Options{})
	sess, err := fm.Create(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	fs, ok := sess.(*FileSession)
	if !ok {
		t.Fatalf("the session is a %T, want a *FileSession", sess)
	}
	return fs
}

// ★ The original name survives the renaming.
//
// ★ A file is stored under a hashed name, so the name the user recognises
//
//	exists only in the map. Without it the interface would show a hash and
//	call it a file name.
func TestTheOriginalNameSurvivesTheRenaming(t *testing.T) {
	sess := newUploadSession(t)

	if err := sess.RecordUpload("shot-a3f9c2b1.png", "shot.png"); err != nil {
		t.Fatal(err)
	}
	names := sess.OriginalNames()
	if names["shot-a3f9c2b1.png"] != "shot.png" {
		t.Fatalf("the name is %q, want the original", names["shot-a3f9c2b1.png"])
	}
}

// ★ The map is read once, and it is empty when there is nothing to map.
func TestTheMapIsEmptyWhenNothingWasRenamed(t *testing.T) {
	sess := newUploadSession(t)

	names := sess.OriginalNames()
	if len(names) != 0 {
		t.Fatalf("want an empty map, got %+v", names)
	}
}

// ★ Two files with the same original name are two entries.
//
// ★ The map is keyed by the path, not by the name: one name can be
//
//	uploaded twice, and the two files are different.
func TestTwoFilesWithTheSameNameAreTwoEntries(t *testing.T) {
	sess := newUploadSession(t)

	if err := sess.RecordUpload("shot-aaaa.png", "shot.png"); err != nil {
		t.Fatal(err)
	}
	if err := sess.RecordUpload("shot-bbbb.png", "shot.png"); err != nil {
		t.Fatal(err)
	}
	names := sess.OriginalNames()
	if len(names) != 2 {
		t.Fatalf("want 2 entries, got %+v", names)
	}
	if names["shot-aaaa.png"] != "shot.png" || names["shot-bbbb.png"] != "shot.png" {
		t.Fatalf("the entries are %+v", names)
	}
}

// ★ Concurrent uploads do not lose an entry.
//
// ★ The map is read, changed and written under the session's lock. Without
//
//	it two uploads at the same moment would both read the same old map and
//	the second write would drop the first file's name.
func TestConcurrentUploadsDoNotLoseAnEntry(t *testing.T) {
	sess := newUploadSession(t)

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "file-" + string(rune('a'+i)) + ".txt"
			if err := sess.RecordUpload(path, path); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	names := sess.OriginalNames()
	if len(names) != n {
		t.Fatalf("want %d entries, got %d (%+v)", n, len(names), names)
	}
}

// ★ The map lives beside the session, not inside the workspace.
//
// ★ The workspace is the directory the **model** reads. A map from hashed
//
//	names to the names the user typed means nothing to it, and a file in
//	there is a file the model might pick up.
func TestTheMapLivesBesideTheSessionNotInsideTheWorkspace(t *testing.T) {
	sess := newUploadSession(t)

	if err := sess.RecordUpload("shot-a3f9c2b1.png", "shot.png"); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(sess.Workspace(), uploadsFile)
	if fileExists(inside) {
		t.Fatalf("the map must not be in the workspace: %s", inside)
	}
	beside := filepath.Join(filepath.Dir(sess.Workspace()), uploadsFile)
	if !fileExists(beside) {
		t.Fatalf("the map is not beside the session either: %s", beside)
	}
}

// ★ Deleting a file takes its name with it.
//
// ★ An entry that outlives its file would show up in the listing as a
//
//	name for something that is no longer there.
func TestDeletingAFileTakesItsNameWithIt(t *testing.T) {
	sess := newUploadSession(t)

	path, err := sess.PutFile("shot-a3f9c2b1.png", strings.NewReader("bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RecordUpload("shot-a3f9c2b1.png", "shot.png"); err != nil {
		t.Fatal(err)
	}
	if err := sess.DeleteWorkspaceFile("shot-a3f9c2b1.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file is still there")
	}
	if names := sess.OriginalNames(); len(names) != 0 {
		t.Fatalf("the entry survived the delete: %+v", names)
	}
}

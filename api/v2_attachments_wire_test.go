package api

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/message"
)

// ★ The attachment list reaches the client through the metadata.
//
//	★ Without it the history cannot show "that image you just sent" — the
//	  transcript holds the list, and the client is the only one who reads it.
func TestTheAttachmentListTravelsInTheMetadata(t *testing.T) {
	msg := schema.UserMessage("what is in this picture?")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: 12, MIME: "image/png"},
	})

	out, ok := toAgUiMessage(msg, 0)
	if !ok {
		t.Fatal("a message with attachments must be rendered")
	}
	raw, present := out.Metadata["attachments"]
	if !present {
		t.Fatalf("the metadata carries no attachment list: %+v", out.Metadata)
	}
	list, ok := raw.([]message.Attachment)
	if !ok || len(list) != 1 {
		t.Fatalf("the list is not the attachment list: %#v", raw)
	}
	if list[0].Name != "shot.png" || list[0].Path != "shot.png" {
		t.Fatalf("the attachment lost its identity: %+v", list[0])
	}
}

// ★ A message that carries only attachments is still a message.
//
//	★ The question may be entirely in the file. A bubble with no text and
//	  no chip would read as "nothing was sent" — and the user did send
//	  something.
func TestAMessageWithOnlyAttachmentsIsNotDropped(t *testing.T) {
	msg := schema.UserMessage("")
	message.StampAttachments(msg, []message.Attachment{
		{Name: "shot.png", Path: "shot.png", Size: 12, MIME: "image/png"},
	})

	if _, ok := toAgUiMessage(msg, 0); !ok {
		t.Fatal("a message that carries only attachments must not be dropped")
	}
}

// ★ A message with no attachments and no text is still dropped — the
//
//	change above must not turn every empty message into a bubble.
func TestAMessageWithNothingIsStillDropped(t *testing.T) {
	if _, ok := toAgUiMessage(schema.UserMessage(""), 0); ok {
		t.Fatal("an empty message carries nothing to render")
	}
}

// ★ The path the client gets back is **relative** to the workspace.
//
//	★ It is the name the client sends back with the next question, so it
//	  has to be the name the server itself resolves — not a directory on
//	  somebody's disk.
//
//	★ The name on disk carries a hash, so it is not the name that was
//	  typed. The **name** the user recognises is reported separately.
func TestTheUploadedPathIsRelativeToTheWorkspace(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", map[string]string{
		"notes.md": "hello",
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("upload = %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(out.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(out.Files))
	}
	path := out.Files[0].Path
	if path != filepath.Base(path) || strings.Contains(path, "/") {
		t.Fatalf("path = %q, want the workspace-relative name", path)
	}
	if !strings.HasPrefix(path, "notes-") || !strings.HasSuffix(path, ".md") {
		t.Fatalf("path = %q, want the hashed name of notes.md", path)
	}
	if out.Files[0].Name != "notes.md" {
		t.Fatalf("name = %q, want the name the user typed", out.Files[0].Name)
	}
	if out.Files[0].Mime != "text/markdown" {
		t.Fatalf("mime = %q, want text/markdown", out.Files[0].Mime)
	}
}

// ★ Deleting a file removes it from the workspace.
//
//	★ The chip's × used to hide the file from the client while the file
//	  stayed on the server — the user was told it was gone, and it was not.
func TestDeletingAFileRemovesItFromTheWorkspace(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", map[string]string{
		"removable.txt": "bye",
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("upload = %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(out.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(out.Files))
	}

	// ★ The delete names the file the way the server named it. The typed
	//   name is not on disk any more — the disk name carries a hash.
	del := callJSON(t, srv, "DELETE", "/v2/sessions/"+id+"/files?path="+url.QueryEscape(out.Files[0].Path), nil)
	if del.code != consts.StatusOK {
		t.Fatalf("delete = %d (%s)", del.code, del.body)
	}

	listing := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/files", nil)
	var ls struct {
		Files []workspaceEntry `json:"files"`
	}
	if err := json.Unmarshal([]byte(listing.body), &ls); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(ls.Files) != 0 {
		t.Fatalf("the file is still there: %+v", ls.Files)
	}
}

// ★ A name that escapes the workspace is refused by the delete too.
//
//	★ The rule that keeps a read inside the session has to keep a delete
//	  inside it — otherwise a client could remove a file the session never
//	  owned.
func TestDeletingOutsideTheWorkspaceIsRefused(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "DELETE", "/v2/sessions/"+id+"/files?path="+url.QueryEscape("../escape.txt"), nil)
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400 (%s)", got.code, got.body)
	}
}

// ★ A delete without a name is a bad request, not a silent success.
func TestDeletingWithoutANameIs400(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "DELETE", "/v2/sessions/"+id+"/files", nil)
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400 (%s)", got.code, got.body)
	}
}

// ★ Deleting a file that is not there is not an error.
//
//	The caller asked for the file to be gone, and it is gone. A second
//	delete of the same file is a no-op.
func TestDeletingAMissingFileIsNotAnError(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "DELETE", "/v2/sessions/"+id+"/files?path=never-existed.txt", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200 (%s)", got.code, got.body)
	}
}

// ★ The content type comes from the **name**, not from the client.
//
//	★ A client that called a text file "image/png" would otherwise have
//	  the server read it as a picture, and the model would answer about a
//	  picture it never saw.
func TestTheContentTypeComesFromTheName(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", map[string]string{
		"notes.md": "hello",
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("upload = %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if out.Files[0].Mime != "text/markdown" {
		t.Fatalf("mime = %q, want text/markdown", out.Files[0].Mime)
	}
}

// ★ The uploaded file is really on disk, under the name the client was told.
func TestTheUploadedFileIsWhereTheClientWasTold(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files", map[string]string{
		"notes.md": "hello",
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("upload = %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	full := filepath.Join(out.WorkspacePath, out.Files[0].Path)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("the file is not where the client was told: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("the content is %q", string(data))
	}
}

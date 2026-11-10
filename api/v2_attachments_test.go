package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// stubSession is a session with a workspace and nothing else.
type stubSession struct {
	storage.Session
	root string
}

func (s *stubSession) Workspace() string { return s.root }

func (s *stubSession) OriginalNames() map[string]string { return map[string]string{} }

func (s *stubSession) RecordUpload(string, string) error { return nil }

func (s *stubSession) ResolveWorkspaceFile(name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) || filepath.HasPrefix(clean, "..") {
		return "", os.ErrInvalid
	}
	full := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, full)
	if err != nil || filepath.HasPrefix(rel, "..") {
		return "", os.ErrInvalid
	}
	return full, nil
}

func newStubSession(t *testing.T) *stubSession {
	t.Helper()
	return &stubSession{root: t.TempDir()}
}

// ★ A name that escapes the workspace is refused.
//
//	The client names a file relative to the workspace; the server joins it
//	against the session's own workspace. A name that climbs out of it is a
//	name the client has no business sending, and it is refused rather than
//	clamped — a clamped path would read a different file.
func TestAnAttachmentThatEscapesTheWorkspaceIsRefused(t *testing.T) {
	sess := newStubSession(t)

	for _, name := range []string{
		"../escape.txt",
		"../../etc/passwd",
		"/abs.txt",
		`\abs.txt`,
		"sub/../../escape.txt",
	} {
		_, err := resolveAttachments(sess, []runAttachment{{Name: name, Path: name}})
		if err == nil {
			t.Fatalf("the name %q must be refused", name)
		}
	}
}

// ★ A file that is not there is refused.
//
//	An attachment that does not exist would send the model looking for
//	something that is not there: it would report "the file is missing"
//	about something the user believes they sent.
func TestAMissingAttachmentIsRefused(t *testing.T) {
	sess := newStubSession(t)

	_, err := resolveAttachments(sess, []runAttachment{{Name: "gone.md", Path: "gone.md"}})
	if err == nil {
		t.Fatal("a missing attachment must be refused")
	}
}

// ★ A directory is not an attachment.
func TestADirectoryIsNotAnAttachment(t *testing.T) {
	sess := newStubSession(t)
	if err := os.MkdirAll(filepath.Join(sess.root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := resolveAttachments(sess, []runAttachment{{Name: "sub", Path: "sub"}})
	if err == nil {
		t.Fatal("a directory must be refused")
	}
}

// ★ A file that is there is accepted, and the size comes from the file —
//
//	not from what the client said.
func TestAnAttachmentTakesItsSizeFromTheFile(t *testing.T) {
	sess := newStubSession(t)
	path := filepath.Join(sess.root, "a.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	atts, err := resolveAttachments(sess, []runAttachment{
		{Name: "a.txt", Path: "a.txt", Size: 999999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 {
		t.Fatalf("got %d attachments, want 1", len(atts))
	}
	if atts[0].Size != 10 {
		t.Fatalf("size = %d, want the file's own size (10)", atts[0].Size)
	}
	if atts[0].Name != "a.txt" || atts[0].Path != "a.txt" {
		t.Fatalf("the attachment lost its identity: %+v", atts[0])
	}
}

// ★ No attachments is not an error.
func TestNoAttachmentsResolveToNothing(t *testing.T) {
	sess := newStubSession(t)

	atts, err := resolveAttachments(sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 0 {
		t.Fatalf("got %d attachments, want none", len(atts))
	}
}

// ★ A turn that carries files and no text is still a new turn.
//
//	Without this, a request with only attachments would be read as a
//	re-attach: the client would be sent after a run that does not exist.
func TestATurnWithOnlyAttachmentsIsNotAReattach(t *testing.T) {
	in := runInput{
		ThreadID:    "t1",
		RunID:       "r1",
		Attachments: []runAttachment{{Name: "a.png", Path: "a.png"}},
	}
	if isAttachRequest(in) {
		t.Fatal("a turn with attachments is a new turn, not a re-attach")
	}
}

// ★ The attachments travel with the query, so the transcript records them.
func TestTheAttachmentsReachTheTranscript(t *testing.T) {
	sess := newStubSession(t)
	path := filepath.Join(sess.root, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	atts, err := resolveAttachments(sess, []runAttachment{{Name: "a.txt", Path: "a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.NewUser("what does it say?")
	message.StampAttachments(msg, atts)

	stored, ok := message.ReadAttachments(msg)
	if !ok || len(stored) != 1 {
		t.Fatalf("the message carries %+v, want one attachment", stored)
	}
	if stored[0].Path != "a.txt" {
		t.Fatalf("the attachment lost its path: %+v", stored[0])
	}
}

var _ = context.Background

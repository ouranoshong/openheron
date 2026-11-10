package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// resolveAttachments turns the client's relative paths into the attachment
// list the transcript records.
//
// ★ The client names a file **relative to the session's workspace**, and the
//
//	server joins it against the session's own workspace. A name that escapes
//	the workspace is refused — not clamped, because a clamped path would
//	resolve to a different file and the model would answer about that file
//	without anything saying so.
//
// ★ The path is checked for existence, because an attachment that is not
//
//	there would send the model looking for a file that does not exist: the
//	model would report "the file is missing" about something the user
//	believes they sent.
//
// ★ The list keeps the name the client used and the path the server
//
//	resolved. The name is what the user recognises; the path is what the
//	transcript needs.
func resolveAttachments(sess storage.Session, in []runAttachment) ([]message.Attachment, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]message.Attachment, 0, len(in))
	for _, a := range in {
		if strings.TrimSpace(a.Path) == "" {
			return nil, fmt.Errorf("attachment %q has no path", a.Name)
		}
		abs, err := sess.ResolveWorkspaceFile(a.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", a.Name, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("attachment %q is a directory", a.Name)
		}
		name := a.Name
		if name == "" {
			name = filepath.Base(a.Path)
		}
		out = append(out, message.Attachment{
			Name: name,
			Path: a.Path,
			Size: info.Size(),
			// ★ The type comes from the name, not from the client. The
			//   client may call a text file "image/png", and the assembly
			//   would then hand the model bytes that are not a picture.
			MIME: message.MIMEOfExt(a.Path),
		})
	}
	return out, nil
}

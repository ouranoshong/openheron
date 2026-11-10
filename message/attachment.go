package message

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// ExtraAttachments marks the messages that carry attachments.
//
// ★ The list belongs to the **message**, not to the session: only a
//
//	message-level binding can answer "that image you just sent".
//	A session-level list could only answer "what does this session have",
//	which is not the same question.
const ExtraAttachments = "openheron_attachments"

// Attachment is one entry of a message's attachment list.
//
// ★ Path is **relative to the session's workspace**, never absolute.
//
//	An absolute path is a fact about one machine: written into the
//	transcript it becomes a record that is only true on the machine that
//	produced it. The workspace is the session's own directory, so a
//	relative name is both portable and confined — the server joins it
//	against the session's workspace and refuses anything that escapes.
type Attachment struct {
	// Name is the file name as the user knows it.
	Name string `json:"name"`

	// Path is the name relative to the session's workspace.
	Path string `json:"path"`

	// Size is the file's size in bytes.
	Size int64 `json:"size"`

	// MIME is the content type, when it is known.
	MIME string `json:"mime,omitempty"`
}

// StampAttachments writes the attachment list onto the message.
//
// ★ The list is stored as **JSON bytes**, not as a []Attachment.
//
//	Extra is encoded with gob when the checkpoint is written, and gob
//	cannot encode a value of an unregistered type inside an interface — a
//	[]Attachment would fail the checkpoint write at stop time, and the
//	session would be left with a half-written approval. Bytes are on the
//	allow-list, and JSON is what the transcript carries anyway.
//
// ★ The list is written **once**, like every other stamp: the message's
//
//	identity is decided when it is born, and a message that is stamped
//	again on its way through the pipeline keeps what it was born with.
func StampAttachments(msg *schema.Message, atts []Attachment) {
	if msg == nil || len(atts) == 0 {
		return
	}
	data, err := json.Marshal(atts)
	if err != nil {
		return
	}
	put(msg, ExtraAttachments, data)
}

// ReadAttachments reads the attachment list off the message.
//
// ★ Three shapes are accepted:
//
//   - the JSON **bytes** this package writes;
//
//   - the **base64 string** those bytes become after a JSON round trip
//     (JSON encodes []byte as a string, so this is the shape a reloaded
//     transcript carries);
//
//   - the struct slice a caller may have put there directly.
//
//     The JSON is the shape that survives a transcript round trip.
func ReadAttachments(msg *schema.Message) ([]Attachment, bool) {
	if msg == nil || msg.Extra == nil {
		return nil, false
	}
	switch v := msg.Extra[ExtraAttachments].(type) {
	case []byte:
		return decodeAttachments(v)
	case string:
		// ★ JSON encodes []byte as a base64 string, so a message that has
		//   been through the transcript's JSON round trip carries the list
		//   in this shape.
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, false
		}
		return decodeAttachments(raw)
	case []Attachment:
		return v, true
	case *[]Attachment:
		if v == nil {
			return nil, false
		}
		return *v, true
	case []any:
		out := make([]Attachment, 0, len(v))
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, attachmentFromMap(m))
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

func decodeAttachments(raw []byte) ([]Attachment, bool) {
	var atts []Attachment
	if err := json.Unmarshal(raw, &atts); err != nil || len(atts) == 0 {
		return nil, false
	}
	return atts, true
}

// HasAttachments reports whether the message carries an attachment list.
func HasAttachments(msg *schema.Message) bool {
	atts, ok := ReadAttachments(msg)
	return ok && len(atts) > 0
}

// MIMEOfExt guesses a content type from a file name.
//
// ★ The **name** decides, not the client. A client that calls a text file
//
//	"image/png" would otherwise make the server read it as an image and
//	hand the model bytes that are not a picture — and the model would
//	answer about a picture it never saw.
//
// ★ An unknown extension is "application/octet-stream", not a guess: the
//
//	consequence of a wrong guess is a file the model cannot read, while
//	the consequence of the honest answer is a file it reads as text.
func MIMEOfExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt":
		return "text/plain"
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	case ".pdf":
		return "application/pdf"
	case ".xml":
		return "application/xml"
	case ".zip":
		return "application/zip"
	case ".tar", ".gz":
		return "application/gzip"
	default:
		return "application/octet-stream"
	}
}

func attachmentFromMap(m map[string]any) Attachment {
	return Attachment{
		Name: stringOf(m["name"]),
		Path: stringOf(m["path"]),
		Size: int64Of(m["size"]),
		MIME: stringOf(m["mime"]),
	}
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func int64Of(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

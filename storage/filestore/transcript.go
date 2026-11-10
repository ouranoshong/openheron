package filestore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

const (
	headerType = "header"

	// messageKind is the only message representation openheron persists.
	messageKind = "message"

	// maxLineBytes bounds a single JSONL record. The default bufio limit is
	// 64KiB, which tool results routinely exceed.
	maxLineBytes = 8 << 20
)

// logHeader is the first line of every session.jsonl.
type logHeader struct {
	Type        string    `json:"type"`
	Version     int       `json:"version"`
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	MessageKind string    `json:"message_kind"`
}

// loadTranscript decodes this session's session.jsonl into fs.messages.
func (fs *FileSession) loadTranscript() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.transcriptLocked()
}

// transcriptLocked decodes this session's session.jsonl into fs.messages.
// The caller must hold fs.mu.
func (fs *FileSession) transcriptLocked() error {
	if fs.loaded {
		return nil
	}
	fs.loaded = true

	path := messagesPath(fs.root, fs.id)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	h, err := readHeader(sc, path)
	if err != nil {
		return err
	}
	if h.ID != fs.id {
		fs.logger().Warn("[session] transcript header id differs from its directory; "+
			"later appends will go to the header's directory",
			"session", fs.id, "headerID", h.ID, "path", path)
	}
	fs.id = h.ID
	fs.ckpt.setID(h.ID)
	if !h.CreatedAt.IsZero() {
		fs.createdAt = h.CreatedAt
	}

	msgs, skipped, err := readMessages(sc)
	fs.messages = msgs
	if skipped > 0 {
		fs.logger().Warn("[session] skipped malformed message lines",
			"session", fs.id, "lines", skipped, "path", path)
	}
	return err
}

// readHeader consumes the first line of a transcript and checks it against the
// format openheron understands.
func readHeader(sc *bufio.Scanner, path string) (logHeader, error) {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return logHeader{}, err
		}
		return logHeader{}, fmt.Errorf("empty session file: %s", path)
	}
	var h logHeader
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
		return logHeader{}, fmt.Errorf("bad session header in %s: %w", path, err)
	}
	if h.Type != headerType {
		return logHeader{}, fmt.Errorf("session file %s does not start with a header", path)
	}
	if h.Version != formatVersion {
		return logHeader{}, fmt.Errorf("session file %s has version %d; openheron expects %d", path, h.Version, formatVersion)
	}
	if h.MessageKind != messageKind {
		return logHeader{}, fmt.Errorf("cannot load session %s: session file uses message_kind=%q; openheron only understands %q",
			path, h.MessageKind, messageKind)
	}
	return h, nil
}

// initTranscript writes a fresh header to this session's transcript, failing if
// the file already exists.
//
// ★ The caller must hold fs.mu: it reads the id.
func (fs *FileSession) initTranscript(createdAt time.Time) error {
	path := messagesPath(fs.root, fs.id)
	h := logHeader{
		Type:        headerType,
		Version:     formatVersion,
		ID:          fs.id,
		CreatedAt:   createdAt.UTC(),
		MessageKind: messageKind,
	}
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// readMessages decodes the message lines of a transcript.
// Malformed lines are skipped and counted rather than dropped silently: a
// truncated tail after a crash must not be mistaken for a clean log.
func readMessages(sc *bufio.Scanner) ([]*schema.Message, int, error) {
	out := make([]*schema.Message, 0)
	skipped := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		msg, err := unmarshalLine([]byte(line))
		if err != nil {
			skipped++
			continue
		}
		out = append(out, msg)
	}
	return out, skipped, sc.Err()
}

// marshalLine encodes one message as a session.jsonl record.
func marshalLine(msg *schema.Message) ([]byte, error) {
	return json.Marshal(msg)
}

// unmarshalLine decodes one session.jsonl record.
func unmarshalLine(line []byte) (*schema.Message, error) {
	var msg schema.Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// writeMessage appends one message to the transcript.
func (fs *FileSession) writeMessage(msg *schema.Message) error {
	data, err := marshalLine(msg)
	if err != nil {
		return err
	}
	// ★ The id is read under the lock, and the append happens outside it:
	//   the append is I/O, and the lock is not there to serialise I/O.
	//   It matters that the id is read at all: the transcript's header is
	//   authoritative, so the id can change once, while an append may be
	//   racing that first load.
	id, _ := fs.identity()
	f, err := os.OpenFile(messagesPath(fs.root, id), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

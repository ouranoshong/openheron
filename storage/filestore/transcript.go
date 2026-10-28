package filestore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/obs"
)

// 本文件是 session.jsonl 的**唯一**编解码处。
//
// ★ 磁盘格式的四个硬编码点都在这里：header 的字段名、message_kind 的值、
//
//	version 的比对、每行的字节上限。动它们等于换一种磁盘格式 —— 已有的
//	会话目录会读不出来。

const (
	// headerType 是 header 行的 type 字段取值。
	headerType = "header"

	// messageKind is the only message representation openheron persists.
	//
	// ★ 这个值是**磁盘契约**：改它等于让已有的 session.jsonl 全部读不出来。
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
//
// ★ transcript 可能很大（长会话几百条消息），而 List 与 Inspect 每轮都要
//
//	问会话要状态 —— 那些路径不需要它。所以它**只在真的要读时**才读盘，
//	而且只读一次。
func (fs *FileSession) loadTranscript() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.transcriptLocked()
}

// transcriptLocked decodes this session's session.jsonl into fs.messages.
// The caller must hold fs.mu.
//
// ★ fs.id 以 header 为准：磁盘上的 header 才是这份 transcript 的身份证，
//
//	目录名只是存放位置（手动挪过目录就会不一致），拿目录名当 id 会让后续
//	追加写到另一个目录去 —— checkpoint 的存取器也得跟着走。
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
		obs.Logger().Warn("[session] transcript header id differs from its directory; "+
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
		obs.Logger().Warn("[session] skipped malformed message lines",
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
// ★ O_EXCL：会话 id 是新铸的，这个文件本不该存在；它是保险而不是分支 ——
//
//	真撞上了就让创建失败，而不是在已有日志上再写一个会话。
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
//
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
//
// ★ JSONL 记录是**这一层**的磁盘契约，不是任何线格式的一部分：客户端看的
//
//	AG-UI 事件是另一套形状，两者的生命周期与版本策略各管各的。
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
//
// ★ 一条消息一次 write 落盘：分两次写就可能与并发写交错，一行被劈成两半。
func (fs *FileSession) writeMessage(msg *schema.Message) error {
	data, err := marshalLine(msg)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(messagesPath(fs.root, fs.id), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

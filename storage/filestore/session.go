package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

var _ storage.Session = (*FileSession)(nil)

const defaultTitle = "New Session"

const maxAuditRecords = 50

// FileSession is one conversation on disk.
// messages are backed by filestore.jsonl and appended in O_APPEND; pending lives
// in state.json, which is rewritten atomically. Memory is only a cache of both.
type FileSession struct {
	// mu guards every mutable field of this struct: the identity fields
	// (id, createdAt, updatedAt) as well as the cached content (messages,
	// loaded, pending, title, count, agentID, decisions).
	//
	// ★ It is a plain Mutex, not an RWMutex, and the reason is the id:
	//
	//	id is NOT immutable. The transcript's header is authoritative, so
	//	the first load may replace it (transcriptLocked, under this lock).
	//	A session therefore has an identity that can change once, while it
	//	is already reachable from the API handlers — which is why the
	//	accessors below take the lock instead of reading the field.
	//
	// ★ It sits at the top on purpose. A mutex in the middle of a struct
	//   reads as "it guards the fields below it", and that reading is how
	//   the identity fields ended up outside the protection.
	mu sync.Mutex

	id        string
	createdAt time.Time
	updatedAt time.Time
	root      string
	log       *slog.Logger

	ckpt *fileCheckpointStore
	// ledger is this session's run ledger. It lives next to the transcript,
	// in the same directory.
	ledger *fileRunLedger

	messages []*schema.Message
	loaded   bool
	pending  *storage.PendingApproval
	// title is the cached display title. Empty means "not cached yet" —
	// List() then derives it from the transcript, which is the only case
	// that still pays the read.
	title string
	// count mirrors the persisted message count; see state.MessageCount.
	count     int
	agentID   string
	decisions []storage.ApprovalRecord
}

// setLog installs the logger on the session and the two stores that live
// inside it. Construction-time only: nothing else holds the session yet.
// isAbsPath reports whether a cleaned name means "from the root".
//
// ★ filepath.IsAbs alone is not enough on Windows: IsAbs("\\abs.txt") is
//
//	false because a drive is missing, yet the name still reads as an
//	absolute path to anyone who wrote it. A file named that way must be
//	rejected on every platform rather than land in the workspace root.
func isAbsPath(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\")
}

func (fs *FileSession) setLog(log *slog.Logger) {
	fs.log = log
	if fs.ckpt != nil {
		fs.ckpt.log = log
	}
	if fs.ledger != nil {
		fs.ledger.log = log
	}
}

func (fs *FileSession) logger() *slog.Logger {
	if fs == nil || fs.log == nil {
		return discard
	}
	return fs.log
}

// newSession assembles a session object without touching the disk.
func newSession(root, id, legacyCkptRoot string) *FileSession {
	return &FileSession{
		root:   root,
		id:     id,
		ckpt:   newfileCheckpointStore(root, id, legacyCkptRoot),
		ledger: newRunLedger(root, id),
	}
}

// openSession opens a session: it reads state.json and nothing else.
func openSession(root, id, legacyCkptRoot string) (*FileSession, error) {
	fs := newSession(root, id, legacyCkptRoot)
	if err := fs.loadState(); err != nil {
		return nil, err
	}
	return fs, nil
}

// openFullSession opens a session **and** reads its transcript.
func openFullSession(root, id, legacyCkptRoot string) (*FileSession, error) {
	fs := newSession(root, id, legacyCkptRoot)
	if !fs.exists() {
		return nil, storage.ErrSessionNotFound
	}
	if err := fs.loadState(); err != nil {
		return nil, err
	}
	if err := fs.loadTranscript(); err != nil {
		return nil, err
	}
	if err := ensureDir(workspacePath(root, id)); err != nil {
		return nil, err
	}
	return fs, nil
}

// Init lays down a brand new session: its directory, its workspace, its
// transcript header and its first state.json.
//
// ★ It holds the lock for the whole call, because it writes the identity
//
//	fields (createdAt, agentID) and the message cache. It runs once, before
//	the session is published to anybody else, so holding the lock across the
//	file operations costs nothing — and it keeps the lock's scope honest:
//	every write to a guarded field happens under it.
func (fs *FileSession) Init(agentID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if err := ensureDir(sessionDir(fs.root, fs.id)); err != nil {
		return err
	}
	if err := ensureDir(workspacePath(fs.root, fs.id)); err != nil {
		return err
	}

	now := time.Now().UTC()
	if err := fs.initTranscript(now); err != nil {
		return err
	}
	st := state{
		CreatedAt: now,
		Title:     defaultTitle,
		AgentID:   agentID,
	}
	if err := fs.writeState(st); err != nil {
		return err
	}
	fs.createdAt = now
	fs.agentID = agentID
	fs.messages = make([]*schema.Message, 0)
	return nil
}

// Delete removes everything this session has on disk: the transcript, the
// state, the workspace and the checkpoints.
func (fs *FileSession) Delete() error {
	// ★ The id is read under the lock, and the removal happens outside it:
	//   removing a directory tree is not something to hold a lock for.
	id, _ := fs.identity()
	return os.RemoveAll(sessionDir(fs.root, id))
}

// exists reports whether this session is really on disk.
func (fs *FileSession) exists() bool {
	id, _ := fs.identity()
	return fileExists(messagesPath(fs.root, id))
}

// Meta is this session's metadata — what the sidebar shows.
func (fs *FileSession) Meta() storage.Meta {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	meta := storage.Meta{
		ID:           fs.id,
		Title:        fs.title,
		CreatedAt:    fs.createdAt,
		UpdatedAt:    fs.updatedAt,
		MessageCount: fs.count,
	}
	if meta.Title == "" {
		if err := fs.transcriptLocked(); err == nil {
			meta.Title = titleOf(fs.messages)
			meta.MessageCount = len(fs.messages)
		}
	}
	return meta
}

// HasPending reports whether this session still owes a human decision.
func (fs *FileSession) HasPending() bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.pending != nil && len(fs.pending.Interrupts) > 0
}

// HasCheckpoint asks this session's own checkpoint store whether there is a
// **resumable** checkpoint.
func (fs *FileSession) HasCheckpoint(ctx context.Context) (bool, error) {
	if fs.ckpt == nil {
		return false, nil
	}
	// ★ The id is read under the lock, but the lookup happens outside it:
	//   the lookup touches the disk, and the lock is not there to serialise
	//   I/O.
	id, _ := fs.identity()
	_, found, err := fs.ckpt.Get(ctx, id)
	return found, err
}

// ID returns the session's identifier.
//
// ★ It takes the lock: the id is **not** immutable — the transcript's header
//
//	is authoritative, and the first load may replace it — and the API
//	handlers read it while the run's own goroutine may still be loading
//	that transcript.
func (fs *FileSession) ID() string {
	id, _ := fs.identity()
	return id
}

// CreatedAt returns when the session was created.
//
// ★ It takes the lock for the same reason ID does: the transcript's header
//
//	carries the creation time too, and the first load replaces it.
func (fs *FileSession) CreatedAt() time.Time {
	_, createdAt := fs.identity()
	return createdAt
}

// identity returns the session's id and creation time under the lock.
//
// ★ The two are read together because they are written together: the
//
//	transcript's header carries both, and the first load replaces them.
func (fs *FileSession) identity() (string, time.Time) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.id, fs.createdAt
}

func (fs *FileSession) Append(msg *schema.Message) error {
	if msg == nil {
		return errors.New("cannot append a nil message")
	}
	if err := fs.writeMessage(msg); err != nil {
		return fmt.Errorf("append message: %w", err)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.messages = append(fs.messages, msg)
	fs.count++

	// Refresh the derived fields before serialising: stateForWrite reads
	// fs.count, so writing first would persist a stale count.
	needState := fs.title == "" && msg.Role == schema.User
	if needState {
		fs.title = titleOf([]*schema.Message{msg})
	}
	if needState || fs.count == 1 {
		// The caches are optional: a write failure loses them, not the
		// message, which is already durable in the append-only log.
		_ = fs.writeState(fs.stateForWrite())
	}
	return nil
}

// stateForWrite builds the state to persist, carrying the cached title
// forward. Every writeState call must go through it: a bare state{…}
// would silently drop the title on the next approval write.
//
// ★ The caller must hold fs.mu: it reads the cached fields.
func (fs *FileSession) stateForWrite() state {
	st := state{
		Title:        fs.title,
		MessageCount: fs.count,
		CreatedAt:    fs.createdAt,
		UpdatedAt:    fs.updatedAt,
		AgentID:      fs.agentID,
		Decisions:    append([]storage.ApprovalRecord(nil), fs.decisions...),
	}
	if fs.pending != nil {
		st.Pending = pendingToDisk(fs.pending)
	}
	return st
}

// GetCheckPointStore returns the checkpoint store of this session.
func (fs *FileSession) GetCheckPointStore() (adk.CheckPointStore, adk.CheckPointDeleter) {
	if fs.ckpt == nil {
		return nil, nil
	}
	return fs.ckpt, fs.ckpt
}

// RunLedger returns this session's run ledger.
//
// ★ The ledger is the session's own: its file lives in the session's
//
//	directory, next to the transcript. A run belongs to exactly one
//	session, so the ledger needs no session id of its own.
func (fs *FileSession) RunLedger() storage.RunLedger {
	if fs.ledger == nil {
		return nil
	}
	return fs.ledger
}

// Messages returns the session's messages.
//
// effective selects the view: false — the default — returns the whole
// transcript, which is the audit trail and what the UI renders. true returns
// the effective history: the last compaction message and everything after it,
// which is what the agent's context is built from.
func (fs *FileSession) Messages(effective bool) []*schema.Message {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	// A session opened without its transcript loads it on first use. The
	// failure path is loud: the transcript is unreadable, and an empty
	// conversation would look like lost history.
	_ = fs.transcriptLocked()
	out := make([]*schema.Message, len(fs.messages))
	copy(out, fs.messages)
	if effective {
		out = message.EffectiveView(out)
	}
	return out
}

// Len reports how many messages the session holds.
// The slice is authoritative here: fs.count exists only so state.json can
// answer List() without opening the transcript, and the two can legitimately
// disagree if a cached write was lost.
func (fs *FileSession) Len() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.messages)
}

func (fs *FileSession) SetPendingApproval(p *storage.PendingApproval) error {
	if p == nil || len(p.Interrupts) == 0 {
		return errors.New("pending approval needs at least one interrupt")
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	st := fs.stateForWrite()
	st.Pending = pendingToDisk(p)
	// Fail-closed: if the state cannot be persisted the approval must not be
	// admitted, so the in-memory value is left untouched too.
	if err := fs.writeState(st); err != nil {
		return fmt.Errorf("persist pending approval: %w", err)
	}

	fs.pending = clonePending(p)
	return nil
}

// PendingApproval peeks without consuming. The render path reads the approval
// to rebuild its chip; taking it there would destroy the user's decision.
func (fs *FileSession) PendingApproval() (*storage.PendingApproval, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.pending == nil {
		return nil, nil
	}
	return clonePending(fs.pending), nil
}

// TakePendingApproval atomically takes and clears the pending approval, doing
// the disk write and the memory update inside one critical section so no
// concurrent caller can observe the in-between state.
func (fs *FileSession) TakePendingApproval() (*storage.PendingApproval, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.pending == nil {
		return nil, nil
	}
	taken := clonePending(fs.pending)

	st := fs.stateForWrite()
	st.Pending = nil
	if err := fs.writeState(st); err != nil {
		return nil, fmt.Errorf("clear pending approval: %w", err)
	}
	fs.pending = nil
	return taken, nil
}

func (fs *FileSession) ClearPendingApproval() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	st := fs.stateForWrite()
	st.Pending = nil
	if err := fs.writeState(st); err != nil {
		return fmt.Errorf("clear pending approval: %w", err)
	}
	fs.pending = nil
	return nil
}

// RecordDecision appends approval decisions to the session's audit log.
func (fs *FileSession) RecordDecision(recs ...storage.ApprovalRecord) error {
	if len(recs) == 0 {
		return nil
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.decisions = append(fs.decisions, recs...)
	if len(fs.decisions) > maxAuditRecords {
		fs.decisions = append([]storage.ApprovalRecord(nil),
			fs.decisions[len(fs.decisions)-maxAuditRecords:]...)
	}
	if err := fs.writeState(fs.stateForWrite()); err != nil {
		return fmt.Errorf("persist approval decisions: %w", err)
	}
	return nil
}

// Decisions returns the recorded approval decisions, oldest first.
func (fs *FileSession) Decisions() []storage.ApprovalRecord {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]storage.ApprovalRecord(nil), fs.decisions...)
}

// Title returns the cached display title.
func (fs *FileSession) Title() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.title
}

func (fs *FileSession) AgentID() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.agentID
}

func (fs *FileSession) SetAgentID(id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	prev := fs.agentID
	fs.agentID = id
	if err := fs.writeState(fs.stateForWrite()); err != nil {
		fs.agentID = prev
		return fmt.Errorf("persist agent id: %w", err)
	}
	return nil
}

// SetTitle overrides the cached title.
func (fs *FileSession) SetTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("title must not be empty")
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	prev := fs.title
	fs.title = title
	if err := fs.writeState(fs.stateForWrite()); err != nil {
		// Rename is cosmetic; keep serving the old title rather than failing
		// a request over a cache entry.
		fs.title = prev
		return fmt.Errorf("persist title: %w", err)
	}
	return nil
}

// Workspace returns the path of this session's own workspace.
//
// ★ It takes the lock: the id is not immutable — the transcript's header is
//
//	authoritative, and the first load may replace it.
func (fs *FileSession) Workspace() string {
	id, _ := fs.identity()
	return workspacePath(fs.root, id)
}

func (fs *FileSession) PutFile(name string, r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("workspace upload needs a reader")
	}
	ws := fs.Workspace()
	if err := ensureDir(ws); err != nil {
		return "", err
	}
	dst, err := safeJoin(ws, name)
	if err != nil {
		return "", err
	}
	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return "", err
	}

	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return dst, nil
}

type pendingOnDisk struct {
	InterruptID string                      `json:"interrupt_id"`
	Detail      *contract.InterruptDetail   `json:"detail,omitempty"`
	Interrupts  []contract.PendingInterrupt `json:"interrupts,omitempty"`
}

func pendingToDisk(p *storage.PendingApproval) *pendingOnDisk {
	if p == nil {
		return nil
	}
	out := &pendingOnDisk{}
	for _, it := range p.Interrupts {
		out.Interrupts = append(out.Interrupts, it)
	}
	return out
}

func pendingFromDisk(p *pendingOnDisk) *storage.PendingApproval {
	if p == nil {
		return nil
	}
	if len(p.Interrupts) > 0 {
		return &storage.PendingApproval{Interrupts: append([]contract.PendingInterrupt(nil), p.Interrupts...)}
	}
	if p.InterruptID == "" {
		return nil
	}
	return &storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{
			InterruptID: p.InterruptID,
			Detail:      p.Detail,
		}},
	}
}

func clonePending(p *storage.PendingApproval) *storage.PendingApproval {
	if p == nil {
		return nil
	}
	out := &storage.PendingApproval{}
	if len(p.Interrupts) > 0 {
		out.Interrupts = make([]contract.PendingInterrupt, len(p.Interrupts))
		copy(out.Interrupts, p.Interrupts)
	}
	return out
}

// safeJoin keeps uploaded names inside the workspace directory.
func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || isAbsPath(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid workspace file name %q", name)
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("workspace file name %q escapes the workspace", name)
	}
	return full, nil
}

// state is the shape of state.json — the on-disk format belongs to the
// implementation, not to the contract.
type state struct {
	Version int            `json:"version"`
	Pending *pendingOnDisk `json:"pending,omitempty"`
	// Title is cached so List() need not re-read every session.jsonl to
	// derive it from the first user message. Optional: absent falls back to
	// the derived title.
	Title string `json:"title,omitempty"`
	// MessageCount is cached so List() can report it without parsing the
	// transcript. Optional: absent falls back to counting the log.
	MessageCount int `json:"message_count,omitempty"`
	// CreatedAt lets List() answer without opening the transcript.
	CreatedAt time.Time                `json:"created_at,omitempty"`
	UpdatedAt time.Time                `json:"updated_at"`
	Decisions []storage.ApprovalRecord `json:"decisions,omitempty"`
	AgentID   string                   `json:"agent_id,omitempty"`
}

// formatVersion guards the on-disk layout.
const formatVersion = 1

const (
	messagesFile = "session.jsonl"
	stateFile    = "state.json"
	workspaceDir = "workspace"
)

func sessionDir(root, id string) string { return filepath.Join(root, id) }
func messagesPath(root, id string) string {
	return filepath.Join(sessionDir(root, id), messagesFile)
}
func statePath(root, id string) string {
	return filepath.Join(sessionDir(root, id), stateFile)
}
func workspacePath(root, id string) string {
	return filepath.Join(sessionDir(root, id), workspaceDir)
}

// titleOf derives a display title from the first user message.
func titleOf(messages []*schema.Message) string {
	const maxRunes = 60
	for _, msg := range messages {
		text := message.UserText(msg)
		if text == "" {
			continue
		}
		if len([]rune(text)) > maxRunes {
			return string([]rune(text)[:maxRunes]) + "..."
		}
		return text
	}
	return defaultTitle
}

// loadState reads state.json into the cached fields.
//
// ★ It does not take the lock: it runs while the session is being opened,
//
//	before anybody else can reach it. The lazy path — the transcript load —
//	is the one that can race, and that one is locked.
func (fs *FileSession) loadState() error {
	st, _, err := readState(statePath(fs.root, fs.id))
	if err != nil {
		return err
	}
	fs.title = st.Title
	fs.count = st.MessageCount
	fs.createdAt = st.CreatedAt
	fs.updatedAt = st.UpdatedAt
	fs.agentID = st.AgentID
	fs.decisions = append([]storage.ApprovalRecord(nil), st.Decisions...)
	fs.pending = pendingFromDisk(st.Pending)
	return nil
}

// writeState persists this session's state.
//
// ★ The caller must hold fs.mu: it reads the id and writes updatedAt.
func (fs *FileSession) writeState(st state) error {
	st.Version = formatVersion
	st.UpdatedAt = time.Now().UTC()

	if err := writeStateFile(statePath(fs.root, fs.id), &st); err != nil {
		return err
	}
	fs.updatedAt = st.UpdatedAt
	return nil
}

// writeStateFile persists state.json through tmp -> fsync -> rename. The rename
// is atomic on POSIX, so a reader never observes a half-written state.
func writeStateFile(path string, st *state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readState loads state.json. A missing file is a valid fresh state.
func readState(path string) (state, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state{Version: formatVersion}, false, nil
		}
		return state{}, false, err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if st.Version != formatVersion {
		return state{}, false, fmt.Errorf("%s has version %d; expected %d", path, st.Version, formatVersion)
	}
	return st, true, nil
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

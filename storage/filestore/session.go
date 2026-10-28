// Package filestore 是 storage.Session / storage.SessionManager 的文件实现。
//
// ★ 契约归 storage（叶子包），本包**不声明任何接口** —— 它只负责落盘。
//
//	五个文件各管一段：
//
//	  - session.go    （本文件）FileSession + 磁盘形态：它拥有这个会话在盘上的
//	                             全部东西（transcript / state.json / workspace /
//	                             自己的 checkpoint 存取器）
//	  - manager.go    FileManager：会话仓库 —— 只认「id 集合」与「会话」
//	  - checkpoint.go checkpoint 的字节级存取（住在会话自己的 checkpoints/ 下）
//	  - transcript.go session.jsonl 的格式与编解码
//	  - options.go    FileManager 的选项与两个最小接口
//
//	★ 三层依赖是单向的：FileManager → FileSession → fileCheckpointStore。
//	仓库不碰任何文件 —— 它要什么（状态、待批、checkpoint）都问会话。
package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

var _ storage.Session = (*FileSession)(nil)

// defaultTitle 是新会话的初始标题。
//
// ★ 内存与落盘必须是同一个值：stateForWrite 读的是内存里的 title，
//
//	两边不一致时下一次写状态会把磁盘上的标题抹掉。
const defaultTitle = "New Session"

// maxAuditRecords 是审计记录保留的上限。
//
// ★ 审计不是日志：全量留着会让 state.json 无限增长，而它每次会话都要
//
//	整体读写一遍。留最近这些条，「最近批过什么」就答得上来。
const maxAuditRecords = 50

// FileSession is one conversation on disk.
//
// ★ 它拥有这个会话在盘上的**全部**东西：transcript、state.json、workspace，
//
//	以及**它自己的** checkpoint 存取器（住在 <会话目录>/checkpoints/ 下）。
//	所以「删会话」就是删一个目录 —— 不用在两个地方各删一次。
//
// messages are backed by filestore.jsonl and appended in O_APPEND; pending lives
// in state.json, which is rewritten atomically. Memory is only a cache of both.
type FileSession struct {
	id        string
	createdAt time.Time
	updatedAt time.Time
	root      string

	// ckpt 是这个会话**自己的** checkpoint 存取器。
	//
	// ★ 它由会话创建（住在自己的 checkpoints/ 子目录里），所以「删会话」
	//   = 删掉它，而删除路径不必再单独找。
	ckpt *fileCheckpointStore

	mu       sync.Mutex
	messages []*schema.Message
	// loaded 记录 transcript 读过了没有 —— 见 [FileSession.transcriptLocked]。
	loaded bool
	// pending 是这轮还欠着的审批（内存形态）。
	pending *storage.PendingApproval
	// title is the cached display title. Empty means "not cached yet" —
	// List() then derives it from the transcript, which is the only case
	// that still pays the read.
	title string
	// count mirrors the persisted message count; see state.MessageCount.
	count int
	// agentID 是这个会话的入口 agent（空 = main）。它只影响之后的轮次，
	// 不改写历史；见 storage.Session.SetAgentID.
	agentID string
	// decisions 是审批决定的审计记录（磁盘上 state.json 的同一份）。
	//
	// ★ 必须在内存里留一份：stateForWrite 是**重建**整个状态的，
	//
	//	不带它的话每次写状态（追加消息、清待批…）都会把审计抹掉。
	decisions []storage.ApprovalRecord
}

// newSession assembles a session object without touching the disk.
//
// ★ 每个会话**自带**它的 checkpoint 存取器（住在自己的 checkpoints/ 子目录里），
//
//	所以只需要会话根。
func newSession(root, id, legacyCkptRoot string) *FileSession {
	return &FileSession{
		root: root,
		id:   id,
		ckpt: newfileCheckpointStore(root, id, legacyCkptRoot),
	}
}

// openSession opens a session: it reads state.json and nothing else.
//
// ★ transcript **不在这里读**：它可能很大（长会话几百条消息），而 List 与
//
//	Inspect 每轮都要问会话要状态 —— 那些路径不需要它。需要 transcript 的
//	地方用 [openFullSession]。
//
// ★ 它**不建任何目录**：读取路径上不该有副作用（List 每轮都跑）。
func openSession(root, id, legacyCkptRoot string) (*FileSession, error) {
	fs := newSession(root, id, legacyCkptRoot)
	if err := fs.loadState(); err != nil {
		return nil, err
	}
	return fs, nil
}

// openFullSession opens a session **and** reads its transcript.
//
// ★ 存在性检查在**这里**做 —— 仓库不碰文件（那是这一层的职责）：仓库拿到的
//
//	是 storage.ErrSessionNotFound，它自己不再判断一遍。
//
// ★ transcript 在这里**当场**读：损坏必须是**当场**的失败，而不是事后一个
//
//	空列表（那看起来像历史丢了）。
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
	// 老会话可能没有 workspace 目录 —— 建出来，免得上传时才发现。
	if err := ensureDir(workspacePath(root, id)); err != nil {
		return nil, err
	}
	return fs, nil
}

// Init lays down a brand new session: its directory, its workspace, its
// transcript header and its first state.json.
//
// ★ 磁盘上的标题写占位值，而内存里留空：空 = 「还没缓存」，第一条用户消息
//
//	到达时才从正文推导（见 Append）。
//
// ★ 它是 [storage.SessionManager.Create] 用的：直接调它，**仓库不会缓存这个
//
//	会话**（它不在 fm.cache 里）—— 仓库的生命周期只有仓库管。
func (fs *FileSession) Init(agentID string) error {
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
//
// ★ checkpoint 住在会话目录里，所以删目录就是删全 —— 不必再单独删它。
//
// ★ 直接调它**不做运行期清理**（不停轮次、不清运行时的引用）—— 那是
//
//	[storage.SessionManager.Delete] 的事：它先 teardown，再删文件。
func (fs *FileSession) Delete() error {
	return os.RemoveAll(sessionDir(fs.root, fs.id))
}

// exists reports whether this session is really on disk.
//
// ★ 看的是 transcript：state.json 可能还没写（会话刚建就崩了），而没有
//
//	transcript 的目录不是会话。
func (fs *FileSession) exists() bool {
	return fileExists(messagesPath(fs.root, fs.id))
}

// Meta is this session's metadata — what the sidebar shows.
//
// ★ 标题的推导只在这里发生（缓存里没有就从正文取一次）—— 别处再写一遍
//
//	就会有两套「这个会话叫什么」的答案。
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
		// 标题没缓存过（老会话、或那次写状态失败）：从正文推导一次。
		//
		// ★ 这需要 transcript —— 于是这里会**读一次**。只影响那少数会话。
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
//
// ★ 它读文件并解码 —— 「文件在」不等于「能用」：损坏的 checkpoint 会被隔离，
//
//	那时候这一轮同样恢复不了。问「能不能恢复」的地方（编排层的恢复路径、
//	启动巡检）都用这一处，免得两处各答一遍。
//
// ★ 有待批但**没有**可恢复的 checkpoint = 孤儿审批：那张卡还在界面上，
//
//	但答复会被拒绝。
func (fs *FileSession) HasCheckpoint(ctx context.Context) (bool, error) {
	if fs.ckpt == nil {
		return false, nil
	}
	_, found, err := fs.ckpt.Get(ctx, fs.id)
	return found, err
}

func (fs *FileSession) ID() string           { return fs.id }
func (fs *FileSession) CreatedAt() time.Time { return fs.createdAt }

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
// ★ 它带上**待批**：一次与待批无关的写状态（追加消息、改标题）不该把
//
//	用户还欠着的那张卡抹掉。待批的两个方法会显式改写这一项。
func (fs *FileSession) stateForWrite() state {
	st := state{
		Title:        fs.title,
		MessageCount: fs.count,
		CreatedAt:    fs.createdAt,
		UpdatedAt:    fs.updatedAt,
		AgentID:      fs.agentID,
		// ★ 审计要跟着每次写状态一起落盘：stateForWrite 是**重建**状态，
		//	不带它的话追加一条消息就把审批记录抹干净了。
		Decisions: append([]storage.ApprovalRecord(nil), fs.decisions...),
	}
	if fs.pending != nil {
		st.Pending = pendingToDisk(fs.pending)
	}
	return st
}

// GetCheckPointStore returns the checkpoint store of this session.
//
// ★ 两个返回值都是 nil 表示「这个部署不做 checkpoint」—— 那时审批中断
//
//	无法恢复（见 agentkit/run.hasCheckpoint）。
func (fs *FileSession) GetCheckPointStore() (adk.CheckPointStore, adk.CheckPointDeleter) {
	if fs.ckpt == nil {
		return nil, nil
	}
	return fs.ckpt, fs.ckpt
}

func (fs *FileSession) Messages() []*schema.Message {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	// A session opened without its transcript loads it on first use. The
	// failure path is loud: the transcript is unreadable, and an empty
	// conversation would look like lost history.
	_ = fs.transcriptLocked()
	out := make([]*schema.Message, len(fs.messages))
	copy(out, fs.messages)
	return out
}

// Len reports how many messages the session holds.
//
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

	// ★ 显式清掉待批：stateForWrite 现在**带着**待批（一次与待批无关的
	//	写状态不该把用户还欠着的卡抹掉），所以「取走」要自己说清楚。
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
//
// ★ 截断的是**最旧的**，不是改写已有的那几条 —— 审计只追加。
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

// AgentID 返回这个会话的入口 agent；空串 = main。
func (fs *FileSession) AgentID() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.agentID
}

// SetAgentID 把会话的入口 agent 落盘。
//
// ★ 只影响之后的轮次：历史消息不改写，已经跑完的 turn 也不会换归属。
//
//	「运行中不能切」「待审批不能切」由 API 判定 —— 只有它看得见运行时相位，
//	session 只负责持久化。
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

func (fs *FileSession) Workspace() string { return workspacePath(fs.root, fs.id) }

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

// ── FileSession 的内部实现 ───────────────────────────────────────────────
//
//	下面这几个只有 FileSession 用：待批在磁盘形态与内存形态之间的转换、
//	以及 workspace 上传的路径防护。

// pendingOnDisk 是 state.json 里持久化的待批集合。
//
// ★ 旧的**单数**形态（interrupt_id + detail）是磁盘格式的遗留，不在契约里 ——
//
//	契约只说「一组待批」。但读的时候**必须认**：磁盘上已有的会话不该因为
//	升级而读不出来 —— 那会让用户再也答不了那一问。
type pendingOnDisk struct {
	// InterruptID 是旧的单数字段。新的写入不再用它。
	InterruptID string `json:"interrupt_id"`
	// Detail 与 InterruptID 配对使用。
	Detail *contract.InterruptDetail `json:"detail,omitempty"`
	// Interrupts 是完整的待批集合（2026-10-21 起写入）。
	Interrupts []contract.PendingInterrupt `json:"interrupts,omitempty"`
}

// pendingToDisk 把内存形态写成磁盘形态。
//
// ★ 写出去的一直是集合形态：旧字段只读不写。
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

// pendingFromDisk 把磁盘形态读成内存形态。
//
// ★ 兼容旧的单数格式：Interrupts 为空而 InterruptID 非空 ⇒ 那是 2026-10-21
//
//	之前写的单待批，按一个元素的集合读进来。
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

// clonePending 深拷贝一份待批。
//
// ★ 切片必须拷贝：`Interrupts` 是 slice，浅拷贝会让读侧和写侧共用同一个
//
//	底层数组 —— 读侧改一下就把待批改了，而它本该是只读的快照。
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
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid workspace file name %q", name)
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("workspace file name %q escapes the workspace", name)
	}
	return full, nil
}

// ── 磁盘形态 ─────────────────────────────────────────────────────────────
//
//	下面这一段是「磁盘上长什么样」与「怎么读写」：state.json 的形态、目录
//	布局、以及几处共用的原语。

// state is the shape of state.json — the on-disk format belongs to the
// implementation, not to the contract.
//
// ★ 它没有出过一次契约：Session 接口只给意图化的方法（SetTitle / SetAgentID /
//
//	RecordDecision / SetPendingApproval），不给「整份 state 的读写」——
//	那种 raw 接口会绕过 stateForWrite 的不变量（见那里的说明）。
//
//	字段与旧版逐字一致（含 legacy 单数待批那两个字段），老会话照读。
type state struct {
	Version int `json:"version"`
	// Pending 是这轮还欠着的审批（可能是一组，见 storage.PendingApproval）。
	Pending *pendingOnDisk `json:"pending,omitempty"`
	// Title is cached so List() need not re-read every session.jsonl to
	// derive it from the first user message. Optional: absent falls back to
	// the derived title.
	Title string `json:"title,omitempty"`
	// MessageCount is cached so List() can report it without parsing the
	// transcript. Optional: absent falls back to counting the log.
	MessageCount int `json:"message_count,omitempty"`
	// CreatedAt lets List() answer without opening the transcript.
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Decisions 是审批决定的审计记录（只追加，最旧的在前）。
	//
	// ★ 有上限（见 [maxAuditRecords]）：审计不是日志 —— 留最近若干条就够，
	//
	//	而无上限的审计会让 state.json 无限增长，而它每次会话都要整体
	//	读写一遍。
	Decisions []storage.ApprovalRecord `json:"decisions,omitempty"`
	// AgentID 是这个会话的入口 agent。空值 = main（兼容加这个字段之前的会话）。
	//
	// ★ 名字而不是指针：空串本身就是「没切过」的合法表示，再用一个
	//   *string 只会让读侧多一层 nil 判断。
	AgentID string `json:"agent_id,omitempty"`
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

// loadState reads state.json into the session's memory.
//
// ★ 旧的单数待批在这里归一成集合 —— 读侧归一，写侧一直写集合。
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
// ★ 调用方持有 fs.mu —— 它改的是会话的状态，不是一个自由函数。
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

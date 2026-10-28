package filestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"openheron/agentkit/obs"
	"openheron/storage"
)

var _ storage.SessionManager = (*FileManager)(nil)

// FileManager owns the session root and caches opened sessions.
//
// ★ 它是**仓库**：只认「id 集合」与「会话对象」，不碰任何文件 ——
//
//	要状态、要待批、要 checkpoint，都问会话（FileSession）。这个包的文件
//	读写全在会话层（session.go）与 checkpoint 存取器（checkpoint.go）里。
type FileManager struct {
	root string
	opts Options

	// legacyCkptRoot 是**老版**的 checkpoint 目录（<dataDir>/checkpoints）——
	// 只读回退用，可以为空。
	legacyCkptRoot string

	mu    sync.Mutex
	cache map[string]*FileSession
}

// NewFileManager prepares the session root. opts may be incomplete; Teardown
// is filled in after the Runtime exists, since the Runtime's own construction
// depends on this manager.
func NewFileManager(root string, opts Options) (*FileManager, error) {
	if root == "" {
		return nil, errors.New("session root must not be empty")
	}
	if err := ensureDir(root); err != nil {
		return nil, fmt.Errorf("create session root %s: %w", root, err)
	}
	return &FileManager{
		root:           root,
		opts:           opts,
		legacyCkptRoot: opts.CheckpointLegacyRoot,
		cache:          make(map[string]*FileSession),
	}, nil
}

// sessionOf assembles a session object without touching the disk.
func (fm *FileManager) sessionOf(id string) *FileSession {
	return newSession(fm.root, id, fm.legacyCkptRoot)
}

// open opens a session: it reads state.json and nothing else.
//
// ★ List 与 Inspect 用它 —— 那两条路径每轮都跑，不需要 transcript。
func (fm *FileManager) open(id string) (*FileSession, error) {
	return openSession(fm.root, id, fm.legacyCkptRoot)
}

// openFull opens a session and reads its transcript.
//
// ★ Get 用它。存在性检查在**会话层**做 —— 仓库不再自己判断一遍。
func (fm *FileManager) openFull(id string) (*FileSession, error) {
	return openFullSession(fm.root, id, fm.legacyCkptRoot)
}

// sessionIDs lists the ids of the session directories.
//
// ★ 读不出来就是**错** —— 目录没了、权限不对、路径根本不是目录，三种都
//
//	得让调用方看见，而不是当成「一个会话都没有」。
func (fm *FileManager) sessionIDs() ([]string, error) {
	entries, err := os.ReadDir(fm.root)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if validateID(e.Name()) != nil {
			continue
		}
		ids = append(ids, e.Name())
	}
	return ids, nil
}

// Create mints a new session and records its root agent.
//
// ★ agentID 写进**同一次** state 写入里：会话从诞生那一刻起就有根，
//
//	读它的人（侧栏、运行、归属）不需要自己回退，也就没有「两份答案」。
func (fm *FileManager) Create(ctx context.Context, agentID string) (storage.Session, error) {
	id := newID()
	if err := validateID(id); err != nil {
		return nil, err
	}

	// ★ 会话落地自己：建目录、建 workspace、写第一份 state.json。
	fs := fm.sessionOf(id)
	if err := fs.Init(agentID); err != nil {
		return nil, err
	}

	fm.mu.Lock()
	fm.cache[id] = fs
	fm.mu.Unlock()
	return fs, nil
}

// Get returns an existing filestore. Unlike the baseline's GetOrCreate it never
// creates on demand, so an arbitrary id in the URL yields 404 rather than a
// silently created filestore.
func (fm *FileManager) Get(_ context.Context, id string) (storage.Session, error) {
	if err := validateID(id); err != nil {
		return nil, fmt.Errorf("%w: %v", storage.ErrSessionNotFound, err)
	}

	fm.mu.Lock()
	fs, ok := fm.cache[id]
	fm.mu.Unlock()
	if ok {
		return fs, nil
	}

	var err error
	fs, err = fm.openFull(id)
	if err != nil {
		if errors.Is(err, storage.ErrSessionNotFound) {
			return nil, fmt.Errorf("%w: %s", storage.ErrSessionNotFound, id)
		}
		return nil, fmt.Errorf("open session %s: %w", id, err)
	}

	fm.mu.Lock()
	fm.cache[id] = fs
	fm.mu.Unlock()
	return fs, nil
}

func (fm *FileManager) List(_ context.Context) ([]storage.Meta, error) {
	ids, err := fm.sessionIDs()
	if err != nil {
		return nil, err
	}
	metas := make([]storage.Meta, 0, len(ids))
	for _, id := range ids {
		fs, err := fm.open(id)
		if err != nil {
			obs.Logger().Warn("[session] skipping unreadable session", "session", id, "err", err)
			continue
		}
		// ★ 问会话要元数据 —— 仓库自己不读 state.json，也不碰 transcript。
		metas = append(metas, fs.Meta())
	}
	// Newest activity first: the sidebar is a recency list, not a directory dump.
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].UpdatedAt.Equal(metas[j].UpdatedAt) {
			return metas[i].ID < metas[j].ID
		}
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

// Delete runs the ordered teardown: stop the loop, drop the checkpoint, remove
// the session directory, evict the cache.
func (fm *FileManager) Delete(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}

	if fm.opts.Teardown != nil {
		fm.opts.Teardown.Cleanup(ctx, id)
	}
	// ★ 会话删自己 —— 目录里含消息、状态、workspace 与 checkpoint，一次删净。
	if err := fm.sessionOf(id).Delete(); err != nil {
		return fmt.Errorf("remove session %s: %w", id, err)
	}

	fm.mu.Lock()
	delete(fm.cache, id)
	fm.mu.Unlock()
	return nil
}

// SetTeardown injects the runtime teardown hook after construction.
func (fm *FileManager) SetTeardown(t Teardown) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.opts.Teardown = t
}

// Inspect reports how the session state and the checkpoint state line up. It
// runs once at startup, never on a hot path.
//
// ★ 它**只报告，不改数据**。
//
//	The two stores are independent files, so a crash between them leaves them
//
// disagreeing. Each combination has a defined reading:
//
//	pending + checkpoint   healthy, approval may proceed
//	pending, no checkpoint orphan; the approval is **kept** so the card stays
//	                         visible, but the round can no longer be resumed
//	checkpoint, no pending unrecoverable: the interrupt id lives inside a gob
//	                         payload we cannot decode, so warn and ask the
//	                         user to re-ask
//	neither                clean
func (fm *FileManager) Inspect(ctx context.Context) (storage.Report, error) {
	var report storage.Report

	ids, err := fm.sessionIDs()
	if err != nil {
		return report, err
	}
	for _, id := range ids {
		fs, err := fm.open(id)
		if err != nil {
			// A corrupt state.json must not be silently dropped; count it and
			// leave the file alone for forensics.
			obs.Logger().Warn("[session] unreadable state, skipping", "session", id, "err", err)
			report.SkippedBadJSON++
			continue
		}

		hasPending := fs.HasPending()
		hasCheckpoint := false
		if ckpt, err := fs.HasCheckpoint(ctx); err != nil {
			obs.Logger().Warn("[session] checkpoint probe failed, assuming none", "session", id, "err", err)
		} else {
			hasCheckpoint = ckpt
		}

		switch {
		case hasPending && hasCheckpoint:
			report.PendingOK++

		case hasPending && !hasCheckpoint:
			// ★ 待批**留着**：界面要继续显示那张卡，用户才有得答。
			//
			//	早先这里把孤儿待批**清掉**，于是重启一次，用户的审批就凭空
			//	消失 —— 而他什么都不知道。清掉并没有让状态变好：checkpoint
			//	不在时 Resume 会干脆地拒绝（run.ErrNoCheckpoint），而待批
			//	记录留着也不会坏 —— 它还带着工具名与参数，是那张卡的来源。
			obs.Logger().Warn("[session] pending approval has no checkpoint; keeping it so the user can still answer",
				"session", id)
			report.Orphan++

		case !hasPending && hasCheckpoint:
			obs.Logger().Warn("[session] has a checkpoint but no pending approval; "+
				"the interrupt id cannot be recovered, please re-ask", "session", id)
			report.Ownerless++

		default:
			report.Clean++
		}
	}

	return report, nil
}

// ── FileManager 的内部实现 ───────────────────────────────────────────────
//
//	下面这几个只有 FileManager 用：会话 id 的校验与生成。

// validateID rejects anything that is not a canonical UUID. The id reaches us
// straight from the HTTP path and lands in filenames, so this layer validates
// centrally rather than trusting a router helper to sanitise the path.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("session id is empty")
	}
	if len(id) > 64 {
		return fmt.Errorf("session id is too long")
	}
	if strings.ContainsAny(id, `/\`) || id != filepath.Base(id) || strings.Contains(id, "..") {
		return fmt.Errorf("session id %q contains path separators", id)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("session id is not a uuid: %w", err)
	}
	if parsed.String() != id {
		return fmt.Errorf("session id is not in canonical form")
	}
	return nil
}

func newID() string { return uuid.NewString() }

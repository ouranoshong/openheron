package filestore

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cloudwego/eino/adk"

	"openheron/agentkit/obs"
	"openheron/persist"
)

const (
	// ckptDir 是会话目录里放 checkpoint 的子目录。
	ckptDir = "checkpoints"
	// ckptExt 是 checkpoint 文件的后缀。
	ckptExt = ".ckpt"
	// ckptTmpExt 是原子写的临时文件后缀。
	ckptTmpExt = ".ckpt.tmp"
	// ckptBadExt 是损坏隔离产物的后缀。
	ckptBadExt = ".corrupt"
	// ckptMagic / ckptVersion 是文件头。
	ckptMagic   = "CKPT"
	ckptVersion = 1
	// ckptHdrSize = magic | version | payloadLen
	ckptHdrSize = len(ckptMagic) + 1 + 8
)

var (
	errBadCheckPoint   = errors.New("malformed checkpoint")
	errEmptyCheckPoint = errors.New("empty checkpoint")
)

var (
	_ adk.CheckPointStore   = (*fileCheckpointStore)(nil)
	_ adk.CheckPointDeleter = (*fileCheckpointStore)(nil)
)

// fileCheckpointStore 是**一个会话**的 checkpoint 存取器。
//
// ★ 它住在会话自己的目录里：
//
//	<sessions>/<sessionID>/checkpoints/<checkpointID>.ckpt
//
//	文件名是 **checkpoint id**（eino 的 key），不是会话 id —— 两者在框架里
//	通常是同一个（cfg.CheckpointID = 会话 id），但语义上是两回事。
//
//	★ 于是「删会话目录 = 删全」：消息、状态、workspace、checkpoint 一起没，
//	  删除路径不必再单独找。
//
// ★ 老布局（<dataDir>/checkpoints/<id>.ckpt）**只读回退**：不试的话，老会话
//
//	里还等着人审批的那一轮就再也恢复不了了 —— 而 checkpoint 是临时状态，
//	丢了只意味着「重新提问」，不值得为此让用户卡住。
//
// 一个存取器只服务一个会话：它写自己会话的 checkpoints/ 目录。所有文件 IO
// 由一把互斥锁串行化 —— checkpoint 只在中断或停止时写，每轮读一次，量太小，
// 不值得分片加锁。
type fileCheckpointStore struct {
	// root 是会话根（sessions/）。
	root string
	// id 是这个会话的 id。
	id string
	// legacyRoot 是**老版**的 checkpoint 目录（<dataDir>/checkpoints）——
	// 只读回退用，可以为空。
	legacyRoot string

	mu sync.Mutex
}

// newfileCheckpointStore prepares the checkpoint store of one session.
//
// sessionsRoot is the root all sessions live under; the store keeps its own
// session's id so its directory is <sessionsRoot>/<id>/checkpoints.
//
// legacyRoot may be empty: it is only consulted as a read-only fallback for
// checkpoints written by the old layout.
func newfileCheckpointStore(sessionsRoot, sessionID, legacyRoot string) *fileCheckpointStore {
	return &fileCheckpointStore{root: sessionsRoot, id: sessionID, legacyRoot: legacyRoot}
}

// setID repoints the store at another session id.
//
// ★ 它只在一种情况下发生：transcript 的 header 里的 id 与目录名不一致
//
//	（有人手动挪过会话目录）。那时 header 是身份证 —— 会话跟着它走，
//	checkpoint 也得跟着，否则两半会分家。
func (s *fileCheckpointStore) setID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = id
}

// dir is where this session's checkpoints live.
func (s *fileCheckpointStore) dir() string {
	return filepath.Join(s.root, s.id, ckptDir)
}

// Get returns the stored checkpoint. A missing or quarantined checkpoint is
// reported as absent rather than as an error: a decode failure inside the
// framework permanently bricks the session, since every later Submit builds a
// fresh loop against the same CheckpointID and dies in the same place.
func (s *fileCheckpointStore) Get(_ context.Context, checkPointID string) ([]byte, bool, error) {
	path, err := s.resolve(checkPointID)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, found, err := s.readFile(path)
	if err != nil {
		return nil, false, err
	}
	if !found && s.legacyRoot != "" {
		// 老路径：那一轮还等着人审批，试着把它捞回来。
		legacy := s.legacyPath(checkPointID)
		data, found, err = s.readFile(legacy)
		if err != nil {
			return nil, false, err
		}
		if found {
			obs.Logger().Info("[checkpoint] read a checkpoint from the legacy directory",
				"session", checkPointID, "path", legacy)
		}
	}
	if !found {
		return nil, false, nil
	}
	return data, true, nil
}

// Set writes a checkpoint atomically. An error here does not abort the turn —
// the framework only records it in ExitState.CheckpointErr — but it does mean
// no checkpoint survives, which the startup inspection later reports as an
// orphaned approval.
func (s *fileCheckpointStore) Set(_ context.Context, checkPointID string, checkPoint []byte) error {
	path, err := s.resolve(checkPointID)
	if err != nil {
		return err
	}
	blob := encodeCheckPoint(checkPoint)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := writeAtomic(path, blob); err != nil {
		return fmt.Errorf("write checkpoint %s: %w", checkPointID, err)
	}
	return nil
}

// Delete removes a checkpoint. It must be idempotent: the framework calls it
// repeatedly and discards the result.
func (s *fileCheckpointStore) Delete(_ context.Context, checkPointID string) error {
	path, err := s.resolve(checkPointID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete checkpoint %s: %w", checkPointID, err)
	}
	// 老路径也清一份：那个会话目录被删了，老文件不该留在那儿。
	if s.legacyRoot != "" {
		legacy := s.legacyPath(checkPointID)
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete legacy checkpoint %s: %w", checkPointID, err)
		}
	}
	return nil
}

// readFile reads one checkpoint file, quarantining it when the bytes are
// damaged. found is false when the file simply is not there.
func (s *fileCheckpointStore) readFile(path string) (data []byte, found bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		s.quarantine(path, err)
		return nil, false, nil
	}

	payload, err := decodeCheckPoint(raw)
	if err != nil {
		// An empty file is a clean "no checkpoint" as far as the framework is
		// concerned; quarantining it would destroy a benign state.
		if errors.Is(err, errEmptyCheckPoint) {
			return []byte{}, true, nil
		}
		s.quarantine(path, err)
		return nil, false, nil
	}
	return payload, true, nil
}

// resolve validates the id and builds the on-disk path.
//
// ★ checkPointID 由客户端提供却直落文件名（父文档 §5.6），故本层必须自校验 ——
//
//	纵深防御不是冗余。
func (s *fileCheckpointStore) resolve(checkPointID string) (string, error) {
	if err := validateID(checkPointID); err != nil {
		return "", fmt.Errorf("invalid checkpoint id: %w", err)
	}
	return filepath.Join(s.dir(), checkPointID+ckptExt), nil
}

// legacyPath is where the old layout kept the checkpoints: one flat file per
// session, in a directory of its own.
func (s *fileCheckpointStore) legacyPath(checkPointID string) string {
	return filepath.Join(s.legacyRoot, checkPointID+ckptExt)
}

func (s *fileCheckpointStore) quarantine(path string, cause error) {
	bad := path + ckptBadExt
	if err := os.Rename(path, bad); err != nil {
		obs.Logger().Error("[checkpoint] session is corrupt and quarantine failed",
			"cause", cause, "err", err, "path", path)
		return
	}
	obs.Logger().Warn("[checkpoint] session is corrupt; quarantined",
		"cause", cause, "path", bad)
}

// encodeCheckPoint prefixes the payload with a fixed header so that corruption
// becomes detectable. Without one, openheron cannot tell a valid gob blob from a
// damaged one, and the quarantine path would be unreachable.
func encodeCheckPoint(payload []byte) []byte {
	out := make([]byte, ckptHdrSize+len(payload))
	copy(out, ckptMagic)
	out[4] = ckptVersion
	binary.BigEndian.PutUint64(out[5:13], uint64(len(payload)))
	copy(out[ckptHdrSize:], payload)
	return out
}

func decodeCheckPoint(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errEmptyCheckPoint
	}
	if len(data) < ckptHdrSize {
		return nil, fmt.Errorf("%w: truncated header (%d bytes)", errBadCheckPoint, len(data))
	}
	if string(data[:4]) != ckptMagic {
		return nil, fmt.Errorf("%w: bad magic %q", errBadCheckPoint, data[:4])
	}
	if data[4] != ckptVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", errBadCheckPoint, data[4])
	}
	n := binary.BigEndian.Uint64(data[5:13])
	payload := data[ckptHdrSize:]
	if uint64(len(payload)) != n {
		return nil, fmt.Errorf("%w: length mismatch, header says %d, have %d", errBadCheckPoint, n, len(payload))
	}
	return payload, nil
}

// writeAtomic is tmp -> fsync -> rename. POSIX rename is atomic, so the target
// is never observed torn.
//
// ★ 实现搬到了 [persist] 包：配置写入要同一套语义，两份实现会漂 ——
//
//	某天有人只改一处（比如忘了 fsync），差异只在断电时显形且极难复现。
//	那一层同时补上了目录 fsync，原先这里只刷文件。
//
//	原语住在 [persist] 而不是本包，是为了让 config 与 kb 不必依赖整个
//	运行时包 —— 详见 persist/atomicfile.go 的包注释。
func writeAtomic(dst string, data []byte) error {
	return persist.AtomicWrite(dst, data, ckptTmpExt)
}

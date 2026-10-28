package persist

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ★ 本包是**文件持久化原语**的唯一产地。与 [atomicfile.go] 同包，因为它们
//
//	回答的是同一个问题：「多个进程同时碰同一个文件时，怎么不把内容写坏」。
//	一个是**写**的保证（读者永远看不到半截内容），一个是**互斥**的保证
//	（两个写者不互相覆盖）。
//
// ★ 本包是**叶子**：不 import 任何 openheron 包，也不含任何运行时概念。
//
//	理由见 atomicfile.go 的包注释 —— 原语住在运行时的 agentkit/run 里时，
//	config 与 kb 都被迫依赖整个运行时包，反向依赖会从传递路径上
//	悄悄破坏依赖铁律。

// pollInterval 是抢锁失败后重试的间隔。
//
// ★ 不用 `flock(fd, LOCK_EX)` 的阻塞形态：那个 syscall **没法被 ctx 打断**，
//
//	于是「Ctrl-C 取消一次排队」会变成「进程卡在那里直到对方跑完」。而对方
//	可能正在跑一次十分钟的索引。非阻塞 + 轮询换来的是可取消，代价是每次
//	多一个 syscall —— 而排队场景本来就不常见。
const pollInterval = 200 * time.Millisecond

// FileLock 是一个**跨进程**的排他锁。
//
// ★ 为什么需要它：`sync.Mutex` 只在**本进程内**排他。而 openheron 的索引有两个
//
//	进程会写 —— 服务进程里的 `kb_index` 工具，与命令行 `openheron kb index`。
//	两者各自「读全部 → 改 → 写全部」，交错起来就是**静默丢更新**：
//	后写的那个把先写的整批覆盖掉，两边都报告成功。同样的道理也适用于
//	`index.gob` 与 `manifest.json` 这对文件 —— 两个进程各写一半，落下来的是
//	一个「维度说 A、向量是 B」的索引，而它加载时一切正常。
//
// ★ 用 `flock(2)` 而不是「建一个 O_EXCL 的锁文件」：前者在进程退出（包括被
//
//	kill -9）时由内核自动释放，后者在崩溃后会**永久**留下一个谁也不敢删的
//	文件。一次十分钟的索引崩掉就足以让索引功能彻底不可用，而那比丢一次更新
//	更难收拾。
//
// ★ 只读侧（[Lock.Try]）不阻塞：查询撞上正在跑的索引时应当立刻返回旧快照，
//
//	而不是排队等十分钟。
//
// ★ 平台：加锁的 syscall 按平台分文件（[persist/filelock_unix.go] 的 flock、
//
//	[persist/filelock_windows.go] 的 LockFileEx），**上层逻辑只有这一份**。
//	平台差异被收窄到两个函数，而不是让并发语义整条分叉 ——
//	「一次索引只跑在一个进程里」这条保证在两个平台上必须含义相同，
//	否则那比「编译不过」危险得多：它会安静地丢数据。
type FileLock struct {
	path string
	f    *os.File

	// depth 让同一进程内的重复获取变成计数器而不是第二次 syscall。
	//
	// ★ 必须这么做，理由有两条：
	//   1. `flock` 是**按打开文件描述**计锁的。同一个 fd 上再调一次
	//      `LOCK_EX` 会成功（不阻塞），但内层一旦 `LOCK_UN`，外层的锁就
	//      没了 —— 于是「Index 持锁跑完整轮、内部每次 Put 再锁一次」这种
	//      结构会把互斥悄悄弄丢。
	//   2. 换个 fd 去拿同一把锁反而会**死锁自己**（同进程的第二个描述符
	//      会被第一个挡住）。
	depth int
}

// RunLockPath 是「一次索引运行」对应的锁文件路径。
//
// ★ 与 [LockPath] 分开：那把是**写锁**（毫秒级，罩住读—合并—写），这把是
// **运行锁**（十分钟量级，罩住一整次索引命令）。合成一把就等于让「一次写」
// 和「一整轮索引」争同一把锁，而那正是「一个小的增量索引要排十分钟队」的
// 来源。
func RunLockPath(dir string) string { return filepath.Join(dir, ".lock.run") }

// LockPath 是一个目录对应的锁文件路径。
//
// ★ 它是**派生**的而不是让调用方各写一遍：锁文件的名字只要有一处写错，
//
//	两个进程就会各锁各的，而双方都认为自己受保护。
func LockPath(dir string) string { return filepath.Join(dir, ".lock") }

// OpenFileLock 打开（必要时创建）一个锁文件。它**不加锁** —— 加锁是
// [FileLock.Lock] / [FileLock.Try] 的事。
func OpenFileLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("persist: create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("persist: open lock file %s: %w", path, err)
	}
	return &FileLock{path: path, f: f}, nil
}

// Lock 取锁，直到拿到或 ctx 结束。
//
// 可重入：同一实例重复调用只是加深计数，返回的释放函数配平一次。
func (l *FileLock) Lock(ctx context.Context) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	if l.depth > 0 {
		l.depth++
		return l.release, nil
	}
	for {
		ferr := lockFile(l.f)
		if ferr == nil {
			l.depth = 1
			l.stampHolder()
			return l.release, nil
		}
		if !errors.Is(ferr, errLockBusy) {
			return nil, fmt.Errorf("persist: lock %s: %w", l.path, ferr)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s is locked by another openheron process (%s): %w",
				l.path, l.holderHint(), ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// Try 取锁，被占用时立刻返回 [ErrLocked]。
//
// ★ 给**只读侧与 agent 工具**用：它们撞上正在跑的索引时应当说清楚「别的进程
//
//	在忙」，而不是挂在那里等十分钟 —— 一个 turn 里的工具调用没有十分钟可等。
func (l *FileLock) Try() (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	if l.depth > 0 {
		l.depth++
		return l.release, nil
	}
	ferr := lockFile(l.f)
	if ferr == nil {
		l.depth = 1
		l.stampHolder()
		return l.release, nil
	}
	if errors.Is(ferr, errLockBusy) {
		return nil, fmt.Errorf("persist: %s is held by another openheron process (%s): %w",
			l.path, l.holderHint(), ErrLocked)
	}
	return nil, fmt.Errorf("persist: lock %s: %w", l.path, ferr)
}

// ErrLocked 表示锁被别的进程占着。
var ErrLocked = fmt.Errorf("locked")

// errLockBusy 是「锁被占着」的**内部**哨兵。
//
// ★ 它不是 [ErrLocked]：后者是**对外**的错误（带着路径与持有者信息），
//
//	而这个只在平台实现与上层之间传递，用来把「别人正忙着」与「锁坏了」
//	分开 —— 前者可以等，后者要立刻报错。
var errLockBusy = errors.New("file lock is held by another process")

func (l *FileLock) release() {
	l.depth--
	if l.depth > 0 {
		return
	}
	// ★ 释放用解锁而不是关 fd：关掉之后下一个等待者要重新 open，而重新
	//   open 与加锁之间有一段窗口 —— 对「谁先谁后」有要求的地方那段窗口
	//   就是 bug。fd 留到进程退出由内核收。
	_ = unlockFile(l.f)
	_ = l.f.Truncate(0)
}

// Close 关掉锁文件。**不释放锁**（那由 release 做）—— 它只在实例不再使用
// 时调用，避免 fd 泄漏。
func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

// Path 返回锁文件路径（诊断用）。
func (l *FileLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// stampHolder 把自己的 pid 写进锁文件。
//
// ★ 它只服务于**诊断**：锁被占住时 `cat .lock` 能看到是谁，`kill -0` 能确认
//
//	它还在不在。锁的正确性完全不依赖这个文件的内容 —— flock 管的是内核里的
//	那把锁，不是这个文件。
func (l *FileLock) stampHolder() {
	if l.f == nil {
		return
	}
	body := strconv.Itoa(os.Getpid()) + "\n"
	if err := l.f.Truncate(0); err != nil {
		return
	}
	if _, err := l.f.WriteAt([]byte(body), 0); err != nil {
		return
	}
}

// holderHint 读锁文件里的 pid，读不到就给一句能照做的兜底。
func (l *FileLock) holderHint() string {
	b, err := os.ReadFile(l.path)
	if err != nil {
		return "holder unknown"
	}
	pid := strings.TrimSpace(string(b))
	if pid == "" {
		return "holder unknown"
	}
	return "pid " + pid
}

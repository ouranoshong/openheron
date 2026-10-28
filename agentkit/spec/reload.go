package spec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"openheron/agentkit/obs"
	"openheron/agentkit/spec/decl"
)

// HotDirs 是**参与热更新的目录**，由 [Categories] 推出。
//
// ★ server/ 不在其中：它配的是进程自身（listen / 静态目录 / 数据目录），
//
//	改了要重启。一个改了就生效、另一个改了不算，混在一起会让人以为
//	「我改了 listen 怎么没反应」。
//
// ★ 它**不是**手写的第二份清单 —— 那会与 [Categories] 漂移，
//
//	而漂移的表现是「某个分区改了不生效」：没有报错，只有沉默。
var HotDirs = hotDirList()

// Fingerprint 算出热更新目录的**内容指纹**。
//
// ★ 用「文件名 + 大小 + mtime」而不是内容哈希：配置很小，而每轮都读全部文件
//
//	是纯浪费；要避免的是**漏检**，所以目录名、文件名、大小、mtime 全都计入。
//	目录不存在**不是错误**（新项目就是空的），只是不贡献指纹。
func (s *Service) Fingerprint() string {
	h := sha256.New()
	if s.root == "" {
		return ""
	}
	for _, dir := range HotDirs {
		full := filepath.Join(s.root, dir)
		fmt.Fprintf(h, "%s\n", full)
		h.Write(dirEntryLines(full))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HotDirFingerprints 返回**每个热更目录各自**的指纹。
//
// ★ 分目录而不是只给一个总指纹：热更要能回答「改的是哪一份配置」。
//
//	改了 agents/main.md 与改了 policy/limits.yaml，在总指纹里长得一样，
//	而两者的处置完全不同 —— 前者立刻生效，后者要重启。
//	「我改了怎么没反应」正是这类问题，而答案就在这一个字段里。
func (s *Service) HotDirFingerprints() map[string]string {
	out := make(map[string]string, len(HotDirs))
	if s.root == "" {
		return out
	}
	for _, dir := range HotDirs {
		full := filepath.Join(s.root, dir)
		h := sha256.New()
		fmt.Fprintf(h, "%s\n", full)
		h.Write(dirEntryLines(full))
		out[dir] = hex.EncodeToString(h.Sum(nil))
	}
	return out
}

// dirEntryLines 返回一个目录里所有文件的指纹行。
//
// ★ 单独抽出来是为了让「总指纹」与「分目录指纹」算的是**同一份东西** ——
//
//	两处各写一遍的话，改了一处忘了另一处，热更就会按不同的口径判断
//	「变了没有」，症状是「有时生效有时不生效」。
func dirEntryLines(full string) []byte {
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// 显式排序：让指纹的定义**不依赖** ReadDir 已经有序这件事。
	sort.Strings(names)

	var buf bytes.Buffer
	for _, n := range names {
		info, err := fs.Stat(os.DirFS(full), n)
		if err != nil {
			continue
		}
		fmt.Fprintf(&buf, "%s %d %d\n", n, info.Size(), info.ModTime().UnixNano())
	}
	return buf.Bytes()
}

// ★ 防抖窗口的默认值住在 [decl]（那里是它作为「写错时的兜底」被用到的地方）。

// Debounce 解析 server/config.yaml 的 watch.debounce，失败退回默认。
//
// ★ **退回默认而不是让配置加载失败**：一个写错的 debounce 不该让整个程序
//
//	起不来，但它也**不该被静默忽略** —— 所以同时记一条日志。
func (c *Config) Debounce() time.Duration {
	if c == nil || c.Server.Watch.Debounce <= 0 {
		return decl.DefaultDebounce
	}
	return c.Server.Watch.Debounce.Duration()
}

// 收敛 decl.LenientDuration 的降级记录到 deferred，让「写错但已降级」也走同一条
// 「启动时必须打印」的通路。
func (c *Config) collectBadDurations() {
	for _, msg := range decl.BadDurations() {
		c.deferKey("watch.debounce fell back to default: %s", msg)
	}
}

// WatchEnabled 返回是否开启热更新（默认 true）。
//
// 用 `*bool` 表达「没写」与「显式写 false」的区别 —— 这里必须能区分：
// 没写 = 用默认（开），写了 false = 用户要关。
func (c *Config) WatchEnabled() bool {
	if c == nil || c.Server.Watch.Enabled == nil {
		return true
	}
	return *c.Server.Watch.Enabled
}

// Reloader 在配置变更时重新加载，并回调调用方（用于换代 decl.Agent Pool）。
type Reloader struct {
	svc *Service

	mu      sync.RWMutex
	loaded  *Config
	lastErr error

	// onChange 在**成功**换上新配置后调用。失败时**不调用** ——
	// 旧配置继续服务，调用方不该以为换代过了。
	onChange func(*Config)
}

// NewReloader 造一个重载器，并做首次加载。
func NewReloader(svc *Service, onChange func(*Config)) (*Reloader, error) {
	r := &Reloader{svc: svc, onChange: onChange}
	if _, err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Reload 重新加载。**失败时旧配置继续服务** —— 改坏配置不该让进程死。
func (r *Reloader) Reload() (*Config, error) {
	cfg, err := r.svc.Load()
	if err != nil {
		r.mu.Lock()
		r.lastErr = err
		r.mu.Unlock()
		return r.Current(), err
	}
	r.mu.Lock()
	r.loaded, r.lastErr = cfg, nil
	r.mu.Unlock()

	if r.onChange != nil {
		r.onChange(cfg)
	}
	return cfg, nil
}

// Current 返回最近一次**成功**加载的配置。首次加载失败时返回 nil。
func (r *Reloader) Current() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded
}

// LastErr 返回最近一次加载失败的原因。
func (r *Reloader) LastErr() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}

// decl.Watch 盯着配置目录，**变更稳定下来之后**调用一次 onChange。
//
// ★ 为什么是轮询而不是 inotify：
//
//   - 配置改动是**低频事件**（人编辑文件），200ms 粒度绰绰有余；
//   - **零依赖**，也不受 inotify watch 数量上限限制；
//   - 在容器 / WSL / 网络文件系统里 inotify **会静默失效**（收不到事件
//     且不报错），而轮询在这些环境里行为一致。
//
// 代价是每秒几次 stat —— 可以忽略，而「监听悄悄坏了」不可接受。
//
// 防抖的语义是「等到不再变」：保存一个大文件会产生多条事件，而我们要的是
// 「保存完了」那一刻。
func (r *Reloader) Watch(ctx context.Context) {
	debounce := decl.DefaultDebounce
	if c := r.Current(); c != nil {
		debounce = c.Debounce()
	}
	// 轮询间隔比防抖小一档：防抖是「合并事件」，轮询是「发现事件」。
	poll := debounce / 2
	if poll < 20*time.Millisecond {
		poll = 20 * time.Millisecond
	}

	// ★ 用**分目录**的指纹，而不是一个总指纹。
	//
	//	总指纹只能回答「有没有变」；分目录的还能回答「变的是哪一份」——
	//	而后者正是「我改了怎么没反应」的答案。
	prev := r.svc.HotDirFingerprints()
	timer := time.NewTimer(poll)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		before := prev
		cur := r.svc.HotDirFingerprints()
		if sameDirs(cur, prev) {
			timer.Reset(poll)
			continue
		}

		// 变了：等它**停下来**。窗口内又变了就重新等 —— 所以每轮重置。
		stable := false
		for !stable {
			timer.Reset(debounce)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			next := r.svc.HotDirFingerprints()
			stable = sameDirs(next, cur)
			cur = next
		}
		prev = cur

		if cfg, err := r.Reload(); err != nil {
			// ★ 记一条 warn 就好 —— 旧配置继续服务，进程不该因为改坏配置而死。
			LogReloadFailed(err)
		} else {
			// ★ 成功也要出声，而且要说清**改的是哪一份**。
			//
			//	「配置更新了」与「配置没更新」在界面上长得一样 ——
			//	后者是「改了没生效」这类问题最难查的一种。
			LogReloaded(cfg.Version, changedDirs(before, cur))
		}
		timer.Reset(poll)
	}
}

// sameDirs 比较两份目录指纹快照是否完全一致。
func sameDirs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// changedDirs 列出两份快照之间变了哪些目录（按名字排序）。
func changedDirs(before, after map[string]string) []string {
	out := make([]string, 0, len(after))
	for k, v := range after {
		if before[k] != v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// LogReloaded 是热更成功的日志出口，单独抽出来便于测试打桩。
//
// ★ changed 是**变了哪几个分区** —— 「改了 agents/ 还是 policy/」
//
//	是「配了但没生效」那类问题的第一个分叉点。
var LogReloaded = func(version string, changed []string) {
	obs.Logger().Info("[config] hot reload applied",
		"version", version, "changed", strings.Join(changed, ","))
}

// LogReloadFailed 是重载失败的日志出口，单独抽出来便于测试打桩。
var LogReloadFailed = func(err error) {
	obs.Logger().Error("[config] hot reload failed, keeping previous config", "err", err)
}

// DescribeHotDirs 返回正在盯的目录（启动日志用）。
func (s *Service) DescribeHotDirs() string {
	var out []string
	{
		for _, d := range HotDirs {
			out = append(out, filepath.Join(s.root, d))
		}
	}
	res := ""
	for i, v := range out {
		if i > 0 {
			res += ", "
		}
		res += v
	}
	return res
}

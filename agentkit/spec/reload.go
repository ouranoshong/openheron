package spec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

var HotDirs = hotDirList()

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

// dirEntryLines fingerprints a directory **tree**: every file under it, by
// relative path, size, modification time and **content**.
//
// ★ One level was not enough once `skills/` became a partition: a skill is
//
//	`skills/<name>/SKILL.md`, and editing that file changes neither the name
//	nor the size of the folder above it — the old walk saw a directory and
//	moved on, so a saved skill never reached the next turn. The walk is
//	recursive for every partition now; the flat ones simply have nothing
//	below them.
//
// ★ The **content hash** is there because the modification time is not a
//
//	truth about the content. Two writes inside the same clock tick carry
//	the same timestamp, so a file edited **in place, with the same length**
//	can look untouched to the fingerprint — and the change never lands.
//	That is not a theoretical concern: the filesystem's timestamp
//	granularity is coarser than the clock, and a test that rewrites a
//	skill with a body of the same length fails on one run out of three.
//
//	The hash is **added** to the timestamp, not swapped for it: a file whose
//	content is unchanged but whose timestamp moved still changes the
//	fingerprint, which is what the reload has always done.
func dirEntryLines(full string) []byte {
	var buf bytes.Buffer
	// ★ filepath.WalkDir visits in lexical order and does not follow
	//   symlinks — the fingerprint only has to be stable, and a symlink loop
	//   would make it endless.
	_ = filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(full, path)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		fmt.Fprintf(&buf, "%s %d %d %s\n", filepath.ToSlash(rel), info.Size(),
			info.ModTime().UnixNano(), contentHash(path))
		return nil
	})
	return buf.Bytes()
}

// contentHash is the hash of a file's bytes.
//
// ★ A file that cannot be read still takes part in the fingerprint, with an
//
//	empty hash: dropping it would make a read error look like a deletion.
func contentHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (c *Config) Debounce() time.Duration {
	if c == nil || c.Server.Watch.Debounce <= 0 {
		return decl.DefaultDebounce
	}
	return c.Server.Watch.Debounce.Duration()
}

func (c *Config) collectBadDurations() {
	for _, msg := range decl.BadDurations() {
		c.deferKey("watch.debounce fell back to default: %s", msg)
	}
}

func (c *Config) WatchEnabled() bool {
	if c == nil || c.Server.Watch.Enabled == nil {
		return true
	}
	return *c.Server.Watch.Enabled
}

// Option configures a Reloader.
type Option func(*Reloader)

// WithLogger sets the logger the watcher writes through. Nil drops the lines.
func WithLogger(log *slog.Logger) Option {
	return func(r *Reloader) { r.log = log }
}

var discard = obs.Discard()

func (r *Reloader) logger() *slog.Logger {
	if r == nil || r.log == nil {
		return discard
	}
	return r.log
}

type Reloader struct {
	svc *Service
	log *slog.Logger

	mu      sync.RWMutex
	loaded  *Config
	lastErr error

	onChange func(*Config)
}

func NewReloader(svc *Service, onChange func(*Config), opts ...Option) (*Reloader, error) {
	r := &Reloader{svc: svc, onChange: onChange}
	for _, o := range opts {
		o(r)
	}
	if _, err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

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

func (r *Reloader) Current() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded
}

func (r *Reloader) LastErr() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}

func (r *Reloader) Watch(ctx context.Context) {
	debounce := decl.DefaultDebounce
	if c := r.Current(); c != nil {
		debounce = c.Debounce()
	}
	poll := debounce / 2
	if poll < 20*time.Millisecond {
		poll = 20 * time.Millisecond
	}

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
			r.logger().Error("[config] hot reload failed, keeping previous config", "err", err)
		} else {
			r.logger().Info("[config] hot reload applied",
				"version", cfg.Version, "changed", strings.Join(changedDirs(before, cur), ","))
		}
		timer.Reset(poll)
	}
}

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

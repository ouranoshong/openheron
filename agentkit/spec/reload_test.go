package spec

import (
	"context"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionChangesWithContent(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	c1, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c1.Version == "" {
		t.Fatal("★ Version must not be empty — it is the key that decides hot reload and the Pool generation")
	}
	c2, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c1.Version != c2.Version {
		t.Fatal("the fingerprint must be stable when nothing changed")
	}

	f.write(t, "agents/main.md", "---\nmodel: openai/strong\n---\n改过了\n")
	c3, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c3.Version == c1.Version {
		t.Fatal("the fingerprint must change after the content changed")
	}
}

func TestServerDirIsNotHot(t *testing.T) {
	for _, d := range HotDirs {
		if d == DirServer {
			t.Fatal("a change under server/ needs a restart and must not take part in hot reload")
		}
	}
	// ★ skills/ is hot for the same reason the other four are: a SKILL.md is
	//   content the agent reads, so editing one has to reach the next turn
	//   without a restart. The walk is recursive — a file two levels down is
	//   what a skill actually is.
	want := []string{DirAgent, DirLLM, DirMCP, DirSkills, DirTools}
	if len(HotDirs) != len(want) {
		t.Fatalf("hot dirs = %v, want exactly %v", HotDirs, want)
	}
	for i, d := range want {
		if HotDirs[i] != d {
			t.Fatalf("hot dirs = %v, want exactly %v", HotDirs, want)
		}
	}
}

// ★ The fingerprint walks the tree **recursively**, and a skill is the case
// that needs it: `skills/<name>/SKILL.md` edits the file, not the folder, so a
// one-level walk saw an unchanged directory and the saved skill never reached
// the next turn.
func TestEditingASkillChangesTheVersion(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	seedSkill(t, f, "code-review", "---\ndescription: one\n---\nbody\n")

	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	before := s.Fingerprint()
	if before == "" {
		t.Fatal("the fingerprint must not be empty")
	}

	// Same name, same folder, same size class — only the content moves.
	seedSkill(t, f, "code-review", "---\ndescription: two\n---\nbody\n")
	if got := s.Fingerprint(); got == before {
		t.Fatal("★ editing skills/<name>/SKILL.md must change the fingerprint, or the change never lands")
	}
}

// ★ The fingerprint follows the **content**, not the timestamp.
//
//	★ The modification time is not a truth about the content: two writes
//	  inside the same clock tick carry the same timestamp, and a file
//	  rewritten **with the same length** then looks untouched. This case
//	  pins that down by putting the timestamp back exactly where it was —
//	  only the content differs, and the fingerprint must still move.
func TestTheFingerprintFollowsTheContentNotTheTimestamp(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	seedSkill(t, f, "code-review", "---\ndescription: one\n---\nbody\n")

	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	before := s.Fingerprint()
	if before == "" {
		t.Fatal("the fingerprint must not be empty")
	}

	path := filepath.Join(f.root, DirSkills, "code-review", SkillFileName)
	stamp, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same length, different content.
	seedSkill(t, f, "code-review", "---\ndescription: two\n---\nbody\n")
	// ★ And the timestamp goes back to where it was.
	if err := os.Chtimes(path, stamp.ModTime(), stamp.ModTime()); err != nil {
		t.Fatal(err)
	}

	if got := s.Fingerprint(); got == before {
		t.Fatal("★ the content changed, so the fingerprint must change — a timestamp is not a truth about the content")
	}
}

func TestToolsDirIsHot(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	var n atomic.Int32
	r, err := NewReloader(s, func(*Config) { n.Add(1) })
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Watch(ctx)

	time.Sleep(50 * time.Millisecond)
	f.write(t, "tools/http.yaml", "methods: [get]\ntimeout: 30s\n")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n.Load() < 2 {
		t.Fatalf("* writing tools/ must trigger a reload, got %d callbacks", n.Load())
	}
	cfg := r.Current()
	if cfg == nil {
		t.Fatal("the reloaded config must be available")
	}
	if cfg.Tools.HTTP == nil {
		t.Fatal("tools/http.yaml was written, so the http tool must be on")
	}
	if got := cfg.EnabledToolNames(); len(got) == 0 || got[0] != decl.ToolNameHTTPGet {
		t.Fatalf("enabled tool names = %v, want [%s]", got, decl.ToolNameHTTPGet)
	}
}

func TestReloadFailureKeepsLastGood(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	r, err := NewReloader(s, func(c *Config) { got = append(got, c.Version) })
	if err != nil {
		t.Fatal(err)
	}
	good := r.Current()
	if good == nil {
		t.Fatal("the first load should succeed")
	}

	f.write(t, "llm/openai.yaml", "type: x\nbase_url: https://y\nbogus_key: 1\n")
	if _, err := r.Reload(); err == nil {
		t.Fatal("a broken configuration must be an error")
	}
	if r.Current() != good {
		t.Fatal("★ after a failed load the current configuration must still be the previous one")
	}
	if r.LastErr() == nil {
		t.Fatal("the failure reason must be kept for logging and troubleshooting")
	}
	if len(got) != 1 {
		t.Fatalf("* a failure must not trigger onChange (it would look like a successful reload), got %d callbacks", len(got))
	}

	f.write(t, "llm/openai.yaml", minimalLLM)
	if _, err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("after recovery it must call back once more, got %d", len(got))
	}
}

func TestNewReloaderFailsWhenFirstLoadFails(t *testing.T) {
	root := t.TempDir()
	s, err := NewService(filepath.Join(root, "u"), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewReloader(s, nil); err == nil {
		t.Fatal("a failed first load must return an error (there is no such thing as an empty configuration)")
	}
}

func TestWatchEnabledDistinguishesUnsetFromFalse(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	c, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if !c.WatchEnabled() {
		t.Error("an omitted enabled must default to on")
	}

	f.write(t, "server/config.yaml", "watch:\n  enabled: false\n")
	c, err = f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.WatchEnabled() {
		t.Error("an explicit false must be respected — omitting it and writing false are two different things")
	}
}

func TestDebounceFallsBackWithoutFailing(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "server/config.yaml", "watch:\n  debounce: not-a-duration\n")

	c, err := f.load(t)
	if err != nil {
		t.Fatalf("* a malformed debounce must not fail the config load: %v", err)
	}
	if c.Debounce() != decl.DefaultDebounce {
		t.Fatalf("it must fall back to the default debounce, got %v", c.Debounce())
	}
}

func TestWatchDetectsChangeAndSwaps(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	var n atomic.Int32
	r, err := NewReloader(s, func(*Config) { n.Add(1) })
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Watch(ctx)

	time.Sleep(50 * time.Millisecond)
	f.write(t, "agents/main.md", "---\nmodel: openai/strong\n---\n改了\n")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n.Load() < 2 {
		t.Fatalf("* the watcher must fire within 3s, got %d callbacks", n.Load())
	}
	if got := r.Current().Agents["main"].ModelRef; got != "openai/strong" {
		t.Fatalf("the new config did not take effect: model=%q", got)
	}
}

func TestWatchIgnoresServerDir(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, _ := NewService(f.root, filepath.Dir(f.root))

	var n atomic.Int32
	r, err := NewReloader(s, func(*Config) { n.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Watch(ctx)

	time.Sleep(50 * time.Millisecond)
	f.write(t, "server/config.yaml", "listen_addr: \":7777\"\n")

	time.Sleep(700 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("* editing server/ must not trigger a reload (it needs a restart), got %d callbacks", n.Load())
	}
}

func TestWatchExitsOnContextCancel(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, _ := NewService(f.root, filepath.Dir(f.root))
	r, err := NewReloader(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.Watch(ctx) }()
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("decl.Watch must exit when ctx is cancelled")
	}
}

// reloadNotice is what the hot-reload test reads out of the log.
type reloadNotice struct {
	version string
	changed []string
}

// noticeHandler turns a log record into a reloadNotice. It never blocks: the
// watcher must not wait for the test.
type noticeHandler struct{ ch chan reloadNotice }

func (h noticeHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h noticeHandler) Handle(_ context.Context, r slog.Record) error {
	n := reloadNotice{}
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "version":
			n.version = a.Value.String()
		case "changed":
			if s := a.Value.String(); s != "" {
				n.changed = strings.Split(s, ",")
			}
		}
		return true
	})
	select {
	case h.ch <- n:
	default:
	}
	return nil
}

func (h noticeHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h noticeHandler) WithGroup(string) slog.Handler      { return h }

func TestHotReloadLogsWhichPartitionsChanged(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	// ★ The notification travels through a channel, not through captured
	//   variables: the hook runs on the watcher's goroutine, and a variable
	//   it writes is read by the test goroutine — that is a race, and the
	//   detector reports it as one.
	notices := make(chan reloadNotice, 1)

	r, err := NewReloader(s, func(*Config) {}, WithLogger(slog.New(noticeHandler{ch: notices})))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		r.Watch(ctx)
	}()
	// ★ Stopping the watcher is what orders this test's reads: the writes
	//   the hook made happen-before the goroutine ends, and the goroutine
	//   ending happens-before the receive below.
	defer func() {
		cancel()
		<-watcherDone
	}()

	time.Sleep(50 * time.Millisecond)
	f.write(t, "agents/main.md", "---\nmodel: openai/strong\n---\n改了\n")

	var got reloadNotice
	deadline := time.After(3 * time.Second)
	for len(got.changed) == 0 {
		select {
		case got = <-notices:
		case <-deadline:
			t.Fatal("the hot reload must report which partition changed")
		}
	}
	if got.changed[0] != "agents" {
		t.Fatalf("changed = %v, want agents", got.changed)
	}
	if got.version == "" {
		t.Fatal("the hot reload must report the new version")
	}
}

func TestHotDirFingerprintsSeparatePartitions(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	before := s.HotDirFingerprints()
	if len(before) == 0 {
		t.Fatal("expected one fingerprint per hot directory")
	}
	f.write(t, "agents/main.md", "---\nmodel: openai/strong\n---\n改了\n")
	after := s.HotDirFingerprints()

	if before["agents"] == after["agents"] {
		t.Fatal("the agents fingerprint must change when a file changes")
	}
	for dir, fp := range before {
		if dir == "agents" {
			continue
		}
		if after[dir] != fp {
			t.Fatalf("the %s fingerprint changed although nothing in it changed", dir)
		}
	}
}

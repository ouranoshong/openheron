package spec

import (
	"context"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
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
	want := []string{DirAgent, DirLLM, DirMCP, DirPolicy, DirTools}
	if len(HotDirs) != len(want) {
		t.Fatalf("hot dirs = %v, want exactly %v", HotDirs, want)
	}
	for i, d := range want {
		if HotDirs[i] != d {
			t.Fatalf("hot dirs = %v, want exactly %v", HotDirs, want)
		}
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

func TestLogReloadFailedIsSwappable(t *testing.T) {
	orig := LogReloadFailed
	var sb strings.Builder
	LogReloadFailed = func(err error) { sb.WriteString(err.Error()) }
	defer func() { LogReloadFailed = orig }()

	LogReloadFailed(os.ErrNotExist)
	if !strings.Contains(sb.String(), "not exist") {
		t.Fatalf("the log sink was never called: %q", sb.String())
	}
}

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
	type notice struct {
		version string
		changed []string
	}
	notices := make(chan notice, 1)

	orig := LogReloaded
	LogReloaded = func(version string, changed []string) {
		// ★ The send never blocks: the watcher must not wait for the test.
		select {
		case notices <- notice{version: version, changed: changed}:
		default:
		}
	}
	// ★ The hook is restored last — after the watcher has stopped, because
	//   the watcher reads this package-level variable.
	defer func() { LogReloaded = orig }()

	r, err := NewReloader(s, func(*Config) {})
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

	var got notice
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

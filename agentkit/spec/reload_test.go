package spec

import (
	"context"
	"openheron/agentkit/spec/decl"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── 版本指纹 ─────────────────────────────────────────────────────────────

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
		t.Fatal("★ Version 必须非空 —— 它是热更新与 Pool 代际的判定键")
	}
	c2, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c1.Version != c2.Version {
		t.Fatal("未改动时指纹必须稳定")
	}

	// 改内容 → 指纹变。
	f.write(t, "agents/main.md", "---\nmodel: openai/strong\n---\n改过了\n")
	c3, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c3.Version == c1.Version {
		t.Fatal("内容改动后指纹必须变")
	}
}

// server/ **不进热更新** —— 它配的是进程自身，改了要重启。
//
// ★ 清单是**逐项写死**的，不是「至少包含这几个」。少了会漏（某个分区
//
//	改了不生效，只有沉默），多了会让 server/ 悄悄混进来。
func TestServerDirIsNotHot(t *testing.T) {
	for _, d := range HotDirs {
		if d == DirServer {
			t.Fatal("server/ 改了要重启，不得参与热更新")
		}
	}
	want := []string{DirAgent, DirLLM, DirPolicy, DirTools}
	if len(HotDirs) != len(want) {
		t.Fatalf("hot dirs = %v, want exactly %v", HotDirs, want)
	}
	for i, d := range want {
		if HotDirs[i] != d {
			t.Fatalf("hot dirs = %v, want exactly %v", HotDirs, want)
		}
	}
}

// tools/ **进热更新** —— 它是 2026-10-24 从 server/ 搬出来的，
// 而搬出来的理由就是热更新：「改一个工具的超时」不该要重启进程。
//
// ★ 症状是「配置改了但工具表没变」，而那**没有任何报错** ——
//
//	所以这条要端到端验：真的写一个 tools/http.yaml，确认换代被触发。
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
	// 换代之后，新配置里那个工具必须**真的开着** —— 「触发了换代」
	// 与「换代后的配置是对的」是两件事。
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

// ── 失败不崩 ─────────────────────────────────────────────────────────────
// ★ 改坏配置不该让进程死 —— 旧配置继续服务。
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
		t.Fatal("首次加载应成功")
	}

	// 写坏配置（未知字段）。
	f.write(t, "llm/openai.yaml", "type: x\nbase_url: https://y\nbogus_key: 1\n")
	if _, err := r.Reload(); err == nil {
		t.Fatal("坏配置应报错")
	}
	if r.Current() != good {
		t.Fatal("★ 加载失败后当前配置必须仍是上一份")
	}
	if r.LastErr() == nil {
		t.Fatal("失败原因必须留存，供日志/排障")
	}
	if len(got) != 1 {
		t.Fatalf("* a failure must not trigger onChange (it would look like a successful reload), got %d callbacks", len(got))
	}

	// 改回好的 → 恢复。
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
	// 没有 agents/main.md ⇒ 首屏就失败。
	if _, err := NewReloader(s, nil); err == nil {
		t.Fatal("首次加载失败应返回错误（没有「空配置」这种状态）")
	}
}

// ── watch 参数 ───────────────────────────────────────────────────────────

func TestWatchEnabledDistinguishesUnsetFromFalse(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	c, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if !c.WatchEnabled() {
		t.Error("没写 enabled 应默认为开")
	}

	f.write(t, "server/config.yaml", "watch:\n  enabled: false\n")
	c, err = f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.WatchEnabled() {
		t.Error("显式写 false 应被尊重 —— 「没写」与「写了 false」是两件事")
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

// ── decl.Watch 真的能发现变更 ─────────────────────────────────────────────────

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

	// 改一个热更新目录里的文件。
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

// 改 server/ **不该**触发回调。
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
		t.Fatal("decl.Watch 应随 ctx 取消而退出")
	}
}

func TestLogReloadFailedIsSwappable(t *testing.T) {
	// 打桩出口存在 —— 「失败要出声」必须可断言。
	orig := LogReloadFailed
	var sb strings.Builder
	LogReloadFailed = func(err error) { sb.WriteString(err.Error()) }
	defer func() { LogReloadFailed = orig }()

	LogReloadFailed(os.ErrNotExist)
	if !strings.Contains(sb.String(), "not exist") {
		t.Fatalf("the log sink was never called: %q", sb.String())
	}
}

// 热更成功要**出声**，而且要说清改的是哪一份 ——
//
// ★ 「改了没生效」是这类问题最难查的一种：界面上「配置更新了」与
//
//	「配置没更新」长得一模一样。日志里那句「changed: agents/」就是分叉点。
func TestHotReloadLogsWhichPartitionsChanged(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s, err := NewService(f.root, filepath.Dir(f.root))
	if err != nil {
		t.Fatal(err)
	}

	orig := LogReloaded
	var gotVersion string
	var gotChanged []string
	LogReloaded = func(version string, changed []string) {
		gotVersion = version
		gotChanged = changed
	}
	defer func() { LogReloaded = orig }()

	r, err := NewReloader(s, func(*Config) {})
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
		if len(gotChanged) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(gotChanged) == 0 {
		t.Fatal("the hot reload must report which partition changed")
	}
	if gotChanged[0] != "agents" {
		t.Fatalf("changed = %v, want agents", gotChanged)
	}
	if gotVersion == "" {
		t.Fatal("the hot reload must report the new version")
	}
}

// 分目录的指纹：改一份不能动另一份。
//
// ★ 这是「changed: agents/」能说准的前提 —— 若指纹是混在一起的一个总数，
//
//	那「变了」只能回答「有没有变」。
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

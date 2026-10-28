package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	hserver "github.com/cloudwego/hertz/pkg/app/server"

	"github.com/cloudwego/eino/callbacks"

	"github.com/ouranoshong/openheron/agentkit/build"
	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/obs/hook"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/api"
	"github.com/ouranoshong/openheron/storage/filestore"
	"github.com/ouranoshong/openheron/webui"
)

//go:embed all:web/dist
var webAssets embed.FS

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "kb" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := kbCmd(ctx, os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("openheron kb: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := initCmd(os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("openheron init: %v", err)
		}
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		}
	}
	flags, err := parseServerFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printServerUsage(os.Stdout)
			return
		}
		log.Fatalf("openheron: %v", err)
	}
	if err := runServer(flags); err != nil {
		log.Fatalf("openheron: %v", err)
	}
}

type serverFlags struct {
	configDir   string
	projectRoot string
}

func parseServerFlags(args []string) (serverFlags, error) {
	fs := flag.NewFlagSet("openheron", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f serverFlags
	fs.StringVar(&f.configDir, "config-dir", "",
		"the config root to read (default: <project-root>/.openheron, or $OPENHERON_CONFIG_DIR)")
	fs.StringVar(&f.projectRoot, "project-root", "",
		"the project root (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, fmt.Errorf("unexpected argument %q; openheron takes flags only", fs.Arg(0))
	}
	return f, nil
}

func printServerUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron [--config-dir <path>] [--project-root <path>]")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Reads exactly one configuration root: --config-dir, else $OPENHERON_CONFIG_DIR,")
	fmt.Fprintln(stdout, "else the nearest .openheron/ at or above the project root.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  --config-dir   the config root (llm/ agents/ policy/ server/ tools/ live here)")
	fmt.Fprintln(stdout, "  --project-root the project root: agent workdir and relative-path base")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Other commands: openheron init, openheron kb, openheron version")
}

func warnUnusedHomeConfig(root string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	homeCfg := filepath.Join(home, spec.DefaultBaseDirName)
	if homeCfg == root {
		return
	}
	if fi, err := os.Stat(homeCfg); err != nil || !fi.IsDir() {
		return
	}
	obs.Logger().Warn("[main] a config dir exists but is not the config root in use",
		"dir", homeCfg, "root", root)
}

func runServer(flags serverFlags) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	installObserver()

	st, err := setupConfig(flags)
	if err != nil {
		return err
	}
	if err := setupRuntime(ctx, st); err != nil {
		return err
	}
	return serve(ctx, st)
}

func installObserver() {
	logger := obs.DefaultLogger(os.Stderr)
	obs.SetLogger(logger)
	callbacks.AppendGlobalHandlers(hook.New(logger))
}

type startup struct {
	cfgSvc   *spec.Service
	mgr      *agentConfig
	reloader *spec.Reloader
	cfg      *spec.Config

	fm      *filestore.FileManager
	builder *build.Builder
	rt      *run.Runtime
	srv     *api.Server

	addr        string
	dataDir     string
	projectRoot string
}

func setupConfig(flags serverFlags) (*startup, error) {
	cfgSvc, err := spec.NewService(flags.configDir, flags.projectRoot)
	if err != nil {
		return nil, err
	}
	warnUnusedHomeConfig(cfgSvc.Root())

	obs.Logger().Info("[main] openheron", "version", version)

	mgr := &agentConfig{}
	reloader, err := spec.NewReloader(cfgSvc, func(c *spec.Config) {
		mgr.set(c)
		obs.Logger().Info("[main] config loaded", "version", short(c.Version))
		for _, d := range c.Deferred() {
			obs.Logger().Info("[main] config notice", "notice", d)
		}
	})
	if err != nil {
		return nil, err
	}
	cfg, _ := mgr.Current()

	cfgDataDir, err := cfgSvc.DataDir(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}

	return &startup{
		cfgSvc:      cfgSvc,
		mgr:         mgr,
		reloader:    reloader,
		cfg:         cfg,
		addr:        override("LISTEN_ADDR", cfg.Server.ListenAddr),
		dataDir:     override("DATA_DIR", cfgDataDir),
		projectRoot: override("PROJECT_ROOT", cfgSvc.ProjectRoot()),
	}, nil
}

func setupRuntime(ctx context.Context, st *startup) error {
	sessionsRoot := filepath.Join(st.dataDir, "sessions")

	// The session manager needs a teardown hook that only exists once the
	// Runtime does, and the Runtime needs the manager. Break the cycle by
	// constructing the manager first and injecting afterwards.
	fm, err := filestore.NewFileManager(sessionsRoot, filestore.Options{
		CheckpointLegacyRoot: filepath.Join(st.dataDir, "checkpoints"),
	})
	if err != nil {
		return err
	}
	st.fm = fm

	// The filesystem tools take paths, and the agent has no other way to learn
	// where the code lives. It arrives through WithPaths, not the spec.
	builder := build.NewBuilder(st.mgr, build.WithPaths(st.projectRoot, st.dataDir))
	st.builder = builder

	rt := run.New(run.Config{
		AgentBuilder:   builder,
		SessionManager: fm,
		Input:          run.InputConfig{ProjectRoot: st.projectRoot, Workspace: sessionsRoot},
		StartTimeout:   startTimeout(st.cfg),
	})
	fm.SetTeardown(rt)
	st.rt = rt

	// Inspection depends on both stores, so it runs after wiring and before the
	// first request.
	inspectSessions(ctx, st)

	if st.cfg.WatchEnabled() {
		go st.reloader.Watch(ctx)
		obs.Logger().Info("[main] hot reload enabled", "watching", st.cfgSvc.DescribeHotDirs())
	}

	st.srv = api.New(rt, fm, hserver.WithHostPorts(st.addr))
	st.srv.SetConfigSource(func() *spec.Config {
		c, _ := st.mgr.Current()
		return c
	})
	st.srv.SetConfigService(st.cfgSvc)
	st.srv.SetToolsSource(toolsAdapter{b: builder})
	st.srv.SetStartTimeoutSource(func() time.Duration {
		c, _ := st.mgr.Current()
		return startTimeout(c)
	})
	st.srv.SetPoolSource(func() map[string]any {
		hits, misses, size, gen := builder.Stats()
		return map[string]any{
			"hits": hits, "misses": misses, "size": size, "gen": gen,
		}
	})
	st.srv.SetSkillsSource(func() []api.SkillView {
		list := build.Skills(context.Background())
		out := make([]api.SkillView, 0, len(list))
		for _, s := range list {
			out = append(out, api.SkillView{
				Name:        s.Name,
				Description: s.Description,
				Context:     s.Context,
				Agent:       s.Agent,
				Model:       s.Model,
			})
		}
		return out
	})

	return setupFrontend(st)
}

func inspectSessions(ctx context.Context, st *startup) {
	if h, m, size, gen := st.builder.Stats(); st.cfg.WatchEnabled() {
		obs.Logger().Info("[main] agent pool", "gen", gen, "entries", size, "hits", h, "misses", m)
	}

	report, err := st.fm.Inspect(ctx)
	if err != nil {
		obs.Logger().Error("[main] startup inspection failed", "err", err)
		return
	}
	obs.Logger().Info("[main] inspection",
		"clean", report.Clean, "pending", report.PendingOK, "orphan", report.Orphan,
		"ownerless", report.Ownerless, "skipped", report.SkippedBadJSON)
	if report.Orphan > 0 {
		obs.Logger().Warn("[main] sessions hold a pending approval with no checkpoint; "+
			"the card stays visible but the round cannot be resumed",
			"count", report.Orphan)
	}
	if report.Ownerless > 0 {
		obs.Logger().Warn("[main] sessions hold a checkpoint with no pending approval and cannot be resumed",
			"count", report.Ownerless)
	}
}

func setupFrontend(st *startup) error {
	mode := override("FRONTEND_MODE", st.cfg.Server.FrontendMode)
	assets, aerr := fs.Sub(webAssets, "web/dist")

	switch mode {
	case "embed", "":
		if aerr != nil || !webui.Available(assets) {
			obs.Logger().Warn("[main] frontend disabled: no web/dist build found; run `make web` to embed the UI")
			return nil
		}
		if err := st.srv.SetEmbeddedFrontend(assets, "/"); err != nil {
			return err
		}
		obs.Logger().Info("[main] frontend: embedded from web/dist")
	case "dir":
		dir := st.cfgSvc.ResolveDir(override("STATIC_DIR", st.cfg.Server.StaticDir))
		if dir == "" {
			obs.Logger().Warn("[main] frontend_mode=dir but static_dir is empty; set STATIC_DIR")
			return nil
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			obs.Logger().Warn("[main] static dir does not exist, frontend disabled", "dir", dir)
			return nil
		}
		st.srv.SetStaticDir(dir)
		obs.Logger().Info("[main] frontend: static dir", "dir", dir)
	default:
		obs.Logger().Warn("[main] unknown frontend_mode; expected embed or dir", "mode", mode)
	}
	return nil
}

func serve(ctx context.Context, st *startup) error {
	errCh := make(chan error, 1)
	go func() { errCh <- st.srv.Spin() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		obs.Logger().Info("[main] shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.rt.Shutdown(shutdownCtx); err != nil {
			obs.Logger().Error("[main] shutdown failed", "err", err)
		}
		return nil
	}
}

type toolsAdapter struct{ b *build.Builder }

func verdictFrom(vs []build.ToolVerdict) []api.ToolVerdict {
	out := make([]api.ToolVerdict, 0, len(vs))
	for _, v := range vs {
		out = append(out, api.ToolVerdict{
			Name:          v.Name,
			Tier:          string(v.Tier),
			Reason:        v.Reason,
			Present:       v.Present,
			ObservedKnown: v.ObservedKnown,
		})
	}
	return out
}

func (a toolsAdapter) EffectiveTools(agentID string) []api.ToolVerdict {
	return verdictFrom(a.b.EffectiveTools(agentID))
}

func (a toolsAdapter) PolicyTools() []api.ToolVerdict {
	return verdictFrom(a.b.PolicyTools())
}

func (a toolsAdapter) DescriptionVerdicts() []api.DescriptionVerdict {
	vs := a.b.DescriptionVerdicts()
	out := make([]api.DescriptionVerdict, 0, len(vs))
	for _, v := range vs {
		out = append(out, api.DescriptionVerdict{
			AgentID:    v.AgentID,
			Empty:      v.Empty,
			Duplicates: v.Duplicates,
			Distinct:   v.Distinct,
		})
	}
	return out
}

func (a toolsAdapter) RuntimeTools(agentID string) api.Observed {
	o := a.b.RuntimeTools(agentID)
	return api.Observed{Names: o.Names, Known: o.Known}
}

type agentConfig struct {
	mu  sync.RWMutex
	cfg *spec.Config
	gen uint64
}

func (c *agentConfig) Current() (*spec.Config, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg, c.gen
}

func (c *agentConfig) set(cfg *spec.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	c.gen++
}

func override(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func short(v string) string {
	if len(v) > 12 {
		return v[:12]
	}
	return v
}

func startTimeout(c *spec.Config) time.Duration {
	if c == nil || c.Server.StartTimeout <= 0 {
		return run.DefaultStartTimeout
	}
	return c.Server.StartTimeout.Duration()
}

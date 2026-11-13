package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	hserver "github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"

	"github.com/cloudwego/eino/callbacks"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/ouranoshong/openheron/agentkit/build"
	"github.com/ouranoshong/openheron/agentkit/obs"
	hertzlog "github.com/ouranoshong/openheron/agentkit/obs/hertz"
	"github.com/ouranoshong/openheron/agentkit/obs/hook"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/api"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/initcfg"
	"github.com/ouranoshong/openheron/storage/filestore"
	"github.com/ouranoshong/openheron/storage/memarbit"
	"github.com/ouranoshong/openheron/webui"
)

//go:embed all:web/dist
var webAssets embed.FS

var version = "dev"

func main() {
	// ★ A bootstrap logger, built before any branch: every early exit has to
	//   report through the same envelope as the server does. runServer builds
	//   its own and hands it to the two frameworks.
	boot, _, _ := newLogger()

	if len(os.Args) > 1 && os.Args[1] == "kb" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := kbCmd(ctx, os.Args[2:], os.Stdout, boot); err != nil {
			boot.Error("openheron kb failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := initCmd(os.Args[2:], os.Stdout); err != nil {
			boot.Error("openheron init failed", "err", err)
			os.Exit(1)
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
		boot.Error("openheron failed", "err", err)
		os.Exit(1)
	}
	if err := runServer(flags); err != nil {
		boot.Error("openheron failed", "err", err)
		os.Exit(1)
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
		"the config root to read (default: <project-root>/.openheron, created when missing; or $OPENHERON_CONFIG_DIR)")
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
	fmt.Fprintln(stdout, "else <project-root>/.openheron (created when missing).")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  --config-dir   the config root (llm/ agents/ mcps/ server/ tools/ live here)")
	fmt.Fprintln(stdout, "  --project-root the project root: agent workdir and relative-path base")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Other commands: openheron init, openheron kb, openheron version")
}

func warnUnusedHomeConfig(root string, log *slog.Logger) {
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
	// The config root is never searched for anymore, so a config in the home
	// directory is never picked up on its own — it has to be named.
	log.Warn("[main] a config dir exists but is not the config root in use; "+
		"pass --config-dir or set $OPENHERON_CONFIG_DIR to use it",
		"dir", homeCfg, "root", root)
}

// configLoadError turns a config load failure into an actionable message.
//
// The upward search is gone, so a command started in the wrong directory now
// resolves to a config root that holds nothing — and the bare load error
// ("agents/main.md does not exist") reads like a lost file rather than a
// wrong directory. The root path is the missing piece, and for a defaulted
// root the way out is one command.
func configLoadError(svc *spec.Service, err error) error {
	if svc.RootIsDefaulted() {
		agent := filepath.Join(svc.Root(), spec.DirAgent, spec.DefaultAgentID+".md")
		if _, statErr := os.Stat(agent); statErr != nil {
			return fmt.Errorf("no configuration at %s: run `openheron init` to create one", svc.Root())
		}
	}
	return fmt.Errorf("config: load %s: %w", svc.Root(), err)
}

// initConfigIfEmpty writes the factory configuration when the process starts on
// a project that has none.
//
// ★ Only the **defaulted** root is filled: `--config-dir` and
// `$OPENHERON_CONFIG_DIR` name a root, and a named root that is empty is the
// user's decision, not a gap for us to fill. ★ The test instance names its
// root, so it never has a configuration written under it.
//
// ★ The plan is the one `openheron init` builds — the same templates, the
// same rules — so "the server created it" and "the command created it" are the
// same tree. The model is the provider template's own default
// (deepseek/deepseek-flash) and the api key is read from the environment at
// request time, exactly as with `openheron init` without flags.
//
// ★ "Empty" means **no agent file**: a tree that has one is a configuration,
// whether or not it loads, and this must not touch it. The plan itself skips
// the files that are already there, so a half-written tree is topped up
// rather than replaced.
func initConfigIfEmpty(svc *spec.Service, log *slog.Logger) error {
	if log == nil {
		log = obs.Discard()
	}
	if !svc.RootIsDefaulted() {
		return nil
	}
	agent := filepath.Join(svc.Root(), spec.DirAgent, spec.DefaultAgentID+".md")
	if _, err := os.Stat(agent); err == nil {
		return nil
	}
	plan, err := initcfg.BuildPlan(svc, initcfg.Options{})
	if err != nil {
		return err
	}
	if err := plan.Apply(svc); err != nil {
		return err
	}
	log.Info("[main] configuration initialized", "dir", svc.Root(),
		"model", plan.Model, "files", len(plan.Files))
	if plan.EnvHint != "" {
		log.Info("[main] the api key is read from the environment at request time",
			"env", plan.EnvHint)
	}
	return nil
}

func runServer(flags serverFlags) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log, level, sink := installLogger()

	st, err := setupConfig(flags, log, level, sink)
	if err != nil {
		return err
	}
	st.log = log
	st.level = level
	if err := setupRuntime(ctx, st, log); err != nil {
		return err
	}
	return serve(ctx, st, log)
}

// installLogger builds the process logger and hands it to the two frameworks
// that keep a process-global registry.
//
// ★ The LevelVar is created here and moves later, once the configuration is
// read. That is why the handler reads a Leveler instead of a fixed level:
// the logger has to exist before setupConfig can log anything, and its
// threshold is not known until setupConfig has run.
// newLogger builds the process logger, its level, and the sink the file
// destination is installed on. It touches no framework: main uses it for the
// early exits, installLogger adds the registrations.
//
// ★ The sink is returned, not rebuilt later. The configuration names a log
//
//	file, but the configuration is read after the logger exists — and the
//	logger cannot be replaced once hertz and eino hold it. So the caller keeps
//	the route and points it at the file when it knows the path.
func newLogger() (*slog.Logger, *slog.LevelVar, *obs.Sink) {
	level := new(slog.LevelVar)
	// The bootstrap level: the environment only, because no file has been
	// read yet. applyLogLevel replaces it with env > file > info.
	if lv, err := obs.LevelFromEnv(); err == nil {
		level.Set(lv)
	}
	sink := obs.NewSink(os.Stderr)
	return obs.New(sink, level), level, sink
}

func installLogger() (*slog.Logger, *slog.LevelVar, *obs.Sink) {
	log, level, sink := newLogger()

	// ★ Both registries are process-global and not safe to change once the
	//   server runs: hlog.SetLogger must precede hserver.New, and eino's
	//   handler list must be in place before the first turn.
	hlog.SetLogger(hertzlog.New(log))
	callbacks.AppendGlobalHandlers(hook.New(log))
	return log, level, sink
}

// applyLogLevel resolves env > server/config.yaml > info and moves the shared
// LevelVar.
func applyLogLevel(log *slog.Logger, level *slog.LevelVar, c *spec.Config) {
	value := ""
	if c != nil {
		value = c.Server.LogLevel
	}
	if env := strings.TrimSpace(os.Getenv(obs.LevelEnv)); env != "" {
		value = env
	}
	parsed, err := obs.ParseLevel(value)
	if err != nil {
		// ★ Warn before Set: if the fallback were error, the warning about
		//   the bad value would be filtered out by the level it explains.
		log.Warn("[main] log level fell back to info", "value", value, "err", err)
	}
	level.Set(parsed)
}

// applyLogFile points the process logger at the configured file and returns
// the rotator, or nil when no file is configured.
//
// ★ A log file that cannot be opened is a **warning**, not a failed start.
//
//	The server's job is to serve, and its log is how the failure would be
//	explained: taking the process down over the destination removes the only
//	place the reason could appear. The process continues on stderr.
//
// ★ The file is created here, 0600, before the rotator sees it. lumberjack
//
//	opens lazily on the first write and creates with 0644 — so neither the
//	permission nor an early failure (a read-only directory, a path that is a
//	directory) would be discovered at startup. The probe makes both visible
//	while there is still someone to tell.
//
// ★ The path is inside the config root, with the rest of the runtime state.
//
//	OPENHERON_LOG_FILE overrides it for one run, the way OPENHERON_LOG_LEVEL
//	does: an operator who has to capture a log should not have to edit the
//	project to do it.
func applyLogFile(log *slog.Logger, sink *obs.Sink, svc *spec.Service, c *spec.Config) io.Closer {
	if c == nil || sink == nil || svc == nil {
		return nil
	}
	cfg := c.Server.Log
	path := cfg.File
	if env := strings.TrimSpace(os.Getenv(obs.FileEnv)); env != "" {
		path = env
	}
	resolved, err := svc.LogFile(path)
	if err != nil {
		log.Warn("[main] the log stays on stderr: the directory could not be created", "path", path, "err", err)
		return nil
	}
	if resolved == "" {
		return nil
	}
	probe, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Warn("[main] the log stays on stderr: the file could not be opened", "path", resolved, "err", err)
		return nil
	}
	_ = probe.Close()

	file := &lumberjack.Logger{
		Filename:   resolved,
		MaxSize:    cfg.MaxSizeMB,
		MaxAge:     cfg.MaxAgeDays,
		MaxBackups: cfg.MaxBackups,
		Compress:   cfg.Compress,
		// Local time: the file is read by a person next to the machine, and
		// hertz's logger prints local time already.
		LocalTime: true,
	}
	var w io.Writer = file
	if cfg.Console {
		w = io.MultiWriter(os.Stderr, file)
	}
	sink.Set(w)
	log.Info("[main] log file", "path", resolved, "console", cfg.Console,
		"max_size_mb", cfg.MaxSizeMB, "max_age_days", cfg.MaxAgeDays)
	return file
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

	log   *slog.Logger
	level *slog.LevelVar
	// logClose is the rotator behind the file destination, when one was
	// configured. It is closed on shutdown so the last buffer flushes and
	// Windows can delete a rotated file.
	logClose io.Closer
}

func setupConfig(flags serverFlags, log *slog.Logger, level *slog.LevelVar, sink *obs.Sink) (*startup, error) {
	cfgSvc, err := spec.NewService(flags.configDir, flags.projectRoot)
	if err != nil {
		return nil, err
	}
	// ★ The configuration is written on the first start, so a fresh clone
	//   starts without a command in front of it.
	//
	//   ★ It has to happen **before** the skills directory is resolved: that
	//     probe looks for the directory on disk, and a tree that was written
	//     a moment ago is the one it has to find.
	//
	//   ★ The line about it goes to the console, not to the log file: the
	//     file's destination is part of the configuration, and the
	//     configuration is what this line is about.
	if err := initConfigIfEmpty(cfgSvc, log); err != nil {
		// ★ Not fatal: a configuration that is there but does not load is a
		//   different thing, and the load below reports it in the way that
		//   fits it.
		log.Warn("[main] the configuration could not be initialized", "err", err)
	}
	// ★ The editor writes <config root>/skills/, and the runtime reads
	//   whatever its own probe resolved. Tell the config layer where that is,
	//   so a skill written into a directory nobody loads is refused instead
	//   of saved in silence.
	if dir, ok := build.ResolveSkillsDir(log); ok {
		cfgSvc.SetSkillsDir(dir)
	}
	warnUnusedHomeConfig(cfgSvc.Root(), log)

	mgr := &agentConfig{}
	// ★ The file destination is installed **once**, on the first configuration
	//   that arrives — which is this callback. server/ is restart-only, so a
	//   reload that changes the path must not silently move the log while the
	//   process keeps running; sync.Once is what makes "once" true even though
	//   the callback fires on every reload.
	var logClose io.Closer
	var logOnce sync.Once
	openLogFile := func(c *spec.Config) {
		logOnce.Do(func() { logClose = applyLogFile(log, sink, cfgSvc, c) })
	}
	reloader, err := spec.NewReloader(cfgSvc, func(c *spec.Config) {
		mgr.set(c)
		// First: the configuration may name a level, and every line below
		// should already obey it.
		applyLogLevel(log, level, c)
		openLogFile(c)
		log.Info("[main] config loaded", "version", short(c.Version))
		for _, d := range c.Deferred() {
			log.Info("[main] config notice", "notice", d)
		}
	})
	if err != nil {
		return nil, configLoadError(cfgSvc, err)
	}
	cfg := mgr.Current()
	applyLogLevel(log, level, cfg)
	// In case the reloader did not call back synchronously.
	openLogFile(cfg)

	// ★ After the level is known, not before: these two lines are the "where
	//   am I" note, and at error level they should be gone like every other
	//   info line. Printing them first would leave a two-line hole in the
	//   threshold that no configuration could close.
	log.Info("[main] openheron", "version", version)
	log.Info("[main] config root",
		"root", cfgSvc.Root(), "projectRoot", cfgSvc.ProjectRoot())

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
		logClose:    logClose,
	}, nil
}

func setupRuntime(ctx context.Context, st *startup, log *slog.Logger) error {
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
	//
	// ★ There is no "a server's tool list changed" hook here any more: the
	//   tool surface is part of the tree's identity, and the builder
	//   compares the surface the servers offer now with the one the tree
	//   was built from. ★ A separate way to announce a change would be a
	//   second way to be wrong about it.
	builder := build.NewBuilder(st.mgr,
		build.WithPaths(st.projectRoot, st.dataDir),
		build.WithConfigService(st.cfgSvc),
		// ★ Without this the builder's logger is obs.Discard(), and its
		//   warnings go nowhere: the MCP servers' lifecycle, "the
		//   configuration on disk does not load", and the skill that is not
		//   in the skills directory.
		build.WithLogger(st.log))
	st.builder = builder

	// ★ The pre-warm: every server some agent mounts is started in the
	//   background as soon as the configuration is known — so the first
	//   turn usually finds them up. ★ It does not wait for them: a
	//   server that is still installing its own package must not decide
	//   whether a turn can start, and the tools it brings join the next
	//   turn.
	st.mgr.setWarm(func(c *spec.Config) { builder.Warm(c) })
	builder.Warm(st.cfg)

	rt := run.New(run.Config{
		AgentBuilder:   builder,
		SessionManager: fm,
		Input:          run.InputConfig{ProjectRoot: st.projectRoot, Workspace: sessionsRoot},
		StartTimeout:   startTimeout(st.cfg),
		// ★ The lease is the ownership rule: it says a run may hold the
		//   session, and it expires when the holder stops making
		//   progress. The turn slot alone cannot say that.
		LeaseArbiter: memarbit.New(),
		LeaseTTL:     leaseTTL(st.cfg),
	})
	fm.SetTeardown(rt)
	st.rt = rt

	// Inspection depends on both stores, so it runs after wiring and before the
	// first request.
	inspectSessions(ctx, st, log)

	if st.cfg.WatchEnabled() {
		go st.reloader.Watch(ctx)
		log.Info("[main] hot reload enabled", "watching", st.cfgSvc.DescribeHotDirs())
	}

	st.srv = api.New(rt, fm, log, hserver.WithHostPorts(st.addr))
	st.srv.SetConfigSource(func() *spec.Config {
		return st.mgr.Current()
	})
	st.srv.SetConfigService(st.cfgSvc)
	st.srv.SetToolsSource(toolsAdapter{b: builder})
	st.srv.SetMCPSource(mcpAdapter{b: builder})
	st.srv.SetContextSource(contextsAdapter{b: builder})
	st.srv.SetCompactorSource(compactorAdapter{b: builder})
	if st.level != nil {
		st.srv.SetLogLevelSource(func() string { return obs.LevelName(st.level.Level()) })
	}
	st.srv.SetStartTimeoutSource(func() time.Duration {
		return startTimeout(st.mgr.Current())
	})
	st.srv.SetPoolSource(func() map[string]any {
		hits, misses, size := builder.Stats()
		return map[string]any{
			"hits": hits, "misses": misses, "size": size,
			"version": configVersion(st.mgr.Current()),
		}
	})
	st.srv.SetSkillsSource(func() []api.SkillView {
		list := build.Skills(context.Background(), st.log)
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

	return setupFrontend(st, log)
}

func inspectSessions(ctx context.Context, st *startup, log *slog.Logger) {
	if h, m, size := st.builder.Stats(); st.cfg.WatchEnabled() {
		log.Info("[main] agent pool",
			"version", configVersion(st.mgr.Current()),
			"entries", size, "hits", h, "misses", m)
	}

	report, err := st.fm.Inspect(ctx)
	if err != nil {
		log.Error("[main] startup inspection failed", "err", err)
		return
	}
	log.Info("[main] inspection",
		"clean", report.Clean, "pending", report.PendingOK, "orphan", report.Orphan,
		"ownerless", report.Ownerless, "skipped", report.SkippedBadJSON)
	if report.Orphan > 0 {
		log.Warn("[main] sessions hold a pending approval with no checkpoint; "+
			"the card stays visible but the round cannot be resumed",
			"count", report.Orphan)
	}
	if report.Ownerless > 0 {
		log.Warn("[main] sessions hold a checkpoint with no pending approval and cannot be resumed",
			"count", report.Ownerless)
	}
}

func setupFrontend(st *startup, log *slog.Logger) error {
	mode := override("FRONTEND_MODE", st.cfg.Server.FrontendMode)
	assets, aerr := fs.Sub(webAssets, "web/dist")

	switch mode {
	case "embed", "":
		if aerr != nil || !webui.Available(assets) {
			log.Warn("[main] frontend disabled: no web/dist build found; run `task web` to embed the UI")
			return nil
		}
		if err := st.srv.SetEmbeddedFrontend(assets, "/"); err != nil {
			return err
		}
		log.Info("[main] frontend: embedded from web/dist")
	case "dir":
		dir := st.cfgSvc.ResolveDir(override("STATIC_DIR", st.cfg.Server.StaticDir))
		if dir == "" {
			log.Warn("[main] frontend_mode=dir but static_dir is empty; set STATIC_DIR")
			return nil
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			log.Warn("[main] static dir does not exist, frontend disabled", "dir", dir)
			return nil
		}
		st.srv.SetStaticDir(dir)
		log.Info("[main] frontend: static dir", "dir", dir)
	default:
		log.Warn("[main] unknown frontend_mode; expected embed or dir", "mode", mode)
	}
	return nil
}

func serve(ctx context.Context, st *startup, log *slog.Logger) error {
	defer func() {
		// ★ After the shutdown lines, not before: the last records belong in
		//   the file too, and a closed rotator would drop them.
		if st.logClose != nil {
			_ = st.logClose.Close()
		}
	}()
	errCh := make(chan error, 1)
	go func() { errCh <- st.srv.Spin() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("[main] shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.rt.Shutdown(shutdownCtx); err != nil {
			log.Error("[main] shutdown failed", "err", err)
		}
		// ★ The MCP clients outlive the snapshot, so something has to end
		//   them: the process is going away, and a stdio child is a
		//   child process of this one.
		if st.builder != nil {
			st.builder.Close()
		}
		return nil
	}
}

// contextsAdapter exposes the builder's context windows to the API layer.
//
// The builder owns the model-ref resolution, so the API layer asks it rather
// than re-deriving which model an agent runs on.
type contextsAdapter struct{ b *build.Builder }

func (a contextsAdapter) Contexts() map[string]contract.AgentContext {
	return a.b.Contexts()
}

// compactorAdapter exposes the builder's summarizers to the API layer.
//
// The summarizer is the agent's own model and the same finalizer the
// middleware uses, so a manual compaction and an automatic one produce the
// same kind of message.
type compactorAdapter struct{ b *build.Builder }

func (a compactorAdapter) Summarizer(ctx context.Context, agentID string) (run.Summarizer, error) {
	return a.b.SummarizerFor(ctx, agentID)
}

// mcpAdapter exposes the builder's MCP projection to the API layer.
//
// The builder owns the projection, and the projection owns the rule that
// matters: the panel derives nothing — every value it shows is one the
// runtime already holds.
type mcpAdapter struct{ b *build.Builder }

func (a mcpAdapter) MCPServers(ctx context.Context, agentID string) []api.MCPServerView {
	views := a.b.MCPServers(ctx, agentID)
	out := make([]api.MCPServerView, 0, len(views))
	for _, v := range views {
		view := api.MCPServerView{
			Name:      v.Name,
			Transport: v.Transport,
			Status:    v.Status,
			Error:     v.Error,
			Tools:     make([]api.MCPToolView, 0, len(v.Tools)),
		}
		for _, t := range v.Tools {
			view.Tools = append(view.Tools, api.MCPToolView{
				Name:        t.Name,
				ExposedName: t.ExposedName,
				Tier:        t.Tier,
				Status:      t.Status,
				Reason:      t.Reason,
			})
		}
		out = append(out, view)
	}
	return out
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

// agentConfig holds the configuration the process runs on.
//
// ★ The configuration **object** is the tree's identity: a reload
//
//	publishes a new one, and the builder compares the object it holds
//	with the one the tree was built from. ★ There is no generation
//	counter here any more — a counter said "everything changed", and
//	the pool's fingerprint says which agents a change reaches.
type agentConfig struct {
	mu  sync.RWMutex
	cfg *spec.Config

	// warm starts the mounted MCP servers in the background.
	//
	// ★ It is a hook because the builder does not exist yet when the
	//   configuration is first loaded — and the pre-warm has to happen
	//   for that first load too, so the first turn usually finds the
	//   servers up.
	warm func(*spec.Config)
}

// setWarm wires the pre-warm.
func (c *agentConfig) setWarm(fn func(*spec.Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warm = fn
}

// Current returns the configuration the process runs on.
//
// ★ It is the whole answer, and that is the point: the tree is built
//
//	from the configuration the manager holds **now**, and a reload
//	publishes a new one.
func (c *agentConfig) Current() *spec.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *agentConfig) set(cfg *spec.Config) {
	c.mu.Lock()
	c.cfg = cfg
	warm := c.warm
	c.mu.Unlock()
	// ★ The pre-warm is outside the lock: it starts child processes, and
	//   a lock held across that would block every reader of the
	//   configuration.
	if warm != nil {
		warm(cfg)
	}
}

// Publish accepts a configuration the builder read itself.
//
// ★ The builder reads the configuration again when the write path marked a
//
//	change, and publishes it here — so that "the next turn uses it" is a
//	promise rather than a race with the watcher's poll. ★ The tree moves
//	with it: the pool's key is a fingerprint now, and the fingerprint is
//	what says which agents the change reaches.
func (c *agentConfig) Publish(cfg *spec.Config) {
	c.set(cfg)
}

// configVersion is the configuration's own version, and "" when there is
// none.
//
// ★ It is the fingerprint of the configuration's **content**, so two
//
//	runs of the same configuration report the same version, and a
//	reload that changed something reports a different one. ★ It says
//	"which configuration" — the tree's identity is the object, and
//	this is the object's name.
func configVersion(cfg *spec.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Version
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

// leaseTTL reads server.lease_ttl.
//
// ★ It falls back to the runtime's default when the config says nothing —
//
//	and `server/` is not hot-reloaded, so a change needs a restart.
func leaseTTL(c *spec.Config) time.Duration {
	if c == nil || c.Server.LeaseTTL <= 0 {
		return run.DefaultLeaseTTL
	}
	return c.Server.LeaseTTL.Duration()
}

func startTimeout(c *spec.Config) time.Duration {
	if c == nil || c.Server.StartTimeout <= 0 {
		return run.DefaultStartTimeout
	}
	return c.Server.StartTimeout.Duration()
}

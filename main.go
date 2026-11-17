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
	"net"
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
	"github.com/ouranoshong/openheron/projects"
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
	if len(os.Args) > 1 && os.Args[1] == "secret" {
		if err := secretCmd(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			boot.Error("openheron secret failed", "err", err)
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
	openheronRoot      string
	configRoot         string
	defaultProjectRoot string
	// legacyProjectRoot is the previous name of defaultProjectRoot.
	legacyProjectRoot string
	// scopeDir is the optional positional argument: the project this process
	// focuses on.
	scopeDir string
}

// The environment variables that override the server's own settings.
//
// ★ They carry the OPENHERON_ prefix: a bare LISTEN_ADDR or STATIC_DIR is a
//
//	name any other tool in the environment may also set, and a variable
//	somebody else owns is a variable that can change this process's
//	behaviour without anybody meaning to.
const (
	listenAddrEnv   = "OPENHERON_LISTEN_ADDR"
	frontendModeEnv = "OPENHERON_FRONTEND_MODE"
	staticDirEnv    = "OPENHERON_STATIC_DIR"
)

func parseServerFlags(args []string) (serverFlags, error) {
	fs := flag.NewFlagSet("openheron", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f serverFlags
	fs.StringVar(&f.openheronRoot, "openheron-root", "",
		"the user-level base directory (default: ~/.openheron; or $OPENHERON_ROOT)")
	fs.StringVar(&f.configRoot, "config-root", "",
		"the config root to read (default: <openheron-root>/configs, created when missing; or $OPENHERON_CONFIG_ROOT)")
	fs.StringVar(&f.defaultProjectRoot, "default-project-root", "",
		"the project root for sessions that belong to no project (default: <openheron-root>/projects/default)")
	fs.StringVar(&f.legacyProjectRoot, "project-root", "",
		"deprecated: the previous name of --default-project-root")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	// ★ The flags come first: Go's flag parser stops at the first argument
	//   that is not a flag, so `openheron . --config-root x` would read the
	//   two flags as two more directories.
	if fs.NArg() > 1 {
		return f, fmt.Errorf("unexpected argument %q; openheron takes one directory, and the flags come first",
			fs.Arg(1))
	}
	if fs.NArg() == 1 {
		f.scopeDir = fs.Arg(0)
	}
	return f, nil
}

func printServerUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron [flags] [dir]")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "The directory is the project this process focuses on. It is also the")
	fmt.Fprintln(stdout, "default project root, unless --default-project-root says otherwise.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  --openheron-root       the user-level base directory (default: ~/.openheron)")
	fmt.Fprintln(stdout, "  --config-root         the config root (llm/ agents/ mcps/ server/ tools/)")
	fmt.Fprintln(stdout, "  --default-project-root the project root for sessions that belong to no project")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Reads exactly one configuration root: --config-root, else $OPENHERON_CONFIG_ROOT,")
	fmt.Fprintln(stdout, "else <openheron-root>/configs (created when missing).")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Other commands: openheron init, openheron secret, openheron kb, openheron version")
}

// resolveOpenheronRoot resolves the user-level base directory.
//
// ★ The flag, else $OPENHERON_ROOT, else ~/.openheron.
//
//	The default is the user's home directory because the base directory
//	holds the three things a person expects to find again on the next
//	machine: the configuration, the state and the log file.
func resolveOpenheronRoot(flagValue string) (string, error) {
	v := strings.TrimSpace(flagValue)
	if v == "" {
		v = strings.TrimSpace(os.Getenv(spec.RootEnv))
	}
	if v == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("resolve the openheron root: no home directory; "+
				"pass --openheron-root or set %s", spec.RootEnv)
		}
		v = filepath.Join(home, ".openheron")
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", fmt.Errorf("resolve the openheron root %q: %w", v, err)
	}
	return abs, nil
}

// overrideEnv reads the environment variable that overrides a configured
// value.
//
// ★ The previous name still works, and it is reported: an environment
//
//	variable that quietly stops working is the hardest kind of mistake to
//	find — the user believes the setting changed, and nothing changed.
func overrideEnv(log *slog.Logger, key, legacyKey, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if legacyKey != "" {
		if v := strings.TrimSpace(os.Getenv(legacyKey)); v != "" {
			if log != nil {
				log.Warn("[main] the environment variable was renamed",
					"old", legacyKey, "new", key)
			}
			return v
		}
	}
	return fallback
}

// commandProjectRoot returns the project root a command runs against.
//
// ★ The working directory, as it always was. A command is a thing a person
//
//	types while standing in a project, and the project they are standing in
//	is the one they mean. ★ The server's default project root is a different
//	question — it answers for a session that belongs to no project, and the
//	server may be serving several projects at once.
func commandProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve the working directory: %w", err)
	}
	return wd, nil
}

// newConfigService builds the configuration service for a command that has
// no server flags.
//
// ★ The openheron root is resolved the way the server resolves it: the
//
//	environment, else the user's home directory. ★ One answer per question —
//	a command that read a different configuration than the server would be a
//	command that lies.
func newConfigService(configRoot, projectRoot string) (*spec.Service, error) {
	home, err := resolveOpenheronRoot("")
	if err != nil {
		return nil, err
	}
	return spec.NewService(configRoot, projectRoot, spec.WithHome(home))
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

// initConfigIfEmpty writes the factory configuration when the process starts
// with the defaulted config root and nothing in it.
//
// ★ Only the **defaulted** root is filled: `--config-root` and
// `$OPENHERON_CONFIG_ROOT` name a root, and a named root that is empty is the
// user's decision, not a gap for us to fill. ★ The test instance names its
// root, so it never has a configuration written under it.
//
// ★ The plan is the one `openheron init` builds — the same templates, the
// same rules — so "the server created it" and "the command created it" are the
// same tree. The model is the provider template's own default
// (deepseek/deepseek-flash) and the api key is read when the configuration
// loads, from the environment or from the keyring, exactly as with
// `openheron init` without flags.
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
		log.Info("[main] the api key is read when the configuration loads",
			"env", plan.EnvHint, "keyring", plan.SecretHint)
	}
	return nil
}

func runServer(flags serverFlags) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log, level, sink := installLogger()
	// ★ The notice channel: the terminal the process was started on, and the
	//   log file when one is installed. The address the service serves on
	//   goes here — see serve.
	notice := obs.NewNotice(sink.NoticeWriter())

	st, err := setupConfig(flags, log, level, sink)
	if err != nil {
		return err
	}
	st.log = log
	st.level = level
	st.notice = notice
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
// ★ The path is inside the openheron root, with the rest of the runtime state.
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
	// ★ The sink keeps the file apart from the log's route: a notice has to
	//   reach the terminal even when the route has left it, and the file
	//   even when the route still covers the terminal.
	sink.Install(file, cfg.Console)
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
	home        string
	projectRoot string
	// scopeDir is the project this process focuses on — the positional
	// argument, empty in the multi-project mode.
	scopeDir string
	// projects is the project registry: the table that says which sessions
	// belong to which project.
	projects *projects.Registry

	log   *slog.Logger
	level *slog.LevelVar
	// notice is the channel the terminal always sees: the address the
	// service serves on, whatever the log's destination is.
	notice *slog.Logger
	// logClose is the rotator behind the file destination, when one was
	// configured. It is closed on shutdown so the last buffer flushes and
	// Windows can delete a rotated file.
	logClose io.Closer
}

func setupConfig(flags serverFlags, log *slog.Logger, level *slog.LevelVar, sink *obs.Sink) (*startup, error) {
	// ★ The openheron root first: it is the base the other two are
	//   measured from, and the config root's default lives inside it.
	home, err := resolveOpenheronRoot(flags.openheronRoot)
	if err != nil {
		return nil, err
	}
	// ★ The default project root. Three sources, in this order: the flag,
	//   the positional directory, and the default inside the openheron root.
	//   The directory is the second one because `openheron .` is the way to
	//   keep the old intuition — "the agent works where I started it" — and
	//   that intuition is worth more than a flag's precedence.
	projectRoot := flags.defaultProjectRoot
	if projectRoot == "" {
		projectRoot = flags.legacyProjectRoot
		if projectRoot != "" {
			log.Warn("[main] --project-root was renamed to --default-project-root; " +
				"the old name still works, but it will go")
		}
	}
	if projectRoot == "" && flags.scopeDir != "" {
		projectRoot = flags.scopeDir
	}
	// ★ The service gets the logger so that the one fact the write path has
	//   to report — the tier table being widened — lands in the log. A
	//   package that cannot log reports it as a notice instead, and a
	//   notice is read by a person looking at the answer, not by an
	//   operator looking for what changed.
	cfgSvc, err := spec.NewService(flags.configRoot, projectRoot,
		spec.WithHome(home), spec.WithServiceLogger(log))
	if err != nil {
		return nil, err
	}
	// ★ The old environment variable names are reported here, once: the
	//   service resolved them and cannot log them.
	for _, d := range cfgSvc.Notices() {
		log.Warn("[main] config notice", "notice", d)
	}
	// ★ The keyring is wired **here** and nowhere else. It is a global OS
	//   resource — the Windows Credential Manager, the macOS Keychain, the
	//   Linux Secret Service — so the one process that reads and writes it
	//   asks for it by name. Everything else (a library user, a test) gets
	//   no store at all and never reaches the developer's credentials.
	cfgSvc.SetSecretStore(spec.KeyringStore())
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
	if dir, ok := build.ResolveSkillsDir(log, cfgSvc.Root()); ok {
		cfgSvc.SetSkillsDir(dir)
	}

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
		"root", cfgSvc.Root(), "home", cfgSvc.Home(),
		"projectRoot", cfgSvc.DefaultProjectRoot())

	// ★ The data directory: the configured value, else the environment. The
	//   environment wins — it is the one that can change without an editor
	//   restart — and it goes through the same resolution, so a relative
	//   value lands in the openheron root like the configured one does.
	dataDirValue := cfg.Server.DataDir
	if env := strings.TrimSpace(os.Getenv(spec.DataDirEnv)); env != "" {
		dataDirValue = env
	} else if env := strings.TrimSpace(os.Getenv("DATA_DIR")); env != "" {
		log.Warn("[main] the environment variable was renamed",
			"old", "DATA_DIR", "new", spec.DataDirEnv)
		dataDirValue = env
	}
	cfgDataDir, err := cfgSvc.DataDir(dataDirValue)
	if err != nil {
		return nil, err
	}

	addr := overrideEnv(log, listenAddrEnv, "LISTEN_ADDR", cfg.Server.ListenAddr)

	// ★ The exposure note is **not** here. A listener beyond the loopback is
	//   reachable by every machine on the network, and the tools this server
	//   drives run shell commands — there is no token and no account, so the
	//   address itself is the whole boundary, and saying so is what makes it
	//   a decision instead of an accident.
	//
	// ★ It must not be a log record: the log's destination is configuration
	//   (`log.file` with `log.console: false` takes the terminal away), and a
	//   warning about being exposed is exactly the line a configuration must
	//   not be able to hide. ⇒ It is a **notice**, said with the address once
	//   the listener is up — see announceExposure.

	return &startup{
		cfgSvc:      cfgSvc,
		mgr:         mgr,
		reloader:    reloader,
		cfg:         cfg,
		addr:        addr,
		dataDir:     cfgDataDir,
		home:        cfgSvc.Home(),
		projectRoot: cfgSvc.DefaultProjectRoot(),
		scopeDir:    flags.scopeDir,
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

	// ★ The project registry: the table that says which sessions belong to
	//   which project. ★ It lives in the **data directory** — the relation
	//   changes, and the configuration is the user's to point anywhere.
	reg, err := projects.Open(projects.Location(st.dataDir))
	if err != nil {
		return err
	}
	st.projects = reg
	// ★ The scope: the project this process focuses on. ★ Naming a
	//   directory is a statement, so the directory is registered — without a
	//   registration the new sessions would have nowhere to belong.
	scopeID := ""
	if st.scopeDir != "" {
		p, err := reg.Ensure(ctx, st.scopeDir)
		if err != nil {
			return err
		}
		scopeID = p.ID
		log.Info("[main] the process focuses on one project", "id", p.ID, "root", p.Root)
	}

	// The filesystem tools take paths, and the agent has no other way to learn
	// where the code lives. It arrives through WithPaths, not the spec.
	//
	// ★ There is no "a server's tool list changed" hook here any more: the
	//   tool surface is part of the tree's identity, and the builder
	//   compares the surface the servers offer now with the one the tree
	//   was built from. ★ A separate way to announce a change would be a
	//   second way to be wrong about it.
	// ★ The round's project root is rendered in **one** place, and the two
	//   readers share it: the prompt the root agent sees, and the block a
	//   sub-agent is given. ★ Two renderings would be two answers, and the
	//   model would pick the one that reads like a rule.
	input := run.InputConfig{ProjectRoot: st.projectRoot, Workspace: sessionsRoot}

	builder := build.NewBuilder(st.mgr,
		build.WithPaths(st.projectRoot, st.dataDir),
		build.WithRoundBlock(input.RoundProjectRootBlock),
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
		Input:          input,
		StartTimeout:   startTimeout(st.cfg),
		// ★ The lease is the ownership rule: it says a run may hold the
		//   session, and it expires when the holder stops making
		//   progress. The turn slot alone cannot say that.
		LeaseArbiter: memarbit.New(),
		LeaseTTL:     leaseTTL(st.cfg),
		// ★ The directory a session works in: the root of the project it
		//   belongs to. ★ It is resolved **at the start of every round**, so
		//   a project that moved between rounds is picked up by the next one
		//   — and the round that is running keeps the root it began with.
		ProjectRootFor: func(ctx context.Context, sessionID string) string {
			root, err := reg.RootOf(ctx, sessionID)
			if err != nil {
				// ★ A table that cannot be read is not a reason to fail the
				//   round: the session works in the default project root,
				//   and the log says why.
				log.Warn("[main] the project table could not be read; the session works in the default project root",
					"session", sessionID, "err", err)
				return ""
			}
			return root
		},
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
	// ★ The guard goes on before the server registers its routes: hertz
	//   wraps the handlers that come after a middleware, and a guard added
	//   later is a guard that never runs.
	st.srv.SetGuard(api.NewGuard(api.LoopbackOnly(st.addr)))
	st.srv.SetConfigSource(func() *spec.Config {
		return st.mgr.Current()
	})
	st.srv.SetConfigService(st.cfgSvc)
	st.srv.SetProjectsRegistry(reg)
	st.srv.SetProjectScope(scopeID)
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
		list := build.Skills(context.Background(), st.log, st.cfgSvc.Root())
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
	mode := overrideEnv(log, frontendModeEnv, "FRONTEND_MODE", st.cfg.Server.FrontendMode)
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
		dir := st.cfgSvc.ResolveDir(overrideEnv(log, staticDirEnv, "STATIC_DIR", st.cfg.Server.StaticDir))
		if dir == "" {
			log.Warn("[main] frontend_mode=dir but static_dir is empty; set the environment variable",
				"env", staticDirEnv)
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
		// ★ The registry holds the lock file open. ★ Windows will not delete
		//   a file that is still open, so it has to go before the process
		//   ends — otherwise the next start finds the table locked.
		if st.projects != nil {
			_ = st.projects.Close()
		}
	}()
	errCh := make(chan error, 1)
	go func() { errCh <- st.srv.Spin() }()

	// ★ The address is announced once the listener is up: an address
	//   printed before the server accepts connections is a promise, not a
	//   fact. A server that failed to start has nothing to announce.
	running, err := waitRunning(ctx, st.srv, errCh)
	if err != nil {
		return err
	}
	if running {
		announceServing(st)
		announceExposure(st)
	}

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

// waitRunning waits until the server is listening.
//
// ★ It ends when the socket is bound, when the server fails to start, or
//
//	when the process is asked to stop — and it reports which of those it
//	was, because the address is only worth announcing in the first case.
func waitRunning(ctx context.Context, srv *api.Server, errCh <-chan error) (bool, error) {
	for !srv.Running() {
		select {
		case err := <-errCh:
			return false, err
		case <-ctx.Done():
			return false, nil
		case <-time.After(20 * time.Millisecond):
		}
	}
	return true, nil
}

// announceServing tells the terminal where the service serves.
//
// ★ It is a notice, not a log record: the log's destination is configuration
//
//	(`log.file` with `log.console: false` takes the terminal away from every
//	record), and the address is the one thing the person who started the
//	process has to be able to read off the terminal.
//
// ★ The address is the configured one — the one the operator wrote down. A
//
//	port of 0 asks the operating system for a free port, and the line would
//	then say `0`: `listen_addr` is a fixed port a person can type.
func announceServing(st *startup) {
	if st == nil || st.notice == nil {
		return
	}
	st.notice.Info("[main] the server serves on", "url", serverURL(st.addr))
}

// announceExposure says what a listener beyond the loopback means.
//
// ★ It is a notice, for the same reason the address is one: the log's
//
//	destination is configuration, and a warning that says "anyone who can
//	reach this port can drive the agent" must not be the thing the
//	configuration hides. ★ The record is a **warning** and the notice
//	channel does not filter it — the notice logger's own threshold is fixed
//	at info, so the line survives `log_level: error` as well.
//
// ★ It is said **with** the address and not before it: the exposure is what
//
//	that address means, the two lines belong together, and both are facts
//	only once the listener is up.
//
// ★ A listener on the loopback is not an exposure, and the line is silent
//
//	then: a warning about nothing is noise, and a warning that always
//	appears is a warning nobody reads.
func announceExposure(st *startup) {
	if st == nil || st.notice == nil {
		return
	}
	if api.LoopbackOnly(st.addr) {
		return
	}
	st.notice.Warn("[main] the server listens beyond the loopback; "+
		"anyone who can reach this port can drive the agent",
		"addr", st.addr)
}

// serverURL renders the listen address as a URL.
//
// ★ ":8080" binds every interface, and "http://:8080" is not something a
//
//	browser opens: the loopback name is what a person means by "the server
//	on this machine".
func serverURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
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

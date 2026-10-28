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

	"openheron/agentkit/build"
	"openheron/agentkit/obs"
	"openheron/agentkit/obs/hook"
	"openheron/agentkit/run"
	"openheron/agentkit/spec"
	"openheron/api"
	"openheron/storage/filestore"
	"openheron/webui"
)

// webAssets 是 web/dist 的构建产物（Vite 输出）。
//
// ★ embed 必须写在仓库根的包里：go:embed 的模式不能含 `..`，且相对
//
//	**本包目录**解析。webui 在 webui/（深一层），写 `web/dist` 指向的是
//	webui/web/dist。所以「嵌入」在 main，「服务」在 webui。
//
// ★ web/dist/.gitkeep 是为了让 go:embed 在没跑过前端构建的开发树上
//
//	也编译得过 —— go:embed 命中不到任何文件会在编译期直接失败，
//	那意味着新 clone 之后连只改 Go 代码都 build 不出来。
//
//go:embed all:web/dist
var webAssets embed.FS

// version 是构建时注入的版本号。
//
// ★ 由 -ldflags "-X main.version=..." 注入（见 Makefile 的 build / release）。
//
//	源码里的默认值是 "dev" —— 让「没注入的构建」一眼能认出来，
//	而不是伪装成一个正式版本号。
var version = "dev"

func main() {
	// ★ 子命令先于服务进程：`openheron kb index` 不该先起一个 hertz server
	//   再在后台干索引。全量 380 块约 9 分钟，而那段时间里端口一直被占着。
	if len(os.Args) > 1 && os.Args[1] == "kb" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := kbCmd(ctx, os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("openheron kb: %v", err)
		}
		return
	}
	// ★ 同理：`openheron init` 建配置不需要端口，也不需要起服务。
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := initCmd(os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("openheron init: %v", err)
		}
		return
	}
	// ★ `openheron version` 也不需要起服务 —— 它是给发布产物看的，
	//   而发布产物恰恰是最不该占着端口的东西。
	//
	//   `--version` / `-v` 一并认：这两个是 Unix 工具的通用约定，
	//   而「打错旗子结果把服务起起来了」是最坏的一种反应。
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

// serverFlags 是主命令的旗标。
type serverFlags struct {
	// configDir 是**配置根**（里面直接有 llm/ agents/ policy/ …）；空 = 自动解析。
	configDir string
	// projectRoot 是**项目根**（agent 的工作目录与相对路径的基准）；空 = 当前目录。
	projectRoot string
}

// parseServerFlags 解析主命令的旗标。
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

// printServerUsage 打印主命令用法。
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

// warnUnusedHomeConfig 在「家目录里有一份 .openheron，但它不是生效的配置根」时说一声。
//
// ★ 取消继承链之后，~/.openheron **不再被读** —— 而那是最容易踩的坑：
//
//	用户以为「配置还在」，实际是「配置没了」，症状是「启动就报没有 main agent」。
//	不阻塞启动（用 --config-dir 指过去可能正是有意的），但必须说一声。
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

	// ★ 启动分三段：**配置期 → 运行期 → 退出期**。
	//
	//	原来的启动函数是 200 多行、23 个分支 —— 「启动失败到底停在哪一步」
	//	要翻整段才看得出来。三段各自能一眼看完，而失败点就在段边界上。
	st, err := setupConfig(flags)
	if err != nil {
		return err
	}
	if err := setupRuntime(ctx, st); err != nil {
		return err
	}
	return serve(ctx, st)
}

// installObserver 装上观测旁路。
//
// ★ 它是「收的那半」：kb 的检索与索引、llm/common 的模型调用
//
//	早就在主动发事件（callbacks.OnStart/OnEnd/OnError），
//	而此前全仓没有任何一处注册接收方 —— 那些事件白发了。
//
//	装上之后**那两处代码一行不改**：这正是旁路范式的前提，
//	发事件的业务代码不该知道谁在收。
//
// ★ 必须在任何图执行之前装：eino 的全局 handler 列表不是并发安全的。
func installObserver() {
	logger := obs.DefaultLogger(os.Stderr)
	obs.SetLogger(logger)
	callbacks.AppendGlobalHandlers(hook.New(logger))
}

// startup 是启动期的产物：配置、运行时装配、HTTP 服务。
//
// ★ 它是一个**结构**而不是一串返回值：启动要产出的东西太多
//
//	（配置服务、代际管理器、两个存储、装配器、运行时、HTTP 服务），
//	用返回值传就是七八个参数，而它们之间有先后依赖。
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

// setupConfig 是**配置期**：读配置、算出路径、接上热更新。
//
// ★ 配置先行：所有路径与参数都来自它，env 只在末尾作覆盖。
//
//	配错了就在这里炸出来，而不是等第一次模型调用。
func setupConfig(flags serverFlags) (*startup, error) {
	cfgSvc, err := spec.NewService(flags.configDir, flags.projectRoot)
	if err != nil {
		return nil, err
	}
	warnUnusedHomeConfig(cfgSvc.Root())

	// ★ 配置只有一个发布者：mgr。builder、/v2/config、/v2/agents 与
	//   start_timeout 都读它 —— 否则热更新时会出现「接口报 A 代、运行跑 B 代」。
	//
	// ★ NewReloader 的首次加载就是第一代；不要先 Load 再 NewReloader
	//   （那会加载两遍，代际从 2 开始）。
	obs.Logger().Info("[main] openheron", "version", version)

	mgr := &agentConfig{}
	reloader, err := spec.NewReloader(cfgSvc, func(c *spec.Config) {
		// ★ 先记下这一代，再打印。装配推迟到下一次 Build —— 失败不会让
		//   进程对外报告一份它其实没在跑的配置。
		mgr.set(c)
		obs.Logger().Info("[main] config loaded", "version", short(c.Version))
		// ★ deferred 必须真的打印 —— 不打印就等于静默忽略。
		//   它既装「配了但本版不兑现」（降级），也装「配了但缺密钥、请求时才会失败」
		//   （提醒）。后者**不影响启动**，只在这里说一声。
		for _, d := range c.Deferred() {
			obs.Logger().Info("[main] config notice", "notice", d)
		}
	})
	if err != nil {
		return nil, err
	}
	cfg, _ := mgr.Current()

	// 运行数据与配置同根（.openheron/state/），.gitignore 一处生效。
	// ★ server.data_dir 真的生效：相对路径按配置目录解析，空串仍是 state/。
	cfgDataDir, err := cfgSvc.DataDir(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}

	return &startup{
		cfgSvc:   cfgSvc,
		mgr:      mgr,
		reloader: reloader,
		cfg:      cfg,
		addr:     override("LISTEN_ADDR", cfg.Server.ListenAddr),
		dataDir:  override("DATA_DIR", cfgDataDir),
		// ★ 相对路径按项目根解析（不是进程 cwd）——见 spec.Service.ResolveDir。
		projectRoot: override("PROJECT_ROOT", cfgSvc.ProjectRoot()),
	}, nil
}

// setupRuntime 是**运行期**：装配运行时、接线接口、巡检、起热更新。
func setupRuntime(ctx context.Context, st *startup) error {
	sessionsRoot := filepath.Join(st.dataDir, "sessions")

	// The session manager needs a teardown hook that only exists once the
	// Runtime does, and the Runtime needs the manager. Break the cycle by
	// constructing the manager first and injecting afterwards.
	//
	// ★ checkpoint 的存取器由**每个会话自己**创建，住在自己的 checkpoints/
	//   子目录里（<sessions>/<id>/checkpoints/）—— 所以删会话目录 = 删全。
	//   老布局（<dataDir>/checkpoints/<id>.ckpt）只读回退。
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
		// ★ checkpoint 的存取器由**会话**提供（storage.Session.
		//	GetCheckPointStore）—— 它归存储层，不归编排层。
		Input: run.InputConfig{ProjectRoot: st.projectRoot, Workspace: sessionsRoot},
		// 没配就用包里的默认常量 —— 两者同值，只是让配置能改它。
		StartTimeout: startTimeout(st.cfg),
	})
	fm.SetTeardown(rt)
	st.rt = rt

	// Inspection depends on both stores, so it runs after wiring and before the
	// first request.
	inspectSessions(ctx, st)

	// ★ 热更新：只有 llm/ agents/ policy/ 参与；server/ 改了要重启。
	//
	//   换配置 ⇒ 管理器换内容 ⇒ 下一次 Build 自然拿新代际 ⇒ 池按代际失效。
	//   失败的换配置**不回调** —— 旧配置继续服务，进程不死。
	if st.cfg.WatchEnabled() {
		go st.reloader.Watch(ctx)
		obs.Logger().Info("[main] hot reload enabled", "watching", st.cfgSvc.DescribeHotDirs())
	}

	st.srv = api.New(rt, fm, hserver.WithHostPorts(st.addr))
	// 自省端点读的是实时配置：llm/ agents/ policy/ 会热更新，捕获一个值
	// 会让 /v2/agents 永远停留在启动那一刻的拓扑。
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
	// ★ 清单来自 agent 包（与装配时**同一个** frontmatter 解析器）——
	//   自己扫目录只拿得到名字，而 description 恰恰是模型选不选它的依据。
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

// inspectSessions 在启动时巡检一遍会话，报出「待批与 checkpoint 对不上」的那些。
//
// ★ 巡检**只报告，不改数据**：待批记录一律留着 —— 界面要继续显示那张卡，
//
//	用户才有得答。悄悄清掉的话，重启一次他的审批就凭空消失了。
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

// setupFrontend 决定界面从哪来。
//
// ★ 界面来源优先级：FRONTEND_MODE=embed > STATIC_DIR > 无。
//
//	embed 是发布形态（单文件）；STATIC_DIR 是开发时指向 web/dist 的
//	过渡手段。两者都要显式校验 —— 静默不生效会让「页面怎么是空的」
//	无从查起。
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

// serve 是**退出期**：跑服务，等它结束或收到信号，然后收摊。
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

// toolsAdapter 把 agent 包的生效结论搬进 api 的结构。
//
// ★ 存在的理由是**不 import agent**：依赖铁律规定 api 只经 agentkit/run 拿装配。
//
//	代价是要在这里翻译一次字段，而它**只搬不判** —— 判定（deny 赢过
//	disabled_tools、兜底 ask）始终只在 agent 包发生。api 那侧拿到的
//	ToolVerdict 是纯数据，所以两份结构可以各自演进而不产生「两份真相」。
type toolsAdapter struct{ b *build.Builder }

// verdictFrom 把一份 [build.ToolVerdict] 搬成 api 侧的那份。
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

// agentConfig 是「当前这一代配置」+ 代际。
//
// ★ 它是配置的**唯一发布者**：builder、/v2/config、/v2/agents 与
// start_timeout 都读它。另起一份会在热更新时出现「接口报 A 代、运行跑 B 代」。
type agentConfig struct {
	mu  sync.RWMutex
	cfg *spec.Config
	gen uint64
}

// Current 返回当前配置与代际。
//
// ★ 配置与代际必须在**同一把锁内成对返回**：分两次取会在配置变更夹在
// 中间时拿到「新配置 + 旧代际」。
func (c *agentConfig) Current() (*spec.Config, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg, c.gen
}

// set 换掉整代。
func (c *agentConfig) set(cfg *spec.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	c.gen++
}

// override 让环境变量压过配置文件 —— 它是一次性覆盖，配置文件才是可提交的管理面。
func override(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// short 截短指纹，只用于日志。
func short(v string) string {
	if len(v) > 12 {
		return v[:12]
	}
	return v
}

// startTimeout 解析启动超时。没配或写坏（已降级）都回落到包默认值。
func startTimeout(c *spec.Config) time.Duration {
	if c == nil || c.Server.StartTimeout <= 0 {
		return run.DefaultStartTimeout
	}
	return c.Server.StartTimeout.Duration()
}

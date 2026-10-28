// Package api is the transport layer: routing, parameter decoding, status
// codes and SSE lifecycle. It holds no business logic — every decision belongs
// to run.
package api

import (
	"context"
	"io/fs"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hserver "github.com/cloudwego/hertz/pkg/app/server"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"

	"openheron/agentkit/run"
	hcfg "openheron/agentkit/spec"
	"openheron/storage"
	"openheron/transport"
	"openheron/webui"
)

// Server wires the routes to the runtime and the session store.
type Server struct {
	rt       *run.Runtime
	sm       storage.SessionManager
	staticFS string

	// embedded 是 go:embed 的 web/dist（v2 React 界面）。
	embedded    fs.FS
	embedPrefix string
	// spa 是 webui 提供的 SPA 兜底处理器。
	spa app.HandlerFunc
	// h is typed as IRoutes so tests can register onto a bare *route.Engine and
	// drive it through ut.PerformRequest without binding a port.
	h route.IRoutes
	// newStream builds the SSE writer for one turn. Overridable in tests (which
	// have no socket); defaults to transport.New.
	newStream func(*app.RequestContext) *transport.SSEStream
	// cfgFn reads the live configuration for the introspection endpoints.
	// A function, not a pointer: openheron hot-reloads llm/, agents/ and policy/,
	// and a captured value would serve stale agent topology after every reload.
	cfgFn ConfigSource
	// cfgSvc is the config Service itself, for /v2/settings — reading needs
	// only the merged Config, writing needs the files behind it.
	cfgSvc *hcfg.Service
	// skillsFn and poolFn are optional diagnostics sources; without them the
	// introspection endpoints simply omit the corresponding block.
	skillsFn func() []SkillView
	poolFn   func() map[string]any
	// toolsFn reports the effective tool table. Optional for the same reason,
	// but with a sharper edge: it is not a diagnostic, it is the answer to
	// "what can this agent actually do", and recomputing it here would be the
	// very duplication this injection exists to prevent.
	toolsFn ToolsSource
	// startTimeoutFn reads server.start_timeout. A function for the same
	// reason as cfgFn: the value is configuration, not a constant.
	startTimeoutFn func() time.Duration
}

// defaultStartTimeout matches the shipped default when no config is wired.
const defaultStartTimeout = 60 * time.Second

func (s *Server) startTimeout() time.Duration {
	if s.startTimeoutFn == nil {
		return defaultStartTimeout
	}
	if d := s.startTimeoutFn(); d > 0 {
		return d
	}
	return defaultStartTimeout
}

// SetStartTimeoutSource wires server.start_timeout into the run path.
func (s *Server) SetStartTimeoutSource(fn func() time.Duration) { s.startTimeoutFn = fn }

// SetSkillsSource wires the skills block of GET /v2/spec.
func (s *Server) SetSkillsSource(fn func() []SkillView) { s.skillsFn = fn }

// SetPoolSource wires the agent-pool block of GET /v2/runtime.
func (s *Server) SetPoolSource(fn func() map[string]any) { s.poolFn = fn }

// New builds the server. Both collaborators are explicit so tests can inject
// fakes without touching the filesystem or a model.
func New(rt *run.Runtime, sm storage.SessionManager, opts ...hertzconfig.Option) *Server {
	return &Server{rt: rt, sm: sm, h: hserver.New(opts...), newStream: transport.New}
}

// SetStaticDir serves the front end from a directory on disk. Empty disables
// it, which is what an embedding application wants.
//
// ★ 开发时它指向 web/dist（Vite 的构建产物）；发布形态用 SetEmbeddedFrontend
// 把同一份产物嵌进二进制，不需要这个目录。
func (s *Server) SetStaticDir(dir string) { s.staticFS = dir }

// SetEmbeddedFrontend serves the React front end from assets, the go:embed
// tree of web/dist. Takes precedence over SetStaticDir.
//
// ★ 两者互斥而不是叠加：staticDir 是磁盘上的目录（开发时是 web/dist），
//
//	assets 是编译进二进制的同一份产物。两个都注册会让「哪一套优先」变成
//	注册顺序的隐式行为 —— 那种依赖只有在改了一行注册代码之后才会暴露，
//	而表现是界面悄悄换了一整套、数据还是 /v2 的。
func (s *Server) SetEmbeddedFrontend(assets fs.FS, prefix string) error {
	handler, err := webui.Handler(assets, prefix)
	if err != nil {
		return err
	}
	s.embedded, s.embedPrefix = assets, prefix
	s.spa = handler
	return nil
}

// Engine exposes the registered routes so tests can drive them directly.
func (s *Server) Engine() route.IRoutes { return s.h }

// Spin starts listening and blocks.
func (s *Server) Spin() error {
	s.registerRoutes()
	return s.h.(*hserver.Hertz).Run()
}

// registerRoutes is split from Spin so a test can exercise routing without
// binding a port.
func (s *Server) registerRoutes() {
	h := s.h
	s.registerV2Routes()

	if s.spa != nil {
		// SPA 兜底挂在 NoRoute 上，而不是通配路由：
		// 真接口先由路由表匹配；只有没匹配上的路径才落到 index.html。
		// 若把兜底写成 h.GET("/*path")，一个拼错的 /v2 接口会拿到一份
		// HTML 而不是 404 JSON，排查会从「接口没通」偏到「前端解析失败」。
		s.h.(*hserver.Hertz).NoRoute(s.spa)
	}

	if s.staticFS != "" && s.embedded == nil {
		// ★ 必须用 StaticFS 显式给 IndexNames，而不是 Static：
		//   Hertz 的 app.FS 默认 IndexNames 为空、GenerateIndexPages=false，
		//   于是请求目录 `/` 会命中 fs.go 的「cannot access directory without
		//   index page」并回 403 —— 首页永远送不出去。
		//   显式声明 index.html 后 `/` 才会返回静态首页。
		h.StaticFS("/", &app.FS{
			Root:       s.staticFS,
			IndexNames: []string{"index.html"},
		})
	}
}

func (s *Server) session(ctx context.Context, c *app.RequestContext) (storage.Session, error) {
	return s.sm.Get(ctx, c.Param("id"))
}

// pendingFromIDs 把一组 interrupt id 变成待批集合。
//
// ★ 只带 id，不带详情：v1 的 A2UI 协议本来就没有详情（chip 上是一句
//
//	格式化过的字符串），而详情是 v2 才有的东西。

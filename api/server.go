// Package api is the transport layer: routing, parameter decoding, status
// codes and SSE lifecycle. It holds no business logic — every decision belongs
// to run.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hserver "github.com/cloudwego/hertz/pkg/app/server"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/run"
	hcfg "github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/transport"
	"github.com/ouranoshong/openheron/webui"
)

// Server wires the routes to the runtime and the session store.
type Server struct {
	rt       *run.Runtime
	sm       storage.SessionManager
	staticFS string

	embedded    fs.FS
	embedPrefix string
	spa         app.HandlerFunc
	// h is typed as IRoutes so tests can register onto a bare *route.Engine and
	// drive it through ut.PerformRequest without binding a port.
	h route.IRoutes
	// newStream builds the SSE writer for one turn. Overridable in tests (which
	// have no socket); defaults to transport.New.
	newStream func(*app.RequestContext) *transport.SSEStream
	// cfgFn reads the live configuration for the introspection endpoints.
	// A function, not a pointer: openheron hot-reloads llm/, agents/, tools/ and
	// mcps/, and a captured value would serve stale agent topology after every reload.
	cfgFn ConfigSource
	// cfgSvc is the config Service itself, for /v2/settings — reading needs
	// only the merged Config, writing needs the files behind it.
	cfgSvc *hcfg.Service
	// skillsFn and poolFn are optional diagnostics sources; without them the
	// introspection endpoints simply omit the corresponding block.
	skillsFn func() []SkillView
	poolFn   func() map[string]any
	// contextsFn reports what each agent's context allows: the model's
	// window and the compaction threshold.
	contextsFn ContextSource
	// toolsFn reports the effective tool table. Optional for the same reason,
	// but with a sharper edge: it is not a diagnostic, it is the answer to
	// "what can this agent actually do", and recomputing it here would be the
	// very duplication this injection exists to prevent.
	toolsFn ToolsSource
	// compactorFn hands out the summarizer of an agent, for the manual
	// compaction. Optional: without it the endpoint reports that no
	// compactor is wired.
	compactorFn CompactorSource
	// mcpFn reports the MCP servers of one agent. Optional for the same
	// reason as the tools source, but with a sharper edge: the view asks
	// the manager to start the servers it reports, so recomputing it here
	// would be the very duplication this injection exists to prevent.
	mcpFn MCPSource
	// startTimeoutFn reads server.start_timeout. A function for the same
	// reason as cfgFn: the value is configuration, not a constant.
	startTimeoutFn func() time.Duration
	// logLevelFn reports the log level in effect. A function because the level
	// can move without the file changing — an env override, or a reload.
	logLevelFn func() string
	// log is the transport's logger. Nil is accepted: tests build a server
	// without one and the lines are dropped.
	log *slog.Logger
}

func (s *Server) logger() *slog.Logger {
	if s == nil || s.log == nil {
		return obs.Discard()
	}
	return s.log
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

// SetLogLevelSource wires the effective log level into GET /v2/config.
func (s *Server) SetLogLevelSource(fn func() string) { s.logLevelFn = fn }

func (s *Server) logLevel() string {
	if s.logLevelFn == nil {
		return ""
	}
	return s.logLevelFn()
}

// SetSkillsSource wires the skills block of GET /v2/spec.
func (s *Server) SetSkillsSource(fn func() []SkillView) { s.skillsFn = fn }

// SetPoolSource wires the agent-pool block of GET /v2/runtime.
func (s *Server) SetPoolSource(fn func() map[string]any) { s.poolFn = fn }

// New builds the server. Both collaborators are explicit so tests can inject
// fakes without touching the filesystem or a model.
func New(rt *run.Runtime, sm storage.SessionManager, log *slog.Logger,
	opts ...hertzconfig.Option) *Server {
	return &Server{rt: rt, sm: sm, log: log, h: hserver.New(opts...), newStream: transport.New}
}

// SetStaticDir serves the front end from a directory on disk. Empty disables
// it, which is what an embedding application wants.
func (s *Server) SetStaticDir(dir string) { s.staticFS = dir }

// SetEmbeddedFrontend serves the React front end from assets, the go:embed
// tree of web/dist. Takes precedence over SetStaticDir.
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
		s.h.(*hserver.Hertz).NoRoute(s.spa)
	}

	if s.staticFS != "" && s.embedded == nil {
		// ★ StaticFS is fasthttp's FS: it keeps the served files open and
		//   reuses them, so a change on disk is invisible until the cache
		//   expires — and on Windows an open handle also blocks deletion.
		//   The default is ten seconds, which is a long time to stare at an
		//   unchanged frontend in dir mode; one second is the compromise.
		//   A non-positive value is normalised back to the default, so this
		//   cannot be switched off (see staticDir in server_test.go for the
		//   consequence in tests).
		h.StaticFS("/", &app.FS{
			Root:          s.staticFS,
			IndexNames:    []string{"index.html"},
			CacheDuration: time.Second,
		})
	}
}

func (s *Server) session(ctx context.Context, c *app.RequestContext) (storage.Session, error) {
	return s.sm.Get(ctx, c.Param("id"))
}

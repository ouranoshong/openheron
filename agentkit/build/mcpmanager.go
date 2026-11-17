package build

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// mcpClient is the MCP client one server is reached through.
type mcpClient = client.MCPClient

// mcpRelistInterval is how old a server's tool list may be before a turn
// re-lists it.
//
// ★ It is a fixed value and not a configuration key: a knob nobody turns
//
//	is a knob nobody tests, and a server's tool list changes on the scale
//	of days. ★ A background ticker was the other option, and it was
//	rejected — it would keep polling servers nobody is using, for ever,
//	and it would need a goroutine with a lifecycle of its own.
const mcpRelistInterval = 30 * time.Second

// mcpManager owns the MCP clients.
//
// ★ One manager owns the clients, not the provider: the provider answers
//
//	"what tools does this snapshot have", and the manager is what a client
//	outlives. A server is one connection, and for stdio one child process.
type mcpManager struct {
	mu      sync.Mutex
	entries map[string]*mcpEntry
	log     *slog.Logger

	// start creates the client of one server.
	//
	// ★ It is a field and not a call to the package function, because the
	//   protocol is not what the manager is about: a test puts a fake in
	//   its place and exercises the lifecycle — the single start, the
	//   restart, the re-list — without a child process.
	//
	// ★ The two contexts are different on purpose. The operation context
	//   bounds the start — connect, initialize, list tools — and the
	//   lifetime context bounds the **child process**. The client outlives
	//   the start that created it, so a child spawned under the start's
	//   deadline would be killed the moment the start returned.
	start func(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg, log *slog.Logger) (mcpClient, error)

	// ctx is the manager's own lifetime: the background starts hang off
	// it, so the process going away takes them with it.
	ctx    context.Context
	cancel context.CancelFunc
}

func newMCPManager(log *slog.Logger) *mcpManager {
	ctx, cancel := context.WithCancel(context.Background())
	if log == nil {
		log = obs.Discard()
	}
	return &mcpManager{
		entries: map[string]*mcpEntry{},
		start:   startMCPClient,
		ctx:     ctx,
		cancel:  cancel,
		log:     log,
	}
}

// Warm starts every mounted server in the background.
//
// ★ The traversal is the point: the servers the tree wants are the ones
// some agent mounts, and that set is the union of the agents' lists
// (spec.Config.MCPServersInUse) — ★ a server nobody mounts is never
// started, not even in the background.
//
// ★ It is **one job**: the traversal decides *what* to start, and each
// start is its own goroutine — three servers that each take a minute must
// not take three minutes.
//
// ★ The warm is not the only way a server starts: a demand (the provider
// or the panel) starts one too, through the same single-flight. That is
// what makes a failed start retry — with the file's own deadline as the
// backoff — without a second mechanism.
func (m *mcpManager) Warm(cfg *spec.Config) {
	if cfg == nil {
		return
	}
	go func() {
		for _, name := range cfg.MCPServersInUse() {
			scfg := cfg.MCPs[name]
			if scfg == nil {
				continue
			}
			m.entry(name).dispatch(scfg, m.start)
		}
	}()
}

func (m *mcpManager) entry(name string) *mcpEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[name]
	if e == nil {
		e = &mcpEntry{name: name, log: m.log, life: m.ctx}
		m.entries[name] = e
	}
	return e
}

// Tools returns the server's client and its own tool names.
//
// ★ The listing is the manager's business, not the snapshot's: a server's
//
//	tools are discovered at runtime, so a snapshot that froze them would
//	need a restart to see a tool the server added yesterday.
//
// ★ **It does not wait.** The start is a background job: a server that is
//
//	still installing its own package must not decide whether a turn can
//	start — that budget belongs to the model. The caller is told the
//	server is on its way (errMCPStarting), and the tools it brings join
//	the **next** turn.
//
// ★ The start is single-flight: the first caller starts the client and
//
//	every other caller finds the same job in flight. Two child processes
//	for one server would be worse than a slow start — the two could
//	report different tool lists, and the tools the model sees would
//	depend on which process answered.
//
// ★ It takes **no context**, and that is the point: the client it hands
//
//	out owns a child process, and a child process may not be owned by a
//	caller that goes away. The caller is a turn, and a turn ends.
func (m *mcpManager) Tools(cfg *decl.MCPServerCfg) (mcpClient, []string, error) {
	e := m.entry(cfg.Name)
	e.dispatch(cfg, m.start)
	return e.view()
}

// callMutex returns the mutex that serializes one stdio server's calls.
func (m *mcpManager) callMutex(name string) *sync.Mutex {
	return &m.entry(name).callMu
}

// markBroken returns the hook that closes a client whose child died.
func (m *mcpManager) markBroken(name string) func() {
	e := m.entry(name)
	return func() { e.markBroken() }
}

// Refresh re-lists the servers whose listing is older than the interval.
//
// ★ It reports whether a listing changed. ★ That answer is no longer
//
//	used to move anything — the tree's identity carries the tool surface,
//	and the builder compares it (see surfaceHash). ★ The report is the
//	manager's own answer to "did anything move", and it is what the
//	manager's own tests pin down: a fresh listing is not re-listed, and
//	a changed one is.
func (m *mcpManager) Refresh(ctx context.Context, cfg *spec.Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	for _, name := range cfg.MCPServersInUse() {
		scfg := cfg.MCPs[name]
		if scfg == nil {
			continue
		}
		e := m.entry(name)
		if !e.stale() {
			continue
		}
		prev := e.lastTools()
		names, err := e.list(ctx)
		if err != nil {
			// ★ A server that does not answer is not a changed tool list.
			continue
		}
		if !sameStrings(prev, names) {
			changed = true
		}
	}
	return changed
}

// ToolsOf reports the live tool names of one server.
//
// ★ The list is **live**: a server that is still starting offers no tools,
//
//	and one that comes up offers its own. ★ That is why the list is part
//	of an agent's fingerprint and not only of its file — the agent's tool
//	surface changes when a server comes up, and the agent has to be
//	rebuilt for it.
//
// ★ It does **not** start a server: the dependency walk asks what the
//
//	server offers, and a walk that started servers would be a walk that
//	changed the world it was measuring.
func (m *mcpManager) ToolsOf(server string) []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	e := m.entries[server]
	m.mu.Unlock()
	if e == nil {
		return nil
	}
	return e.lastTools()
}

// surfaceHash is what the mounted servers offer right now.
//
// ★ It is part of the tree's identity: the tool set is built once per
//
//	snapshot, and a server that comes up offers tools the tree does not
//	have yet. ★ A generation used to say "the surface changed"; this says
//	**what** it is, and the tree can be compared with it.
//
// ★ The listing is the **mounted** servers — the ones some agent lists.
//
//	A server nobody mounts is never started, and one that was unmounted
//	keeps its client but loses its claim on the tree: the tree's tools
//	do not carry it any more, and the tree must not be held hostage by
//	it.
//
// ★ The tool names are sorted, because the order the server hands them
//
//	in is not the order it will hand them in next time.
func (m *mcpManager) surfaceHash(cfg *spec.Config) string {
	if m == nil || cfg == nil {
		return ""
	}
	var sb strings.Builder
	for _, name := range cfg.MCPServersInUse() {
		tools := m.ToolsOf(name)
		sort.Strings(tools)
		sb.WriteString(name)
		sb.WriteByte('=')
		sb.WriteString(strings.Join(tools, ","))
		sb.WriteByte(';')
	}
	return sb.String()
}

// Close tears every client down.
//
// ★ The client outlives the snapshot, so something has to end it: the
//
//	process is going away.
func (m *mcpManager) Close() {
	// ★ The manager's own lifetime ends first: the background starts hang
	//   off it, and a process that goes away must not leave a child
	//   behind.
	m.cancel()
	m.mu.Lock()
	entries := make([]*mcpEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.entries = map[string]*mcpEntry{}
	m.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
}

// mcpEntry is one server's client and its state.
type mcpEntry struct {
	name string
	log  *slog.Logger

	// life is the context that bounds the child process.
	//
	// ★ It is the **manager's** lifetime, and never the caller's: the
	//
	//	caller is a turn (or a panel request), and a caller that goes
	//	away would take the child with it — leaving a tool that
	//	answers "file already closed" for the rest of the session.
	life context.Context

	// mu guards the entry's own state.
	mu          sync.Mutex
	fingerprint string
	cli         mcpClient
	tools       []string
	listedAt    time.Time
	err         error
	serial      bool

	// state is where the server is in its own lifecycle.
	//
	// ★ It is a field and not a pair of booleans, because the four
	//
	//	states answer different questions: "not up yet" and "up and
	//	broken" are different facts, and a reader who cannot tell them
	//	apart reads both as "unreachable" — which is a verdict about the
	//	server, not about a moment in its life.
	state mcpState

	// lastAttempt is when the last start was tried.
	//
	// ★ It is the backoff's clock: a server that just failed is not
	//	asked again until the file's own deadline has passed, so a
	//	server that cannot start is not spawned in a loop.
	lastAttempt time.Time

	// cancel ends the child's lifetime. It is the entry's, because the
	// client outlives the snapshot it was built for.
	//
	// ★ It is stored **when the start begins**, not when it succeeds: a
	//   start that is in flight has a child process too, and a process
	//   that goes away must not leave it behind.
	cancel context.CancelFunc

	// startMu makes the start single-flight.
	startMu sync.Mutex

	// callMu serializes the calls of a stdio server.
	callMu sync.Mutex
}

// mcpState is where one server is in its own lifecycle.
type mcpState string

const (
	// mcpNotStarted: declared, and nobody has asked for it yet.
	mcpNotStarted mcpState = "not_started"
	// mcpStarting: a start is in flight.
	mcpStarting mcpState = "starting"
	// mcpReady: the client is up and the listing is in hand.
	mcpReady mcpState = "ready"
	// mcpFailed: the last start failed, and the reason is kept.
	mcpFailed mcpState = "failed"
)

// errMCPStarting says the server is on its way.
//
// ★ It is not a failure: a caller that treats it as one warns about a
//
//	server that is doing exactly what it was asked to do.
var errMCPStarting = errors.New("the server is starting")

// errMCPNotStarted says nobody has asked for the server yet.
var errMCPNotStarted = errors.New("the server is not started")

// mcpStartTimeout is the ceiling for one start.
//
// ★ It is the file's own deadline, and a file that names none gets the
//
//	default — the same default the loader applies
//	(decl.DefaultMCPTimeout). ★ The backoff reuses it, so a server that
//	cannot start is not asked again before the deadline it was given
//	has passed.
func mcpStartTimeout(cfg *decl.MCPServerCfg) time.Duration {
	if cfg == nil {
		return decl.DefaultMCPTimeout
	}
	if cfg.Timeout.Value > 0 {
		return cfg.Timeout.Value
	}
	return decl.DefaultMCPTimeout
}

func (e *mcpEntry) client() mcpClient {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cli
}

func (e *mcpEntry) lastTools() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.tools...)
}

// view is the entry's state as a caller sees it.
//
// ★ The client comes with the state: a caller that got a client for a
//
//	server that is not ready would call a server that is not there.
func (e *mcpEntry) view() (mcpClient, []string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.state {
	case mcpReady:
		return e.cli, append([]string(nil), e.tools...), nil
	case mcpStarting:
		return nil, append([]string(nil), e.tools...), errMCPStarting
	case mcpFailed:
		return nil, append([]string(nil), e.tools...), e.err
	default:
		return nil, nil, errMCPNotStarted
	}
}

// stale reports whether the listing is old enough to be re-listed.
func (e *mcpEntry) stale() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli == nil {
		return false
	}
	return time.Since(e.listedAt) >= mcpRelistInterval
}

// dispatch starts the client if it is not up, and returns at once.
//
// ★ The start is a **background job**: the caller never waits for it. A
//
//	server that is still installing its own package must not decide
//	whether a turn can start — that budget belongs to the model.
//
// ★ The decision is single-flight: the first caller starts it, every
//
//	other caller finds the same job in flight and goes on. Two child
//	processes for one server would be worse than a slow start.
//
// ★ A changed declaration means a changed server: the old client is
//
//	stale, and a hot partition whose resources are not rebuilt lies about
//	being hot.
//
// ★ It takes **no context** either, and that is the trap it exists to
//
//	remove: the child's life used to come from the caller, and the
//	caller is a turn. A turn that ends cancelled the context, the
//	child died with it, and every later call to that server's tools
//	answered "transport error: failed to write request" — a tool that
//	was still offered, and could not be called.
func (e *mcpEntry) dispatch(cfg *decl.MCPServerCfg,
	start func(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg, log *slog.Logger) (mcpClient, error)) {

	e.startMu.Lock()
	defer e.startMu.Unlock()

	want := mcpFingerprint(cfg)
	e.mu.Lock()
	cli, fp, state, last := e.cli, e.fingerprint, e.state, e.lastAttempt
	e.mu.Unlock()

	if cli != nil && fp == want && state == mcpReady {
		return
	}
	if state == mcpStarting {
		return
	}
	if state == mcpFailed && time.Since(last) < mcpStartTimeout(cfg) {
		// ★ The backoff is the file's own deadline: a server that just
		//   failed to start is not asked again before that deadline has
		//   passed — so a server that cannot start is not spawned in a
		//   loop, and one that can start is not given up on.
		return
	}
	if cli != nil {
		e.close()
	}

	// ★ The child's life is the **manager's**, not the caller's: the
	//   child ends when the entry is closed, when it breaks, or when
	//   the process goes away — and not one moment earlier.
	life, cancel := context.WithCancel(e.life)
	e.mu.Lock()
	e.cancel = cancel
	e.state = mcpStarting
	e.err = nil
	e.mu.Unlock()

	e.log.Info("[mcp] the server is starting",
		"server", cfg.Name, "transport", cfg.Transport)

	go e.runStart(life, cancel, cfg, want, start)
}

// runStart is the background job: it starts the client, lists its tools,
// and records what came of it.
//
// ★ The listing is part of the job, not a separate step: a server that is
//
//	up but not listed yet is a server whose tools nobody can name, and the
//	job is what makes both true at once.
//
// ★ The comparison is against the listing **before** the job, because the
//
//	question is whether the surface changed — and the surface is the
//	tool list the model sees. ★ A server that comes up with tools it did
//	not have is exactly the change that has to move the generation.
func (e *mcpEntry) runStart(life context.Context, cancel context.CancelFunc,
	cfg *decl.MCPServerCfg, want string,
	start func(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg, log *slog.Logger) (mcpClient, error)) {

	opCtx, opCancel := context.WithTimeout(life, mcpStartTimeout(cfg))
	defer opCancel()

	cli, err := start(opCtx, life, cfg, e.log)
	var names []string
	if err == nil {
		names, err = e.listClient(opCtx, cli, cfg.Transport == decl.MCPTransportStdio)
	}

	e.mu.Lock()
	if err != nil {
		e.state, e.err, e.lastAttempt = mcpFailed, err, time.Now()
		e.cli, e.tools, e.listedAt = nil, nil, time.Time{}
		// ★ The client is gone, so its tools are gone with it — and the
		//   tree's tool surface with them. ★ The manager does not have to
		//   announce that: the tree compares the surface it was built
		//   from with the one the servers offer now.
		e.cancel = nil
	} else {
		e.state, e.err, e.lastAttempt = mcpReady, nil, time.Now()
		e.cli, e.tools, e.listedAt = cli, names, time.Now()
		e.fingerprint, e.serial = want, cfg.Transport == decl.MCPTransportStdio
	}
	e.mu.Unlock()

	if err != nil {
		// ★ The child is over: a failed start leaves no process behind.
		cancel()
		e.log.Warn("[mcp] the server did not start",
			"server", cfg.Name, "transport", cfg.Transport, "err", err)
		return
	}

	e.log.Info("[mcp] the server is up",
		"server", cfg.Name, "transport", cfg.Transport, "tools", len(names))
}

// list reads the server's own tool names.
//
// ★ For a stdio server the read takes the same lock the calls take: the
//
//	transport writes to the child's stdin, and a listing interleaved with
//	a call is a corrupted frame.
func (e *mcpEntry) list(ctx context.Context) ([]string, error) {
	e.mu.Lock()
	cli := e.cli
	serial := e.serial
	e.mu.Unlock()
	if cli == nil {
		return nil, errors.New("the server is not started")
	}
	names, err := e.listClient(ctx, cli, serial)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.tools, e.listedAt = names, time.Now()
	e.mu.Unlock()
	return names, nil
}

// listClient reads the tool names of one client.
//
// ★ The client is a parameter because the background job lists **before**
//
//	the client is the entry's: a server that is up but not listed yet is a
//	server whose tools nobody can name.
func (e *mcpEntry) listClient(ctx context.Context, cli mcpClient, serial bool) ([]string, error) {
	if cli == nil {
		return nil, errors.New("the server is not started")
	}
	if serial {
		e.callMu.Lock()
		defer e.callMu.Unlock()
	}
	res, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Tools))
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

// markBroken closes a client whose child died.
//
// ★ A crash is not a configuration error, and a dead server that stays
//
//	dead is the worst outcome: the next call starts it again.
func (e *mcpEntry) markBroken() {
	e.mu.Lock()
	cli := e.cli
	cancel := e.cancel
	e.cli, e.cancel, e.tools, e.listedAt = nil, nil, nil, time.Time{}
	// ★ The server is not "ready" any more, and it is not "failed" either:
	//   nobody asked it to do anything and it died — so it is back to
	//   "not started", and the next demand starts it again.
	e.state = mcpNotStarted
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cli != nil {
		_ = cli.Close()
	}
}

// close tears the client down.
//
// ★ The child's lifetime ends with the client: a stdio server is a child
//
//	process of this one, and it must not outlive the connection that owns
//	it.
func (e *mcpEntry) close() {
	e.mu.Lock()
	cli := e.cli
	cancel := e.cancel
	e.cli, e.cancel, e.tools, e.listedAt = nil, nil, nil, time.Time{}
	// ★ A closed server is not "ready", and it is not "failed" either: it
	//   is one nobody has asked for yet.
	e.state = mcpNotStarted
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cli != nil {
		_ = cli.Close()
	}
}

// mcpFingerprint is the identity of one server's declaration.
//
// ★ Two declarations with the same fingerprint describe the same server,
//
//	so the client stays; a different one means the server changed and the
//	client has to be rebuilt.
func mcpFingerprint(cfg *decl.MCPServerCfg) string {
	if cfg == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(cfg.Name)
	b.WriteString("\x00")
	b.WriteString(cfg.Transport)
	b.WriteString("\x00")
	b.WriteString(cfg.Command)
	b.WriteString("\x00")
	b.WriteString(strings.Join(cfg.Args, "\x01"))
	b.WriteString("\x00")
	b.WriteString(cfg.URL)
	b.WriteString("\x00")
	for _, k := range sortedMapKeys(cfg.Env) {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(cfg.Env[k])
		b.WriteString("\x01")
	}
	b.WriteString("\x00")
	for _, k := range sortedMapKeys(cfg.Headers) {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(cfg.Headers[k])
		b.WriteString("\x01")
	}
	b.WriteString("\x00")
	b.WriteString(cfg.Timeout.Value.String())
	b.WriteString("\x00")
	b.WriteString(cfg.CallTimeout.Value.String())
	return b.String()
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── the clients ─────────────────────────────────────────────────────────

// startMCPClient connects to one server and initializes the session.
//
// ★ The initialization result is discardable: the provider only needs the
//
//	tool list. A failure to initialize is the same failure as an
//	unreachable server — the server does not contribute tools, and it says
//	so.
func startMCPClient(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg, log *slog.Logger) (mcpClient, error) {
	switch cfg.Transport {
	case decl.MCPTransportStdio:
		return startStdioClient(opCtx, lifeCtx, cfg, log)
	case decl.MCPTransportHTTP:
		return startHTTPClient(opCtx, cfg)
	default:
		return nil, fmt.Errorf("unknown transport %q", cfg.Transport)
	}
}

// mcpTeardownGrace is how long the teardown of a **failed** server may take
// before the caller stops waiting for it.
//
// ★ It is not a timeout on the server: the server's own deadline is the
//
//	file's `timeout:` (the ceiling for connect, initialize and list tools).
//	This one bounds the **corpse** — how long a child that will not answer may
//	keep the teardown waiting. ★ A build waits for a server, not for a
//	server's corpse: a child that is still installing its own package does
//	not exit because its stdin closed, and a teardown that waits for it
//	waits for ever.
const mcpTeardownGrace = 5 * time.Second

// startStdioClient starts the server as a child process.
//
// ★ The child inherits this process's environment, with the file's env:
//
//	layered on top: the same name, the configuration wins. ★ That
//	inheritance is what makes the credential path work without
//	configuration — the sandbox runtime puts a sentinel into the
//	container's variable, and the MCP server reads it from there.
//
// ★ The command is resolved in the sandbox's PATH, never against the
//
//	project root: the workspace must not get to choose what runs.
//
// mcpCommandLogger puts mcp-go's stdio transport back on the process logger.
//
// ★ Left alone, the library calls log.Default() and writes straight to
//
//	stderr: those lines ignore log_level, miss log.file and our sink, and a
//	normal shutdown prints "ERROR: Error reading from stdout" once per
//	stdio child. This adapter implements the library's tiny Infof/Errorf
//	interface, so no import of its util package is needed.
type mcpCommandLogger struct{ log *slog.Logger }

func newMCPCommandLogger(log *slog.Logger) mcpCommandLogger {
	if log == nil {
		log = obs.Discard()
	}
	return mcpCommandLogger{log: log}
}

func (l mcpCommandLogger) Infof(format string, v ...any) {
	l.log.Debug("[mcp] " + fmt.Sprintf(format, v...))
}

func (l mcpCommandLogger) Errorf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	// ★ readResponses is blocked in ReadString when Close closes done and
	//   cmd.Wait closes the stdout pipe. The library treats the resulting
	//   os.ErrClosed as a transport failure; in fact the reader is simply
	//   stopping because we closed it. A genuine failure reports EOF
	//   (which the library already ignores) or a different error, so only
	//   the exact os.ErrClosed wording is downgraded — real errors stay
	//   Errors.
	if strings.HasPrefix(msg, "Error reading from stdout:") &&
		strings.Contains(msg, "file already closed") {
		l.log.Debug("[mcp] stdio reader stopped on teardown", "detail", msg)
		return
	}
	l.log.Error("[mcp] " + msg)
}

func startStdioClient(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg, log *slog.Logger) (mcpClient, error) {
	env := mcpChildEnv(cfg)

	opCtx, cancel := context.WithTimeout(opCtx, mcpStartTimeout(cfg))
	defer cancel()

	// ★ The child is spawned under the **lifetime** context, not under the
	//   start's deadline: the client outlives the start that created it,
	//   and a child bound to the start would be killed the moment the
	//   start returned.
	//
	//   ★ The handle to the child is kept, because the failure path below
	//   has to end it: the teardown waits for the child to exit, and a
	//   child that will not answer may not exit on its own.
	var child *exec.Cmd
	tr := transport.NewStdioWithOptions(cfg.Command, env, cfg.Args,
		transport.WithCommandFunc(func(ctx context.Context, command string, _ []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = env
			child = cmd
			return cmd, nil
		}),
		transport.WithCommandLogger(newMCPCommandLogger(log)))
	if err := tr.Start(lifeCtx); err != nil {
		return nil, fmt.Errorf("start %q: %w", cfg.Command, err)
	}
	cli := client.NewClient(tr)
	if err := initializeMCPClient(opCtx, cli); err != nil {
		// ★ A server that will not answer must not hold the build.
		//
		//	The teardown waits for the child to exit, and a child that is
		//	still installing its own package does not exit because its
		//	stdin closed — so the wait would last as long as the
		//	install, and the turn would die with a start timeout that
		//	names neither the server nor the reason. The child is
		//	killed first, and the teardown is bounded anyway: a build
		//	waits for a server, not for a server's corpse.
		//
		//	★ The server does not contribute tools and the build goes on
		//	without it — the caller reports it as unreachable, and the
		//	tools it would have brought are simply not there.
		killMCPChild(child, log)
		closeMCPClient(tr, log)
		return nil, err
	}
	return cli, nil
}

// killMCPChild ends a child that outlived its own start.
//
// ★ The kill is what makes the teardown finish: closing the pipes asks the
//
//	child to leave, and a child that is busy installing has no reason to
//	look at its stdin.
func killMCPChild(cmd *exec.Cmd, log *slog.Logger) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil {
		log.Warn("[mcp] the child did not die",
			"pid", cmd.Process.Pid, "err", err)
	}
}

// closeMCPClient tears a client down without waiting for ever.
//
// ★ The close runs in the background and the wait is bounded: the child is
//
//	already dead, so the wait is short — but a child that ignores the kill
//	must not turn a failed server into a failed turn.
func closeMCPClient(tr *transport.Stdio, log *slog.Logger) {
	done := make(chan struct{})
	go func() {
		_ = tr.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(mcpTeardownGrace):
		log.Warn("[mcp] the teardown did not finish in time",
			"grace", mcpTeardownGrace.String())
	}
}

// mcpChildEnv assembles the child's environment.
//
// ★ The child inherits this process's environment, with the file's env:
//
//	layered on top: the same name, the configuration wins. ★ That
//	inheritance is what makes the credential path work without
//	configuration — the sandbox runtime puts a sentinel into the
//	container's variable, and the MCP server reads it from there.
//
// ★ The PATH is the inherited one, unchanged: the command is resolved in
//
//	the sandbox's PATH and never against the project root, so a checkout
//	cannot decide which executable runs.
func mcpChildEnv(cfg *decl.MCPServerCfg) []string {
	env := os.Environ()
	for _, k := range sortedMapKeys(cfg.Env) {
		env = append(env, k+"="+cfg.Env[k])
	}
	return env
}

// startHTTPClient opens the session with a streamable HTTP server.
//
// ★ Streamable HTTP and only that: mcp-go ships an SSE client as well, and
//
//	a transport the configuration cannot ask for is a transport that would
//	have to be maintained without ever being used.
func startHTTPClient(ctx context.Context, cfg *decl.MCPServerCfg) (mcpClient, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpStartTimeout(cfg))
	defer cancel()

	tr, err := transport.NewStreamableHTTP(cfg.URL, transport.WithHTTPHeaders(cfg.Headers))
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", cfg.URL, err)
	}
	if err := tr.Start(ctx); err != nil {
		return nil, fmt.Errorf("open %q: %w", cfg.URL, err)
	}
	cli := client.NewClient(tr)
	if err := initializeMCPClient(ctx, cli); err != nil {
		_ = tr.Close()
		return nil, err
	}
	return cli, nil
}

// initializeMCPClient performs the protocol handshake.
func initializeMCPClient(ctx context.Context, cli mcpClient) error {
	_, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo:      mcp.Implementation{Name: "openheron", Version: "1"},
		},
	})
	if err != nil {
		return fmt.Errorf("initialize the server: %w", err)
	}
	return nil
}

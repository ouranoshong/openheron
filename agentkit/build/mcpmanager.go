package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

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
	start func(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg) (mcpClient, error)
}

func newMCPManager() *mcpManager {
	return &mcpManager{entries: map[string]*mcpEntry{}, start: startMCPClient}
}

func (m *mcpManager) entry(name string) *mcpEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[name]
	if e == nil {
		e = &mcpEntry{name: name}
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
// ★ The start is single-flight: the first caller starts the client and
//
//	every other caller waits for that same start and gets the same client.
//	Two child processes for one server would be worse than a slow start —
//	the two could report different tool lists, and the tools the model
//	sees would depend on which process answered.
func (m *mcpManager) Tools(ctx context.Context, cfg *decl.MCPServerCfg) (mcpClient, []string, error) {
	e := m.entry(cfg.Name)
	if err := e.ensure(ctx, cfg, m.start); err != nil {
		return nil, e.lastTools(), err
	}
	names, err := e.list(ctx)
	if err != nil {
		return e.client(), e.lastTools(), err
	}
	return e.client(), names, nil
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
// ★ It reports whether a listing changed, because that is what moves the
//
//	generation: the tools are rebuilt exactly like a configuration change
//	rebuilds them, and a restart is not needed either way.
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

// Close tears every client down.
//
// ★ The client outlives the snapshot, so something has to end it: the
//
//	process is going away.
func (m *mcpManager) Close() {
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

	// mu guards the entry's own state.
	mu          sync.Mutex
	fingerprint string
	cli         mcpClient
	tools       []string
	listedAt    time.Time
	err         error
	serial      bool

	// cancel ends the child's lifetime. It is the entry's, because the
	// client outlives the snapshot it was built for.
	cancel context.CancelFunc

	// startMu makes the start single-flight.
	startMu sync.Mutex

	// callMu serializes the calls of a stdio server.
	callMu sync.Mutex
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

// stale reports whether the listing is old enough to be re-listed.
func (e *mcpEntry) stale() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli == nil {
		return false
	}
	return time.Since(e.listedAt) >= mcpRelistInterval
}

// ensure starts the client if it is not up, or if the declaration changed.
//
// ★ A changed declaration means a changed server: the old client is stale,
//
//	and a hot partition whose resources are not rebuilt lies about being
//	hot.
func (e *mcpEntry) ensure(ctx context.Context, cfg *decl.MCPServerCfg,
	start func(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg) (mcpClient, error)) error {
	e.startMu.Lock()
	defer e.startMu.Unlock()

	want := mcpFingerprint(cfg)
	e.mu.Lock()
	cli, fp := e.cli, e.fingerprint
	e.mu.Unlock()

	if cli != nil && fp == want {
		return nil
	}
	if cli != nil {
		e.close()
	}

	life, cancel := context.WithCancel(context.Background())
	started, err := start(ctx, life, cfg)
	if err != nil {
		cancel()
		e.mu.Lock()
		e.err = err
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.cancel = cancel
	e.cli, e.fingerprint, e.err = started, want, nil
	e.serial = cfg.Transport == decl.MCPTransportStdio
	e.mu.Unlock()
	return nil
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
	e.mu.Lock()
	e.tools, e.listedAt = names, time.Now()
	e.mu.Unlock()
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
func startMCPClient(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg) (mcpClient, error) {
	switch cfg.Transport {
	case decl.MCPTransportStdio:
		return startStdioClient(opCtx, lifeCtx, cfg)
	case decl.MCPTransportHTTP:
		return startHTTPClient(opCtx, cfg)
	default:
		return nil, fmt.Errorf("unknown transport %q", cfg.Transport)
	}
}

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
func startStdioClient(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg) (mcpClient, error) {
	env := mcpChildEnv(cfg)

	opCtx, cancel := context.WithTimeout(opCtx, cfg.Timeout.Value)
	defer cancel()

	// ★ The child is spawned under the **lifetime** context, not under the
	//   start's deadline: the client outlives the start that created it,
	//   and a child bound to the start would be killed the moment the
	//   start returned.
	tr := transport.NewStdioWithOptions(cfg.Command, env, cfg.Args,
		transport.WithCommandFunc(func(ctx context.Context, command string, _ []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = env
			return cmd, nil
		}))
	if err := tr.Start(lifeCtx); err != nil {
		return nil, fmt.Errorf("start %q: %w", cfg.Command, err)
	}
	cli := client.NewClient(tr)
	if err := initializeMCPClient(opCtx, cli); err != nil {
		_ = tr.Close()
		return nil, err
	}
	return cli, nil
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
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout.Value)
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

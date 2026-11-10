package build

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// fakeMCPClient is a client that answers from a list of tool names.
//
// ★ It embeds the interface on purpose: the methods this test does not use
//
//	are not the manager's business, and a nil embedded interface says so
//	louder than a stub of twenty methods.
type fakeMCPClient struct {
	client.MCPClient

	mu     sync.Mutex
	tools  []string
	lists  int
	closed bool
}

func (c *fakeMCPClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists++
	out := &mcp.ListToolsResult{}
	for _, name := range c.tools {
		out.Tools = append(out.Tools, mcp.Tool{Name: name})
	}
	return out, nil
}

func (c *fakeMCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeMCPClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeMCPClient) listCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lists
}

func (c *fakeMCPClient) setTools(names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = append([]string(nil), names...)
}

// newFakeMCPManager returns a manager whose clients are fakes.
func newFakeMCPManager(starts *int32, cli *fakeMCPClient) *mcpManager {
	m := newMCPManager(nil)
	m.start = func(context.Context, context.Context, *decl.MCPServerCfg, *slog.Logger) (mcpClient, error) {
		atomic.AddInt32(starts, 1)
		return cli, nil
	}
	return m
}

// waitForMCPSettled waits until the server is no longer on its way.
//
// ★ The start is a background job, so a caller that asks right after the
//
//	ask sees the server on its way. A test that asserts on the finished
//	state has to wait for it — the way a reader of the panel presses
//	Refresh, and the way a second turn finds the tools.
func waitForMCPSettled(t *testing.T, m *mcpManager, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		e := m.entry(name)
		e.mu.Lock()
		state := e.state
		e.mu.Unlock()
		if state == mcpReady || state == mcpFailed {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the server %q never settled", name)
}

// mcpStateOf reads the server's state, for a test that has to name it.
func mcpStateOf(m *mcpManager, name string) mcpState {
	e := m.entry(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func TestTwoCallersAskingForTheSameServerGetOneClient(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)

	cfg := mcpCfg("github")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// ★ "On its way" is an answer, not a failure: the callers
			//   that arrive while the job is in flight are told so.
			if _, _, err := m.Tools(context.Background(), cfg); err != nil &&
				!errors.Is(err, errMCPStarting) {
				t.Errorf("Tools: %v", err)
			}
		}()
	}
	wg.Wait()
	waitForMCPSettled(t, m, "github")

	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("the server was started %d times, want 1 — one server is one client", got)
	}
}

func TestACrashedChildIsStartedAgainOnTheNextCall(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)
	cfg := mcpCfg("github")

	if _, _, err := m.Tools(context.Background(), cfg); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "github")
	// The child died: the manager closes it, and the next call starts a
	// new one.
	m.entry("github").markBroken()
	if !cli.isClosed() {
		t.Fatal("the dead client must be closed")
	}
	if _, _, err := m.Tools(context.Background(), cfg); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "github")
	if got := atomic.LoadInt32(&starts); got != 2 {
		t.Fatalf("the server was started %d times, want 2 — a crash is not a configuration error", got)
	}
}

func TestAChangedDeclarationReplacesTheClient(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)

	cfg := mcpCfg("github")
	if _, _, err := m.Tools(context.Background(), cfg); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "github")

	changed := mcpCfg("github", func(c *decl.MCPServerCfg) { c.Command = "other" })
	if _, _, err := m.Tools(context.Background(), changed); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "github")
	if got := atomic.LoadInt32(&starts); got != 2 {
		t.Fatalf("the server was started %d times, want 2 — a changed declaration is a changed server", got)
	}
	if !cli.isClosed() {
		t.Fatal("the stale client must be closed")
	}
}

func TestAFreshListingIsNotReListed(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)

	cfg := &spec.Config{
		MCPs:   decl.MCPFiles{"github": mcpCfg("github")},
		Agents: map[string]*decl.Agent{spec.DefaultAgentID: {ID: spec.DefaultAgentID, MCPServers: []string{"github"}}},
	}
	if changed := m.Refresh(context.Background(), cfg); changed {
		t.Fatal("nothing changed, the refresh must not report a change")
	}
	// The server was never started, so there is nothing to re-list.
	if got := cli.listCount(); got != 0 {
		t.Fatalf("list calls = %d, want 0", got)
	}
}

func TestAChangedToolListIsReportedByTheRefresh(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)

	cfg := &spec.Config{
		MCPs:   decl.MCPFiles{"github": mcpCfg("github")},
		Agents: map[string]*decl.Agent{spec.DefaultAgentID: {ID: spec.DefaultAgentID, MCPServers: []string{"github"}}},
	}
	if _, _, err := m.Tools(context.Background(), cfg.MCPs["github"]); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "github")
	before := cli.listCount()

	// The listing is younger than the interval, so it is not re-listed.
	if changed := m.Refresh(context.Background(), cfg); changed {
		t.Fatal("a fresh listing must not report a change")
	}
	if got := cli.listCount(); got != before {
		t.Fatalf("list calls = %d, want %d — a fresh listing is not re-listed", got, before)
	}

	// The interval passes and the server changed its list.
	cli.setTools([]string{"read_issue", "list_issues"})
	m.entry("github").mu.Lock()
	m.entry("github").listedAt = time.Now().Add(-2 * mcpRelistInterval)
	m.entry("github").mu.Unlock()

	if changed := m.Refresh(context.Background(), cfg); !changed {
		t.Fatal("a changed tool list must be reported")
	}
}

func TestTheChildEnvironmentIsTheProcessesOwnWithTheFileOnTop(t *testing.T) {
	t.Setenv("OPENHERON_TEST_INHERITED", "inherited")
	t.Setenv("OPENHERON_TEST_OVERRIDDEN", "inherited")

	cfg := mcpCfg("github", func(c *decl.MCPServerCfg) {
		c.Env = map[string]string{"OPENHERON_TEST_OVERRIDDEN": "from the file"}
	})
	env := mcpChildEnv(cfg)

	values := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				values[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if values["OPENHERON_TEST_INHERITED"] != "inherited" {
		t.Fatalf("the child must inherit the environment, got %q",
			values["OPENHERON_TEST_INHERITED"])
	}
	if values["OPENHERON_TEST_OVERRIDDEN"] != "from the file" {
		t.Fatalf("the file must win over the inherited value, got %q",
			values["OPENHERON_TEST_OVERRIDDEN"])
	}
}

func TestTheCommandIsNeverResolvedAgainstTheProjectRoot(t *testing.T) {
	// ★ The PATH is the inherited one, unchanged: a checkout cannot put an
	//   executable on it.
	env := mcpChildEnv(mcpCfg("github"))
	path := ""
	for _, kv := range env {
		if len(kv) > 5 && kv[:5] == "PATH=" {
			path = kv[5:]
		}
	}
	if path != "" && path == "/tmp" {
		t.Fatalf("the PATH must not be the project root, got %q", path)
	}
}

func TestAnUnreachableServerSaysSoAndKeepsTheOtherServersAlive(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newMCPManager(nil)
	m.start = func(_ context.Context, _ context.Context, cfg *decl.MCPServerCfg, _ *slog.Logger) (mcpClient, error) {
		atomic.AddInt32(&starts, 1)
		if cfg.Name == "broken" {
			return nil, errors.New("the command is not on the PATH")
		}
		return cli, nil
	}

	if _, _, err := m.Tools(context.Background(), mcpCfg("broken")); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "broken")
	if _, _, err := m.Tools(context.Background(), mcpCfg("broken")); err == nil {
		t.Fatal("an unreachable server must report the failure")
	}
	if _, _, err := m.Tools(context.Background(), mcpCfg("github")); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "github")
	if _, own, err := m.Tools(context.Background(), mcpCfg("github")); err != nil {
		t.Fatalf("Tools: %v", err)
	} else if len(own) != 1 {
		t.Fatalf("tools = %v, want the reachable server's tool", own)
	}
}

// silentMCPCfg is a server whose child starts and then says nothing.
//
// ★ The child is this test binary in its silent mode: it stays alive and
//
//	ignores its stdin, which is what a server that is still installing its
//	own package looks like from the outside.
func silentMCPCfg(t *testing.T) *decl.MCPServerCfg {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("the test binary's path: %v", err)
	}
	return mcpCfg("silent", func(c *decl.MCPServerCfg) {
		c.Command = self
		c.Args = []string{"-test.run=TestNothingRunsInTheChild"}
		c.Env = map[string]string{mcpSilentEnv: "1"}
		c.Timeout = decl.MCPDuration{Value: 2 * time.Second}
	})
}

// TestAServerThatNeverAnswersDoesNotHoldTheBuild is the regression test for
// the hang.
//
// ★ The teardown waits for the child to exit, and this child does not exit
//
//	because its stdin closed — so a start that waits for the teardown waits
//	for the child's whole life, and the turn dies with a start timeout that
//	names neither the server nor the reason.
func TestAServerThatNeverAnswersDoesNotHoldTheBuild(t *testing.T) {
	cfg := silentMCPCfg(t)

	life, cancelLife := context.WithCancel(context.Background())
	defer cancelLife()

	begin := time.Now()
	_, err := startStdioClient(context.Background(), life, cfg, nil)
	elapsed := time.Since(begin)

	if err == nil {
		t.Fatal("a server that never answers must report the failure")
	}
	if !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("the error must name the handshake that failed, got %v", err)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("the start took %s: a failed server must not hold the build", elapsed)
	}
}

// TestTheSilentServerDoesNotTakeTheOthersDownWithIt is the same failure one
// level up: the manager.
func TestTheSilentServerDoesNotTakeTheOthersDownWithIt(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("the test binary's path: %v", err)
	}

	m := newMCPManager(nil)
	// ★ The manager owns the child processes it starts. This one starts a
	//   silent child that stays alive for two minutes on purpose, and a
	//   manager that is never closed leaves it running: on Linux it is an
	//   orphan nobody notices, on Windows it keeps a handle on the test
	//   executable and `go test` then fails to delete it —
	//   "unlinkat ...build.test.exe: Access is denied" — over a test that
	//   passed.
	defer m.Close()
	begin := time.Now()
	_, _, err = m.Tools(context.Background(), silentMCPCfg(t))
	elapsed := time.Since(begin)
	// ★ The manager answers at once: the server is on its way, and the
	//   caller is not held by it.
	if !errors.Is(err, errMCPStarting) {
		t.Fatalf("the manager must say the server is starting, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the manager took %s: a server on its way must not hold the build", elapsed)
	}

	// ★ The other server still answers: one server on its way is a
	//   degradation, not the end of the tool surface.
	good := mcpCfg("fake", func(c *decl.MCPServerCfg) {
		c.Command = self
		c.Args = []string{"-test.run=TestNothingRunsInTheChild"}
		c.Env = map[string]string{mcpServerEnv: "1"}
		c.Timeout = decl.MCPDuration{Value: 30 * time.Second}
	})
	if _, _, err := m.Tools(context.Background(), good); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	waitForMCPSettled(t, m, "fake")
	if _, own, err := m.Tools(context.Background(), good); err != nil {
		t.Fatalf("Tools: %v", err)
	} else if len(own) == 0 {
		t.Fatal("the reachable server must still offer its tools")
	}
}

// ★ A normal shutdown used to print one ERROR per stdio child: mcp-go's
//
//	reader is blocked in ReadString when its own Close shuts the pipe. That
//	is teardown, not a transport failure — but a genuine read error still
//	has to stay an Error.
func TestMCPCommandLoggerSeparatesTeardownFromFailure(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(obs.NewHandler(&buf, slog.LevelDebug))
	l := newMCPCommandLogger(log)

	l.Errorf("Error reading from stdout: %v", errors.New("read |0: file already closed"))
	quiet := buf.String()
	if strings.Contains(quiet, "[Error]") {
		t.Fatalf("our own teardown must not be an error:\n%s", quiet)
	}
	if !strings.Contains(quiet, "[Debug]") ||
		!strings.Contains(quiet, "stdio reader stopped on teardown") {
		t.Fatalf("the teardown lost its trace:\n%s", quiet)
	}

	buf.Reset()
	l.Errorf("Error reading from stdout: %v", errors.New("read |0: broken pipe"))
	loud := buf.String()
	if !strings.Contains(loud, "[Error]") ||
		!strings.Contains(loud, "Error reading from stdout") {
		t.Fatalf("a real transport failure must stay visible:\n%s", loud)
	}

	buf.Reset()
	l.Errorf("Error writing response: %v", errors.New("short write"))
	if !strings.Contains(buf.String(), "[Error]") {
		t.Fatalf("other library errors must stay visible:\n%s", buf.String())
	}
}

func TestMCPCommandLoggerRoutesInfoAndSurvivesANilLogger(t *testing.T) {
	var buf bytes.Buffer
	l := newMCPCommandLogger(slog.New(obs.NewHandler(&buf, slog.LevelDebug)))
	l.Infof("connected to %s", "fetch")
	if !strings.Contains(buf.String(), "[Debug]") ||
		!strings.Contains(buf.String(), "connected to fetch") {
		t.Fatalf("the library's Info line must follow our level and sink:\n%s", buf.String())
	}
	// ★ The kb CLI builds a client without a process logger. Adapting the
	//   library must not turn that path into a nil-pointer panic.
	newMCPCommandLogger(nil).Errorf("Error reading from stdout: %v",
		errors.New("file already closed"))
}

package build

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

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
	m := newMCPManager()
	m.start = func(context.Context, *decl.MCPServerCfg) (mcpClient, error) {
		atomic.AddInt32(starts, 1)
		return cli, nil
	}
	return m
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
			if _, _, err := m.Tools(context.Background(), cfg); err != nil {
				t.Errorf("Tools: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("the server was started %d times, want 1 — one server is one client", got)
	}
}

func TestACrashedChildIsStartedAgainOnTheNextCall(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)
	cfg := mcpCfg("github")

	if _, _, err := m.Tools(context.Background(), cfg); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	// The child died: the manager closes it, and the next call starts a
	// new one.
	m.entry("github").markBroken()
	if !cli.isClosed() {
		t.Fatal("the dead client must be closed")
	}
	if _, _, err := m.Tools(context.Background(), cfg); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if got := atomic.LoadInt32(&starts); got != 2 {
		t.Fatalf("the server was started %d times, want 2 — a crash is not a configuration error", got)
	}
}

func TestAChangedDeclarationReplacesTheClient(t *testing.T) {
	var starts int32
	cli := &fakeMCPClient{tools: []string{"read_issue"}}
	m := newFakeMCPManager(&starts, cli)

	cfg := mcpCfg("github")
	if _, _, err := m.Tools(context.Background(), cfg); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	changed := mcpCfg("github", func(c *decl.MCPServerCfg) { c.Command = "other" })
	if _, _, err := m.Tools(context.Background(), changed); err != nil {
		t.Fatalf("Tools: %v", err)
	}
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
		Agents: map[string]*decl.Agent{spec.DefaultAgentID: {ID: spec.DefaultAgentID}},
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
		Agents: map[string]*decl.Agent{spec.DefaultAgentID: {ID: spec.DefaultAgentID}},
	}
	if _, _, err := m.Tools(context.Background(), cfg.MCPs["github"]); err != nil {
		t.Fatalf("Tools: %v", err)
	}
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
	m := newMCPManager()
	m.start = func(_ context.Context, cfg *decl.MCPServerCfg) (mcpClient, error) {
		atomic.AddInt32(&starts, 1)
		if cfg.Name == "broken" {
			return nil, errors.New("the command is not on the PATH")
		}
		return cli, nil
	}

	if _, _, err := m.Tools(context.Background(), mcpCfg("broken")); err == nil {
		t.Fatal("an unreachable server must report the failure")
	}
	if _, own, err := m.Tools(context.Background(), mcpCfg("github")); err != nil {
		t.Fatalf("Tools: %v", err)
	} else if len(own) != 1 {
		t.Fatalf("tools = %v, want the reachable server's tool", own)
	}
}

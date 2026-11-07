package build

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

func TestNetToolsAreOffByDefault(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	env := toolEnv{cfg: cfg}
	ctx := context.Background()

	for _, p := range []toolProvider{
		httpRequestToolProvider{},
		webSearchToolProvider{},
		wikipediaToolProvider{},
	} {
		tools, err := p.Provide(ctx, env)
		if err != nil {
			t.Fatalf("%s: %v", p.ID(), err)
		}
		if len(tools) != 0 {
			t.Fatalf("%s must be off by default, got %d tools", p.ID(), len(tools))
		}
	}
}

func TestHTTPToolsDefaultToGetOnly(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{}

	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("want only the GET tool by default, got %d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "request_get" {
		t.Fatalf("the only tool must be request_get, got %q", info.Name)
	}
}

func TestHTTPToolsHonorTheMethodAllowlist(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Methods: []string{"get", "post"}}

	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, info.Name)
	}
	if len(names) != 2 || names[0] != "request_get" || names[1] != "request_post" {
		t.Fatalf("want [request_get request_post], got %v", names)
	}
}

// ── SSRF ──────────────────────────────────────────────────────────────────

func TestHTTPToolRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer srv.Close()

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runGetTool(t, tools, srv.URL)
	if err == nil {
		t.Fatalf("the loopback request must be refused, got %q", out)
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("the error must say why, got %v", err)
	}
}

func TestPublicOnlyClientRejectsPrivateAddresses(t *testing.T) {
	for _, ip := range []string{
		"127.0.0.1",
		"10.0.0.5",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.0.1", // CGNAT
		"0.0.0.0",
	} {
		if isPublicIP(net.ParseIP(ip)) {
			t.Fatalf("%s must not be considered public", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34"} {
		if !isPublicIP(net.ParseIP(ip)) {
			t.Fatalf("%s must be considered public", ip)
		}
	}
}

func TestToolOutputIsCapped(t *testing.T) {
	big := strings.Repeat("x", defaultToolOutputBytes*2)
	capped := capTool(fakeInvokable{out: big}, defaultToolOutputBytes)

	got, err := capped.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > defaultToolOutputBytes+100 {
		t.Fatalf("the output must be capped, got %d bytes", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("the truncation must be visible to the model, got %q", got[:80])
	}
}

func TestToolOutputUncappedWhenSmall(t *testing.T) {
	capped := capTool(fakeInvokable{out: "small"}, defaultToolOutputBytes)
	got, err := capped.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "small" {
		t.Fatalf("got %q, want small", got)
	}
}

func TestEnabledToolsAreAllowedByDefault(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{}
	cfg.Tools.WebSearch = &decl.WebSearchCfg{}
	cfg.Tools.Wikipedia = &decl.WikipediaCfg{}

	snap := mustSnapshot(t, newTestBuilderWithConfig(t, cfg))
	// ★ An enabled tool is **not** an allowed one: the default is ask, and
	//   the file that provides the tool names its exceptions.
	for _, name := range []string{
		decl.ToolNameHTTPGet,
		decl.ToolNameWebSearch,
		decl.ToolNameWikipedia,
	} {
		if got := snap.agentSpec.policy.TierOf(name); got != TierAsk {
			t.Fatalf("%s tier = %q, want ask", name, got)
		}
	}
}

// ★ The tier table is assembled from the configuration: a tool nobody
//
//	named asks, and a tool its own file names runs without a nod.
func TestTheFilesOwnExceptionsWinOverTheDefault(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.WebSearch = &decl.WebSearchCfg{}
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Allows: []string{decl.ToolNameHTTPGet}}

	snap := mustSnapshot(t, newTestBuilderWithConfig(t, cfg))
	if got := snap.agentSpec.policy.TierOf(decl.ToolNameHTTPGet); got != TierAllow {
		t.Fatalf("the file names it, so it must run without a nod, got %q", got)
	}
	if got := snap.agentSpec.policy.TierOf(decl.ToolNameWebSearch); got != TierAsk {
		t.Fatalf("nobody named it, so it must ask, got %q", got)
	}
}

func TestDisabledToolsStillRemoveTheEnabledTools(t *testing.T) {
	cfg := configFromSpecs("test/model", []string{decl.ToolNameWebSearch}, nil, nil)
	cfg.Tools.WebSearch = &decl.WebSearchCfg{}

	snap := mustSnapshot(t, newTestBuilderWithConfig(t, cfg))
	table := snap.tableFor(spec.DefaultAgentID)
	if table == nil {
		t.Fatal("the default agent must have a table")
	}
	if got := table.Blocked(decl.ToolNameWebSearch); got != LayerDisable {
		t.Fatalf("the agent's disabled_tools must still remove it, got %q", got)
	}
}

func TestUnknownWebSearchProviderIsRejected(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.WebSearch = &decl.WebSearchCfg{Provider: "binggo"}

	_, err := webSearchToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err == nil {
		t.Fatal("an unknown provider must be rejected")
	}
	if !strings.Contains(err.Error(), "binggo") {
		t.Fatalf("the error must name the provider, got %v", err)
	}
}

func TestNetToolNamesDoNotCollideWithReservedNames(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Methods: []string{"get", "post", "put", "delete"}}
	cfg.Tools.WebSearch = &decl.WebSearchCfg{}
	cfg.Tools.Wikipedia = &decl.WikipediaCfg{}

	reg := &toolRegistry{providers: []toolProvider{
		httpRequestToolProvider{},
		webSearchToolProvider{},
		wikipediaToolProvider{},
	}}
	tools, err := reg.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the registry must accept these tools: %v", err)
	}
	seen := map[string]bool{}
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if seen[info.Name] {
			t.Fatalf("duplicate tool %q", info.Name)
		}
		seen[info.Name] = true
		if owner, ok := reservedToolNames[info.Name]; ok {
			t.Fatalf("tool %q collides with %s", info.Name, owner)
		}
	}
	if len(tools) != 6 {
		t.Fatalf("want 4 http + 1 search + 1 wiki = 6 tools, got %d", len(tools))
	}
}

type fakeInvokable struct {
	out string
}

func (f fakeInvokable) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "fake"}, nil
}

func (f fakeInvokable) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return f.out, nil
}

func runGetTool(t *testing.T, tools []tool.BaseTool, url string) (string, error) {
	t.Helper()
	if len(tools) == 0 {
		t.Fatal("no tools")
	}
	inv, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("the tool is not invokable")
	}
	args, _ := json.Marshal(map[string]string{"url": url})
	return inv.InvokableRun(context.Background(), string(args))
}

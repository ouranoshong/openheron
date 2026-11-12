package build

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// ★ The proxy is where the request goes: the tool hands the target to the
//
//	proxy and never resolves it itself. The target here does not exist —
//	that is the point: without the proxy this request cannot even be
//	attempted.
func TestTheHTTPToolGoesThroughTheConfiguredProxy(t *testing.T) {
	var gotHost, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		w.Write([]byte("through the proxy"))
	}))
	defer srv.Close()

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: srv.URL}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runGetTool(t, tools, "http://example.invalid/thing")
	if err != nil {
		t.Fatalf("the request must go through the proxy: %v", err)
	}
	if out != "through the proxy" {
		t.Fatalf("the proxy's answer must come back, got %q", out)
	}
	if gotHost != "example.invalid" {
		t.Fatalf("the proxy must receive the target host, got %q", gotHost)
	}
	if gotPath != "/thing" {
		t.Fatalf("the proxy must receive the target path, got %q", gotPath)
	}
}

// ★ The proxy **replaces** the public-address rule, it does not add to it:
//
//	a target the proxy can reach is often one the guard would refuse —
//	an intranet host is not public and is what the proxy is for.
func TestTheProxyReplacesThePublicAddressRule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: srv.URL}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	// The same target is refused without a proxy (see the test above).
	out, err := runGetTool(t, tools, "http://127.0.0.1:1/secret")
	if err != nil {
		t.Fatalf("a proxy takes over the boundary, the tool must not refuse: %v", err)
	}
	if out != "ok" {
		t.Fatalf("got %q, want ok", out)
	}
}

// ★ The search has a proxy of its own, and it follows the same rule: a
//
//	wrong one is refused when the provider is built, and a good one
//	reaches the transport.
func TestTheWebSearchProxyReachesTheTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.WebSearch = &decl.WebSearchCfg{Provider: "duckduckgo", Proxy: srv.URL}
	if _, err := (webSearchToolProvider{}).Provide(context.Background(), toolEnv{cfg: cfg}); err != nil {
		t.Fatalf("the provider must accept the proxy: %v", err)
	}

	cli, err := newPublicOnlyClient(defaultWebSearchTimeout, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := cli.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the transport is %T", cli.Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("a configured proxy must reach the transport")
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Host != strings.TrimPrefix(srv.URL, "http://") {
		t.Fatalf("the transport must use the configured proxy, got %v", u)
	}
}

// ★ Without a proxy there is no proxy: the transport must not reach for
//
//	one on its own, because a client that picks up the environment's
//	proxy is a client whose route the configuration does not describe.
func TestTheTransportHasNoProxyWithoutAConfiguredOne(t *testing.T) {
	cli, err := newPublicOnlyClient(defaultHTTPTimeout, "")
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := cli.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the transport is %T", cli.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("no proxy is configured, so the transport must not have one")
	}
	if tr.DialContext == nil {
		t.Fatal("the direct route must keep its own dialer")
	}
}

// ★ A wrong proxy is refused when the tool is built, so the answer names
//
//	the setting instead of arriving as a failed request later.
func TestAnInvalidProxyIsRejected(t *testing.T) {
	for _, raw := range []string{"127.0.0.1:7890", "ftp://127.0.0.1:21", "http://"} {
		cfg := configFromSpecs("test/model", nil, nil, nil)
		cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: raw}
		_, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
		if err == nil {
			t.Fatalf("the proxy %q must be rejected", raw)
		}
		if !strings.Contains(err.Error(), "proxy") {
			t.Fatalf("the error must name the proxy, got %v", err)
		}
	}
}

// ★ The whole chain: the file on disk, the configuration it loads into,
//
//	the tool it assembles, and the request that leaves the machine —
//	through the proxy the file named.
func TestTheProxyInTheFileReachesTheRequest(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Write([]byte("through the proxy"))
	}))
	defer proxy.Close()

	root := t.TempDir()
	cfgRoot := filepath.Join(root, ".openheron")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(cfgRoot, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_LLM_KEY", "sk-test")
	write("llm/openai.yaml",
		"type: openai_compat\nbase_url: https://api.example.com\napi_key: ${TEST_LLM_KEY}\nmodels:\n  - id: fast\n")
	write("agents/main.md", "---\nmodel: openai/fast\n---\nbody\n")
	write("tools/http.yaml", "methods: [get]\nproxy: "+proxy.URL+"\n")

	svc, err := spec.NewService(cfgRoot, root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.HTTP == nil || cfg.Tools.HTTP.Proxy != proxy.URL {
		t.Fatalf("the file's proxy must survive the load, got %+v", cfg.Tools.HTTP)
	}

	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runGetTool(t, tools, "http://example.invalid/through")
	if err != nil {
		t.Fatalf("the request must go through the proxy: %v", err)
	}
	if out != "through the proxy" {
		t.Fatalf("the proxy's answer must come back, got %q", out)
	}
	if gotHost != "example.invalid" {
		t.Fatalf("the proxy must receive the target host, got %q", gotHost)
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

// ★ The agent's own tools: list decides what it may use — a provider's
//
//	tool is reachable only when the agent names it.
func TestTheToolsListDecidesWhetherTheEnabledToolsAreReachable(t *testing.T) {
	cfg := configFromSpecs("test/model", allToolsExcept(decl.ToolNameWebSearch), nil, nil)
	cfg.Tools.WebSearch = &decl.WebSearchCfg{}

	snap := mustSnapshot(t, newTestBuilderWithConfig(t, cfg))
	table := snap.tableFor(spec.DefaultAgentID)
	if table == nil {
		t.Fatal("the default agent must have a table")
	}
	if got := table.Blocked(decl.ToolNameWebSearch); got != LayerNotLoaded {
		t.Fatalf("the agent's tools list must decide it, got %q", got)
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

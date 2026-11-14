package build

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

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

	cli, err := newNetworkClient(defaultWebSearchTimeout, srv.URL)
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
	cli, err := newNetworkClient(defaultHTTPTimeout, "")
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
	t.Setenv("LLM_OPENAI_API_KEY", "sk-test")
	write("llm/openai.yaml",
		"type: openai_compat\nbase_url: https://api.example.com\nmodels:\n  - id: fast\n")
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

// ── the environment's proxy ───────────────────────────────────────────────

// setProxyEnv puts the environment's proxy settings under the test's
// control.
//
// ★ The machine this runs on may export a proxy of its own, and a test
//
//	that means "there is no proxy in the environment" has to say so —
//	otherwise it is testing the machine, not the code.
//
// ★ Both spellings are set, to the same value. On Windows the lowercase
//
//	name **is** the uppercase one, and on Linux the uppercase one is
//	read first — so either way the test decides, not the machine.
func setProxyEnv(t *testing.T, httpProxy, noProxy string) {
	t.Helper()
	t.Setenv("HTTP_PROXY", httpProxy)
	t.Setenv("http_proxy", httpProxy)
	t.Setenv("HTTPS_PROXY", httpProxy)
	t.Setenv("https_proxy", httpProxy)
	t.Setenv("NO_PROXY", noProxy)
	t.Setenv("no_proxy", noProxy)
	// ★ A CGI environment refuses the environment's proxy outright, and
	//   that is a rule about the process, not about the route.
	t.Setenv("REQUEST_METHOD", "")
}

// ★ "env" hands the route to the environment: the request goes to the
//
//	proxy HTTP_PROXY names, and the proxy never resolves the target.
func TestTheEnvProxyTakesTheRequest(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Write([]byte("through the environment"))
	}))
	defer proxy.Close()
	setProxyEnv(t, proxy.URL, "")

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: "env"}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runGetTool(t, tools, "http://example.invalid/thing")
	if err != nil {
		t.Fatalf("the request must go through the environment's proxy: %v", err)
	}
	if out != "through the environment" {
		t.Fatalf("the proxy's answer must come back, got %q", out)
	}
	if gotHost != "example.invalid" {
		t.Fatalf("the proxy must receive the target host, got %q", gotHost)
	}
}

// ★ NO_PROXY takes a host back out of the proxy: the tool dials that host
//
//	itself, and the public-address rule applies to it.
func TestNoProxyTakesTheHostOutOfTheProxy(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Write([]byte("through the environment"))
	}))
	defer proxy.Close()
	setProxyEnv(t, proxy.URL, "example.invalid")

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: "env"}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGetTool(t, tools, "http://example.invalid/thing"); err == nil {
		t.Fatal("a direct request to a name that does not resolve must fail")
	}
	if gotHost != "" {
		t.Fatalf("the proxy must not have seen the request, got host %q", gotHost)
	}
}

// ★ A target the environment exempts from the proxy is dialed by the tool
//
//	itself — and then the guard is the guard: the request is refused.
func TestATargetTheEnvironmentExemptsIsStillGuarded(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("through the environment"))
	}))
	defer proxy.Close()
	setProxyEnv(t, proxy.URL, "")

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: "env"}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	// The loopback is never proxied: the environment's own rules say so.
	_, err = runGetTool(t, tools, "http://127.0.0.1:1/secret")
	if err == nil {
		t.Fatal("a private target must be refused")
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("the error must say why, got %v", err)
	}
}

// ★ A named proxy wins over the environment: the file says which proxy,
//
//	and the environment does not get a vote.
func TestANamedProxyWinsOverTheEnvironment(t *testing.T) {
	var envGotHost string
	envProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envGotHost = r.Host
		w.Write([]byte("the environment's proxy"))
	}))
	defer envProxy.Close()
	named := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("the named proxy"))
	}))
	defer named.Close()
	setProxyEnv(t, envProxy.URL, "")

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: named.URL}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runGetTool(t, tools, "http://example.invalid/thing")
	if err != nil {
		t.Fatalf("the request must go through the named proxy: %v", err)
	}
	if out != "the named proxy" {
		t.Fatalf("the named proxy's answer must come back, got %q", out)
	}
	if envGotHost != "" {
		t.Fatalf("the environment's proxy must not have been used, got host %q", envGotHost)
	}
}

// ★ The empty setting is no proxy, even when the environment has one: a
//
//	client that picks the environment's proxy up on its own is a client
//	whose route the configuration does not describe.
func TestTheEmptySettingIgnoresTheEnvironment(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Write([]byte("through the environment"))
	}))
	defer proxy.Close()
	setProxyEnv(t, proxy.URL, "")

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGetTool(t, tools, "http://example.invalid/thing"); err == nil {
		t.Fatal("a direct request to a name that does not resolve must fail")
	}
	if gotHost != "" {
		t.Fatalf("the environment's proxy must not have been used, got host %q", gotHost)
	}
}

// ★ The environment is read **once**, when the tool is built: a client
//
//	whose route changes because the environment changed under it is a
//	client the configuration does not describe.
func TestTheEnvironmentIsReadWhenTheToolIsBuilt(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Write([]byte("through the environment"))
	}))
	defer proxy.Close()
	setProxyEnv(t, "", "")

	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: "env"}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}

	// The environment changes after the tool was built.
	setProxyEnv(t, proxy.URL, "")

	if _, err := runGetTool(t, tools, "http://example.invalid/thing"); err == nil {
		t.Fatal("the client must not change its route under itself")
	}
	if gotHost != "" {
		t.Fatalf("the environment's proxy must not have been used, got host %q", gotHost)
	}
}

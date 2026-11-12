package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// TestTheProxyKeyAgainstARealProxy checks the proxy key against a real
// proxy and a real target.
//
// ★ It is **not** a hermetic test and it does not belong to the suite: it
//
//	needs the network and a proxy that happens to be running on this
//	machine, so it is behind an environment variable and the suite skips
//	it. What it buys is the one thing a fake proxy cannot buy — the
//	**egress address**: the same target answered from two different
//	places is the proof that the request went through the proxy.
//
//	OPENHERON_TEST_PROXY=http://127.0.0.1:7890 go test ./agentkit/build -run TheProxyKeyAgainstARealProxy -v
func TestTheProxyKeyAgainstARealProxy(t *testing.T) {
	proxy := os.Getenv("OPENHERON_TEST_PROXY")
	if proxy == "" {
		t.Skip("set OPENHERON_TEST_PROXY to run this check")
	}
	const trace = "https://www.cloudflare.com/cdn-cgi/trace"

	t.Run("the request goes through the proxy", func(t *testing.T) {
		out, err := fetchThrough(t, proxy, trace)
		if err != nil {
			t.Fatalf("the request through the proxy must work: %v", err)
		}
		t.Logf("through the proxy:\n%s", out)
	})

	t.Run("the same target without a proxy", func(t *testing.T) {
		out, err := fetchThrough(t, "", trace)
		if err != nil {
			t.Logf("the direct route failed: %v", err)
			return
		}
		t.Logf("direct:\n%s", out)
	})

	// ★ The guard: a private target is refused without a proxy and handed
	//
	//	to the proxy with one. That is the whole point of the setting — it
	//	moves the boundary, it does not add to it.
	t.Run("a private target without a proxy", func(t *testing.T) {
		_, err := fetchThrough(t, "", "http://192.168.1.1/")
		if err == nil {
			t.Fatal("a private target must be refused without a proxy")
		}
		if !strings.Contains(err.Error(), "non-public") {
			t.Fatalf("the error must say why, got %v", err)
		}
		t.Logf("without a proxy: %v", err)
	})

	t.Run("a private target with a proxy", func(t *testing.T) {
		_, err := fetchThrough(t, proxy, "http://192.168.1.1/")
		if err != nil && strings.Contains(err.Error(), "non-public") {
			t.Fatalf("with a proxy the guard must not fire: %v", err)
		}
		t.Logf("with a proxy: %v", err)
	})

	// ★ The environment's proxy, asked for by name: the same real proxy,
	//
	//	reached through the "env" setting. The route has to come out the
	//	same way — otherwise the setting did nothing.
	t.Run("the environment's proxy", func(t *testing.T) {
		setProxyEnv(t, proxy, "")
		out, err := fetchThrough(t, "env", trace)
		if err != nil {
			t.Fatalf("the request through the environment's proxy must work: %v", err)
		}
		t.Logf("through the environment:\n%s", out)
	})

	// ★ The whole chain: the file on disk, the configuration that loads it,
	//
	//	the tool it assembles, and the request that leaves the machine.
	t.Run("the file on disk", func(t *testing.T) {
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
		write("tools/http.yaml", "methods: [get]\nproxy: "+proxy+"\n")

		svc, err := spec.NewService(cfgRoot, root)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := svc.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tools.HTTP == nil || cfg.Tools.HTTP.Proxy != proxy {
			t.Fatalf("the file's proxy must survive the load, got %+v", cfg.Tools.HTTP)
		}
		tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
		if err != nil {
			t.Fatal(err)
		}
		out, err := runGetTool(t, tools, trace)
		if err != nil {
			t.Fatalf("the request through the proxy must work: %v", err)
		}
		t.Logf("from the file on disk:\n%s", out)
	})
}

// fetchThrough runs the HTTP tool with the given proxy setting.
func fetchThrough(t *testing.T, proxy, url string) (string, error) {
	t.Helper()
	cfg := configFromSpecs("test/model", nil, nil, nil)
	cfg.Tools.HTTP = &decl.HTTPToolsCfg{Proxy: proxy}
	tools, err := httpRequestToolProvider{}.Provide(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return runGetTool(t, tools, url)
}

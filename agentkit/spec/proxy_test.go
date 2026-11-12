package spec

import (
	"strings"
	"testing"
)

// ★ The proxy is named or it is not: an empty setting is no proxy, and a
//
//	wrong one is refused while the file is being read — not on every
//	request that follows.
func TestParseProxy(t *testing.T) {
	t.Run("empty is no proxy", func(t *testing.T) {
		for _, raw := range []string{"", "   "} {
			u, err := ParseProxy(raw)
			if err != nil {
				t.Fatalf("%q: %v", raw, err)
			}
			if u != nil {
				t.Fatalf("%q must be no proxy, got %v", raw, u)
			}
		}
	})

	// ★ socks5 is not a second proxy program: net/http's transport carries
	//
	//	its own SOCKS5 dialer, so the scheme is one the tool can speak.
	t.Run("the schemes the transport speaks", func(t *testing.T) {
		for _, raw := range []string{
			"http://127.0.0.1:7890",
			"https://proxy.example.com:8443",
			"socks5://127.0.0.1:1080",
			"socks5h://127.0.0.1:1080",
		} {
			u, err := ParseProxy(raw)
			if err != nil {
				t.Fatalf("%q: %v", raw, err)
			}
			if u == nil || u.String() != raw {
				t.Fatalf("%q must parse to itself, got %v", raw, u)
			}
		}
	})

	t.Run("a wrong proxy says why", func(t *testing.T) {
		for raw, want := range map[string]string{
			"127.0.0.1:7890":  "scheme",
			"ftp://127.0.0.1": "unsupported scheme",
			"http://":         "no host",
		} {
			_, err := ParseProxy(raw)
			if err == nil {
				t.Fatalf("%q must be rejected", raw)
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%q: the error must mention %q, got %v", raw, want, err)
			}
		}
	})
}

// ★ A wrong proxy is caught while the file is loaded: the user hears about
//
//	it there, where the file is the thing being edited, and not on every
//	request that follows.
func TestAWrongProxyIsALoadTimeError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\nproxy: 127.0.0.1:7890\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a wrong proxy must be a load-time error")
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("the error must name the proxy, got %v", err)
	}
}

func TestAProxyLoads(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\nproxy: http://127.0.0.1:7890\n")

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.HTTP == nil {
		t.Fatal("the http tool must be on")
	}
	if cfg.Tools.HTTP.Proxy != "http://127.0.0.1:7890" {
		t.Fatalf("the proxy must survive the load, got %q", cfg.Tools.HTTP.Proxy)
	}
}

// ★ The web search has a proxy of its own: it is a different file, and a
//
//	search that cannot leave the machine is a search that answers nothing.
func TestAWrongSearchProxyIsALoadTimeError(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/web_search.yaml", "provider: duckduckgo\nproxy: ftp://127.0.0.1\n")

	_, err := f.load(t)
	if err == nil {
		t.Fatal("a wrong proxy must be a load-time error")
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("the error must name the proxy, got %v", err)
	}
}

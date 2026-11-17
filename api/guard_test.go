package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/storage/filestore"
)

// The guard is the difference between an open port and remote code
// execution, because the agent's tools run shell commands. Each of its rules
// gets its own case, and each case fails when the rule is taken away.

// guardedServer builds a server whose routes sit behind the given guard.
//
// ★ It also registers a probe route: the guard is a transport concern, so
//
//	the test must not depend on what any endpoint happens to do with the
//	request it lets through.
func guardedServer(t *testing.T, g *Guard) *Server {
	t.Helper()

	fm, err := filestore.NewFileManager(filepath.Join(t.TempDir(), "sessions"), filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rt := run.New(run.Config{SessionManager: fm})

	engine := route.NewEngine(config.NewOptions(nil))
	srv := New(rt, fm, obs.Discard())
	srv.h = engine
	srv.SetGuard(g)
	srv.registerRoutes()
	srv.h.Any("/probe", func(_ context.Context, c *app.RequestContext) {
		c.String(consts.StatusOK, "ok")
	})
	t.Cleanup(func() { _ = engine.Close() })
	return srv
}

// probe performs a request against the probe route with the given headers.
func probe(t *testing.T, srv *Server, method string, headers ...ut.Header) result {
	t.Helper()

	engine, ok := srv.h.(*route.Engine)
	if !ok {
		t.Fatal("server is not backed by a route.Engine")
	}
	w := ut.PerformRequest(engine, method, "/probe",
		&ut.Body{Body: strings.NewReader(""), Len: 0}, headers...)
	code := w.Code
	if code == 0 {
		code = consts.StatusOK
	}
	return result{code: code, body: w.Body.String()}
}

func hostHeader(host string) ut.Header {
	return ut.Header{Key: "Host", Value: host}
}

// ── the Host rule ───────────────────────────────────────────────────────────

// ★ The Host is the only part of a rebound request that still tells the
//
//	truth: after DNS rebinding the request is same-origin, so there is no
//	Origin header and nothing for CORS to refuse.
func TestTheHostRuleKeepsOtherNamesOutOfALoopbackServer(t *testing.T) {
	srv := guardedServer(t, NewGuard(true))

	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.5:8080"} {
		got := probe(t, srv, "GET", hostHeader(host))
		if got.code != consts.StatusOK {
			t.Fatalf("host %q must be served, got %d: %s", host, got.code, got.body)
		}
	}

	for _, host := range []string{"evil.example.com", "192.168.1.5:8080", "openheron.example.com:8080", ""} {
		got := probe(t, srv, "GET", hostHeader(host))
		if got.code != consts.StatusForbidden {
			t.Fatalf("host %q must be refused, got %d: %s", host, got.code, got.body)
		}
		if code := decodeCode(t, got.body); code != codeForbiddenHost {
			t.Fatalf("host %q must fail as %q, got %q", host, codeForbiddenHost, code)
		}
	}
}

// ★ A server that is not on the loopback is serving somebody else on
//
//	purpose; the Host rule would break the very clients it was opened for.
func TestTheHostRuleStandsDownWhenTheServerIsNotOnTheLoopback(t *testing.T) {
	srv := guardedServer(t, NewGuard(false))

	got := probe(t, srv, "GET", hostHeader("openheron.example.com:8080"))
	if got.code != consts.StatusOK {
		t.Fatalf("a public server must serve any host, got %d: %s", got.code, got.body)
	}
}

// ── the Origin rule ─────────────────────────────────────────────────────────

func TestTheOriginRuleRefusesAnotherSite(t *testing.T) {
	srv := guardedServer(t, NewGuard(true))

	got := probe(t, srv, "POST",
		hostHeader("127.0.0.1:8080"),
		ut.Header{Key: "Origin", Value: "http://evil.example.com"})
	if got.code != consts.StatusForbidden {
		t.Fatalf("a cross-site POST must be refused, got %d: %s", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeForbiddenOrigin {
		t.Fatalf("the refusal must be %q, got %q", codeForbiddenOrigin, code)
	}
}

// ★ The dev server runs on another port, and a desktop shell would run on
//
//	another one again — so the rule is about the host, not about a fixed
//	origin.
func TestTheOriginRuleAcceptsTheLoopbackOnAnyPort(t *testing.T) {
	srv := guardedServer(t, NewGuard(true))

	for _, origin := range []string{
		"http://127.0.0.1:8080",
		"http://127.0.0.1:5173",
		"http://localhost:5173",
		"http://[::1]:8080",
	} {
		got := probe(t, srv, "POST",
			hostHeader("127.0.0.1:8080"),
			ut.Header{Key: "Origin", Value: origin})
		if got.code != consts.StatusOK {
			t.Fatalf("origin %q must be served, got %d: %s", origin, got.code, got.body)
		}
	}
}

// ★ Sec-Fetch-Site is the browser's own word for "that page is on another
//
//	site" — the cheapest signal there is.
func TestTheOriginRuleRefusesWhatTheBrowserCallsCrossSite(t *testing.T) {
	srv := guardedServer(t, NewGuard(true))

	got := probe(t, srv, "POST",
		hostHeader("127.0.0.1:8080"),
		ut.Header{Key: "Sec-Fetch-Site", Value: "cross-site"})
	if got.code != consts.StatusForbidden {
		t.Fatalf("a cross-site POST must be refused, got %d: %s", got.code, got.body)
	}

	for _, site := range []string{"same-origin", "same-site", "none"} {
		got := probe(t, srv, "POST",
			hostHeader("127.0.0.1:8080"),
			ut.Header{Key: "Sec-Fetch-Site", Value: site})
		if got.code != consts.StatusOK {
			t.Fatalf("Sec-Fetch-Site %q must be served, got %d: %s", site, got.code, got.body)
		}
	}
}

// ★ A GET changes nothing, and a page on another site cannot read the
//
//	answer without CORS — so the rule stays off the read path.
func TestTheOriginRuleLeavesTheReadPathAlone(t *testing.T) {
	srv := guardedServer(t, NewGuard(true))

	got := probe(t, srv, "GET",
		hostHeader("127.0.0.1:8080"),
		ut.Header{Key: "Origin", Value: "http://evil.example.com"})
	if got.code != consts.StatusOK {
		t.Fatalf("a GET must be served, got %d: %s", got.code, got.body)
	}
}

// ── the address rule ────────────────────────────────────────────────────────

// ★ A bare ":8080" binds **every** interface. It is the shape a port number
//
//	takes, and it is not a loopback address — so the old default was not
//	"local" at all.
func TestLoopbackOnlyReadsTheAddressItIsGiven(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		"127.0.0.1":      true,
		":8080":          false,
		"0.0.0.0:8080":   false,
		"[::]:8080":      false,
		"192.168.1.5:80": false,
	}
	for addr, want := range cases {
		if got := LoopbackOnly(addr); got != want {
			t.Fatalf("LoopbackOnly(%q) = %v, want %v", addr, got, want)
		}
	}
}

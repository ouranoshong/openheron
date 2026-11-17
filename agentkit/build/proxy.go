package build

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

// newNetworkClient builds the client the network tools use.
//
// ★ Without a proxy the tool dials the target itself and refuses a target
//
//	that does not resolve to a public address. That is the guard against
//	a model being used to reach the host's own network.
//
// ★ A proxy **replaces** that guard, and it replaces it on purpose. A
//
//	proxy exists to reach what cannot be reached directly — an intranet
//	host is not public, and reaching it is what the proxy is for — so
//	with a proxy configured the target may be any address the proxy can
//	reach, and the proxy decides. The proxy is named in the
//	configuration, so it is the user's own boundary and not ours to
//	police.
//
// ★ The two rules cannot be merged: a guard that looks at the target and
//
//	a proxy that is asked to reach the target are answers to two
//	different questions, and a target the proxy can reach is often one
//	the guard would refuse. One rule, one setting: the proxy is named,
//	or it is not.
//
// ★ Three settings, and the third one is spelled out. "" is no proxy, a
//
//	URL names the proxy and ignores the environment, and "env" hands the
//	route to HTTP_PROXY / HTTPS_PROXY / NO_PROXY. The default cannot
//	mean "whatever the environment says" — that would change the route
//	of every tool on a machine that happens to export a proxy, and the
//	one who pays for that is the person whose machine it is.
func newNetworkClient(timeout time.Duration, proxy string) (*http.Client, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	p, err := spec.ParseProxy(proxy)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}

	switch {
	case p.Env:
		return newEnvProxyClient(timeout, dialer), nil

	case p.URL != nil:
		tr := newToolTransport()
		tr.Proxy = http.ProxyURL(p.URL)
		// ★ The proxy is named in the configuration, so dialing it is not
		//   the tool reaching into the host's network — it is the user
		//   pointing at their own proxy. A local proxy is 127.0.0.1 by
		//   definition, so the public-only rule must not apply to it.
		tr.DialContext = dialer.DialContext
		return &http.Client{Timeout: timeout, Transport: tr}, nil

	default:
		tr := newToolTransport()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialPublicOnly(ctx, dialer, network, addr)
		}
		return &http.Client{Timeout: timeout, Transport: tr}, nil
	}
}

// newEnvProxyClient builds the client for the "env" setting.
//
// ★ The route is decided **per request**, because the environment decides
//
//	it: HTTP_PROXY carries one proxy, HTTPS_PROXY another, and NO_PROXY
//	takes hosts back out of the proxy. So the client holds two
//	transports and picks one per request — the one that dials the proxy,
//	and the one that dials the target under the public-address rule.
//
// ★ The transport hands the dialer an address and does not say whether
//
//	that address is the proxy or the target, so the choice cannot live
//	in the dialer: it has to happen before the transport takes the
//	request.
func newEnvProxyClient(timeout time.Duration, dialer *net.Dialer) *http.Client {
	// ★ The environment is read **once**, here, when the tool is built: a
	//   client whose route changes because the environment changed under
	//   it is a client the configuration does not describe. The values are
	//   frozen; the NO_PROXY rules still apply per request, because they
	//   are about the target, not about the moment.
	proxyFor := httpproxy.FromEnvironment().ProxyFunc()

	direct := newToolTransport()
	direct.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialPublicOnly(ctx, dialer, network, addr)
	}

	proxied := newToolTransport()
	proxied.Proxy = func(req *http.Request) (*url.URL, error) {
		if req.URL == nil {
			return nil, nil
		}
		return proxyFor(req.URL)
	}
	// ★ The proxy is the user's own boundary here too: the environment
	//   named it, and the target is not checked on that route. A request
	//   that goes **direct** — NO_PROXY took the host out — is a request
	//   this tool makes itself, so the public-address rule applies to it.
	proxied.DialContext = dialer.DialContext

	return &http.Client{
		Timeout: timeout,
		Transport: &envProxyRouter{
			direct:   direct,
			proxied:  proxied,
			proxyFor: proxyFor,
		},
	}
}

// envProxyRouter picks the transport a request goes through.
type envProxyRouter struct {
	direct  *http.Transport
	proxied *http.Transport

	// proxyFor answers the same question the transport asks: which proxy
	// serves this URL, or none.
	proxyFor func(*url.URL) (*url.URL, error)
}

func (rt *envProxyRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.proxyFor != nil && req.URL != nil {
		u, err := rt.proxyFor(req.URL)
		if err != nil {
			return nil, err
		}
		if u != nil {
			return rt.proxied.RoundTrip(req)
		}
	}
	return rt.direct.RoundTrip(req)
}

// newToolTransport builds the transport the network tools share.
func newToolTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

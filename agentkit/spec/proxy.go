package spec

import (
	"fmt"
	"net/url"
	"strings"
)

// Proxy is the proxy setting of a tool that reaches the network.
//
// ★ Three shapes, one key:
//
//	""      no proxy: the tool dials the target itself
//	"env"   the environment decides (HTTP_PROXY / HTTPS_PROXY / NO_PROXY)
//	a URL   that proxy, and the environment is ignored
type Proxy struct {
	// Env asks for the environment's proxy instead of a named one.
	Env bool

	// URL is the proxy the file named. It is nil when Env is set.
	URL *url.URL
}

// ParseProxy reads the proxy setting of a tool that reaches the network.
//
// ★ An empty setting is **no proxy**, not "the default proxy". The tools
//
//	dial the target themselves and refuse a target that does not resolve
//	to a public address; a proxy has to be named, because naming it is
//	what moves that boundary to the proxy (see decl.HTTPToolsCfg.Proxy).
//
// ★ "env" is **spelled out** rather than implied by an empty value,
//
//	because the empty value is the **default**, and the default has to
//	keep meaning what it means. The environment is a shared thing that
//	says nothing about this tool: a client that picked its proxy up on
//	its own would change its route without anybody asking, and the one
//	who pays for that is the person whose machine it is.
//
// ★ The schemes are the ones net/http's transport speaks. socks5 is one
//
//	of them — the transport carries its own SOCKS5 dialer — so a socks5
//	proxy needs no extra dependency and no second proxy program.
//
// ★ A wrong proxy is a **hard error**, not a notice: a request that cannot
//
//	leave the machine is not a degraded request, it is a failed one, and
//	the user has to fix the file before the next turn can mean anything.
func ParseProxy(raw string) (Proxy, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return Proxy{}, nil
	case strings.EqualFold(s, "env"):
		return Proxy{Env: true}, nil
	}
	if !strings.Contains(s, "://") {
		return Proxy{}, fmt.Errorf(
			"proxy %q is not a URL: it must carry a scheme, e.g. http://127.0.0.1:7890", s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return Proxy{}, fmt.Errorf("proxy %q is not a URL: %w", s, err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return Proxy{}, fmt.Errorf(
			"proxy %q uses the unsupported scheme %q (supported: http, https, socks5, socks5h)",
			s, u.Scheme)
	}
	if u.Host == "" {
		return Proxy{}, fmt.Errorf("proxy %q has no host", s)
	}
	return Proxy{URL: u}, nil
}

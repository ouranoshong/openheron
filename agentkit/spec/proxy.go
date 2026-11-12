package spec

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseProxy reads the proxy setting of a tool that reaches the network.
//
// ★ An empty setting is **no proxy**, not "the default proxy". The tools
//
//	dial the target themselves and refuse a target that does not resolve
//	to a public address; a proxy has to be named, because naming it is
//	what moves that boundary to the proxy (see decl.HTTPToolsCfg.Proxy).
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
func ParseProxy(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if !strings.Contains(s, "://") {
		return nil, fmt.Errorf(
			"proxy %q is not a URL: it must carry a scheme, e.g. http://127.0.0.1:7890", s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("proxy %q is not a URL: %w", s, err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf(
			"proxy %q uses the unsupported scheme %q (supported: http, https, socks5, socks5h)",
			s, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy %q has no host", s)
	}
	return u, nil
}

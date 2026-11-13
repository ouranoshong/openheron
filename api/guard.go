package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// The guard answers one question: may this request drive the agent?
//
// ★ openheron runs tools that execute shell commands, so an open port is not
//   a convenience — it is remote code execution with a confirmation step.
//
// ★ **Two defences, and both of them cost the user nothing.** There is no
//   token and no account: the server is a single-user tool on one machine,
//   and the two rules below are about the **browser**, which is the only
//   thing that can be tricked into sending a request nobody asked for.
//
//  1. The Host header. A page on another site can point the browser at
//     127.0.0.1 (DNS rebinding), and then the request is **same-origin**:
//     no Origin header, no preflight, nothing for CORS to refuse. The Host
//     is the only part of the request that still tells the truth. When the
//     server listens on the loopback, the Host must name the loopback.
//  2. The Origin header. A page on another site can also POST to the
//     server directly. The browser attaches Origin, and an Origin from
//     another site says the request did not come from the UI.
//
// ★ The first one is conditional on the loopback. A server bound to 0.0.0.0
//   is serving somebody else on purpose, and a Host rule would break the
//   very clients it was opened for.

const (
	// codeForbiddenHost is the code for a request whose Host does not name
	// this host.
	codeForbiddenHost = "forbidden_host"
	// codeForbiddenOrigin is the code for a request that comes from another
	// site.
	codeForbiddenOrigin = "forbidden_origin"
)

// Guard is the access policy of one server.
type Guard struct {
	// loopbackOnly says the server listens on a loopback address, so only a
	// loopback caller may drive it.
	loopbackOnly bool
}

// NewGuard builds the policy.
//
// loopbackOnly says the server listens on a loopback address.
func NewGuard(loopbackOnly bool) *Guard {
	return &Guard{loopbackOnly: loopbackOnly}
}

// Reject returns the status and the error body when the request must not be
// served. A zero status means it may.
func (g *Guard) Reject(c *app.RequestContext) (int, errBody) {
	if g == nil {
		return 0, errBody{}
	}
	if g.loopbackOnly && !loopbackHost(hostOf(c)) {
		return consts.StatusForbidden, errBody{
			Error: "the request does not name this host",
			Code:  codeForbiddenHost,
		}
	}
	if mutating(string(c.Request.Method())) && !sameSite(c) {
		return consts.StatusForbidden, errBody{
			Error: "the request does not come from this site",
			Code:  codeForbiddenOrigin,
		}
	}
	return 0, errBody{}
}

// sameSite reports whether a mutating request comes from this site.
//
// ★ Two rules, and the first one is the browser's own words:
//   - Sec-Fetch-Site: cross-site means the browser says the page is on
//     another site. That is the cheapest signal there is.
//   - The Origin header says where the page came from. It must name the
//     loopback (the UI and the dev server are both on it, on different
//     ports) or the very host the request addressed.
//
// ★ Why the Origin is compared to the Host and not to a fixed origin: the
//
//	dev server runs on another port, and a desktop shell would run on
//	another one again. The host is what both sides agree on.
func sameSite(c *app.RequestContext) bool {
	if site := c.Request.Header.Get("Sec-Fetch-Site"); site != "" && site == "cross-site" {
		return false
	}
	origin := c.Request.Header.Get("Origin")
	if origin == "" {
		// ★ No Origin: a script (curl) or a browser that does not send one.
		//   The Host rule is what protects the browser case.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	name := u.Hostname()
	if name == "" {
		return false
	}
	if loopbackName(name) {
		return true
	}
	return strings.EqualFold(name, hostName(hostOf(c)))
}

// mutating reports whether the method changes something.
//
// ★ A GET is left alone: a page on another site cannot read the answer
//
//	without CORS, and the Host rule covers the rebinding case.
func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// LoopbackOnly reports whether a listen address binds the loopback only.
//
// ★ A bare ":8080" binds **every** interface — it is the shape a port number
//
//	takes, and it is not a loopback address. So the default that used to
//	read like "local" was in fact "open to the network".
func LoopbackOnly(addr string) bool {
	name := hostName(addr)
	if name == "" {
		return false
	}
	return loopbackName(name)
}

// loopbackHost reports whether a Host header names the loopback.
func loopbackHost(host string) bool {
	name := hostName(host)
	if name == "" {
		return false
	}
	return loopbackName(name)
}

// loopbackName reports whether a hostname is the loopback.
//
// ★ "localhost" counts: it is what a person types, and Go resolves it to a
//
//	loopback address. An IP counts when it is in 127.0.0.0/8 or is ::1.
//	An empty name does not — a request without a Host is not a request a
//	browser made, and it is not one to serve either.
func loopbackName(name string) bool {
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// hostName strips the port off a host or an address.
func hostName(host string) string {
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	// No port: the value may still be a bracketed IPv6 literal.
	return strings.Trim(host, "[]")
}

// hostOf reads the Host **header**.
//
// ★ Not c.Request.Host(): that one answers the **URI's** host, and a
//
//	request line carries a path ("GET /v2/health HTTP/1.1"), so it is empty
//	for every ordinary request. The header is the one the client meant.
func hostOf(c *app.RequestContext) string {
	return string(c.Request.Header.Host())
}

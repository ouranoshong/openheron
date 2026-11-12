package build

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
	"github.com/cloudwego/eino-ext/components/tool/httprequest/delete"
	"github.com/cloudwego/eino-ext/components/tool/httprequest/get"
	"github.com/cloudwego/eino-ext/components/tool/httprequest/post"
	"github.com/cloudwego/eino-ext/components/tool/httprequest/put"
	"github.com/cloudwego/eino-ext/components/tool/wikipedia"
	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

const (
	defaultHTTPTimeout      = 30 * time.Second
	defaultWebSearchTimeout = 60 * time.Second
	defaultWikipediaTimeout = 15 * time.Second
	defaultToolOutputBytes  = 20000
)

// newPublicOnlyClient builds the client the network tools use.
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
func newPublicOnlyClient(timeout time.Duration, proxy string) (*http.Client, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	proxyURL, err := spec.ParseProxy(proxy)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil {
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialPublicOnly(ctx, dialer, network, addr)
		}
	} else {
		tr.Proxy = http.ProxyURL(proxyURL)
		// ★ The proxy is named in the configuration, so dialing it is not
		//   the tool reaching into the host's network — it is the user
		//   pointing at their own proxy. A local proxy is 127.0.0.1 by
		//   definition, so the public-only rule must not apply to it.
		tr.DialContext = dialer.DialContext
	}
	return &http.Client{Timeout: timeout, Transport: tr}, nil
}

func dialPublicOnly(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split address %q: %w", addr, err)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %q: no address", host)
	}
	var lastErr error
	for _, ip := range ips {
		if !isPublicIP(ip.IP) {
			lastErr = fmt.Errorf("refusing to connect to a non-public address: %s (%s)", host, ip.IP)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable address for %q", host)
	}
	return nil, lastErr
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return false
		}
	}
	return true
}

type cappedTool struct {
	tool.InvokableTool
	maxBytes int
}

func (t cappedTool) InvokableRun(ctx context.Context, argumentsInJSON string,
	opts ...tool.Option) (string, error) {
	out, err := t.InvokableTool.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		return out, err
	}
	if t.maxBytes <= 0 || len(out) <= t.maxBytes {
		return out, nil
	}
	return out[:t.maxBytes] + fmt.Sprintf("\n\n[truncated: %d more bytes]", len(out)-t.maxBytes), nil
}

func capTool(t tool.InvokableTool, maxBytes int) tool.InvokableTool {
	if t == nil {
		return nil
	}
	return cappedTool{InvokableTool: t, maxBytes: maxBytes}
}

type httpRequestToolProvider struct{}

func (httpRequestToolProvider) ID() string     { return "http" }
func (httpRequestToolProvider) Optional() bool { return true }

func (httpRequestToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	cfg := env.cfg.Tools.HTTP
	if cfg == nil {
		return nil, nil
	}
	cli, err := newPublicOnlyClient(cfg.Timeout.Duration(), cfg.Proxy)
	if err != nil {
		return nil, err
	}
	headers := cfg.Headers

	methods := map[string]bool{}
	for _, m := range cfg.Methods {
		m := strings.ToLower(strings.TrimSpace(m))
		switch m {
		case "get", "post", "put", "delete":
			methods[m] = true
		default:
			return nil, fmt.Errorf("unknown http method %q (supported: get, post, put, delete)", m)
		}
	}
	if len(methods) == 0 {
		methods["get"] = true
	}

	var out []tool.BaseTool
	add := func(t tool.InvokableTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, capTool(t, defaultToolOutputBytes))
		return nil
	}

	//   request_get / requests_post / requests_put / requests_delete
	if methods["get"] {
		if err := add(get.NewTool(ctx, &get.Config{
			ToolName: decl.ToolNameHTTPGet, Headers: headers, HttpClient: cli,
		})); err != nil {
			return nil, err
		}
	}
	if methods["post"] {
		if err := add(post.NewTool(ctx, &post.Config{
			ToolName: decl.ToolNameHTTPPost, Headers: headers, HttpClient: cli,
		})); err != nil {
			return nil, err
		}
	}
	if methods["put"] {
		if err := add(put.NewTool(ctx, &put.Config{
			ToolName: decl.ToolNameHTTPPut, Headers: headers, HttpClient: cli,
		})); err != nil {
			return nil, err
		}
	}
	if methods["delete"] {
		if err := add(delete.NewTool(ctx, &delete.Config{
			ToolName: decl.ToolNameHTTPDelete, Headers: headers, HttpClient: cli,
		})); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type webSearchToolProvider struct{}

func (webSearchToolProvider) ID() string     { return "web_search" }
func (webSearchToolProvider) Optional() bool { return true }

func (webSearchToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	cfg := env.cfg.Tools.WebSearch
	if cfg == nil {
		return nil, nil
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "duckduckgo"
	}
	switch provider {
	case "duckduckgo":
	default:
		return nil, fmt.Errorf("unknown web search provider %q (supported: duckduckgo)", cfg.Provider)
	}

	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	region := duckduckgo.Region(strings.TrimSpace(cfg.Region))
	if region == "" {
		region = duckduckgo.RegionWT
	}
	timeout := cfg.Timeout.Duration()
	if timeout <= 0 {
		timeout = defaultWebSearchTimeout
	}

	cli, err := newPublicOnlyClient(timeout, cfg.Proxy)
	if err != nil {
		return nil, err
	}
	t, err := duckduckgo.NewTextSearchTool(ctx, &duckduckgo.Config{
		ToolName:   decl.ToolNameWebSearch,
		MaxResults: maxResults,
		Region:     region,
		Timeout:    timeout,
		HTTPClient: cli,
	})
	if err != nil {
		return nil, fmt.Errorf("build the duckduckgo search tool: %w", err)
	}
	return []tool.BaseTool{capTool(t, defaultToolOutputBytes)}, nil
}

type wikipediaToolProvider struct{}

func (wikipediaToolProvider) ID() string     { return "wikipedia" }
func (wikipediaToolProvider) Optional() bool { return true }

func (wikipediaToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	cfg := env.cfg.Tools.Wikipedia
	if cfg == nil {
		return nil, nil
	}
	topK := cfg.TopK
	if topK <= 0 {
		topK = 3
	}
	language := strings.TrimSpace(cfg.Language)
	if language == "" {
		language = "zh"
	}
	timeout := cfg.Timeout.Duration()
	if timeout <= 0 {
		timeout = defaultWikipediaTimeout
	}

	t, err := wikipedia.NewTool(ctx, &wikipedia.Config{
		ToolName:  decl.ToolNameWikipedia,
		TopK:      topK,
		Language:  language,
		Timeout:   timeout,
		BaseURL:   strings.TrimSpace(cfg.BaseURL),
		UserAgent: "openheron (https://github.com/cloudwego/openheron)",
	})
	if err != nil {
		return nil, fmt.Errorf("build the wikipedia tool: %w", err)
	}
	return []tool.BaseTool{capTool(t, defaultToolOutputBytes)}, nil
}

var _ = decl.ToolsCfg{}

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

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

const (
	defaultHTTPTimeout      = 30 * time.Second
	defaultWebSearchTimeout = 60 * time.Second
	defaultWikipediaTimeout = 15 * time.Second
	defaultToolOutputBytes  = 20000
)

func newPublicOnlyClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialPublicOnly(ctx, dialer, network, addr)
			},
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
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
	cli := newPublicOnlyClient(cfg.Timeout.Duration())
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

	t, err := duckduckgo.NewTextSearchTool(ctx, &duckduckgo.Config{
		ToolName:   decl.ToolNameWebSearch,
		MaxResults: maxResults,
		Region:     region,
		Timeout:    timeout,
		HTTPClient: newPublicOnlyClient(timeout),
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

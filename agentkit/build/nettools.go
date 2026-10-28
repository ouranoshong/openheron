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

	"openheron/agentkit/spec/decl"
)

// 本文件是「能连到外面去」的三类工具：通用 HTTP 请求、网页搜索、维基检索。
//
// ★ 它们与知识库工具的差别只有一处，但那一处是全部：
//
//	它们的**输出会原样进模型上下文**。知识库召回的是自己写的语料，
//	而这里是别人写的网页 —— 于是「外部内容里的指令」也进了模型脑子。
//	策略层（allow/ask/deny）管的是「能不能调」，管不了这个。
//
//	所以这一族工具的规矩是：
//
//	 ① **默认全关** —— 没配就一个都不挂（与知识库同一条规矩）。
//	 ② URL 是模型给的 ⇒ client 只连**公网**（见 [newPublicOnlyClient]）。
//	 ③ 输出有**长度上限**（见 [cappedTool]）—— 一个 10MB 的页面会把这一轮
//	    直接撑爆，不是报错，是「模型突然开始胡说」。
//	 ④ 能改外部世界的方法（POST/PUT/DELETE）要**显式点名**才给。

const (
	// defaultHTTPTimeout 是单个 HTTP 请求的上限。
	defaultHTTPTimeout = 30 * time.Second
	// defaultWebSearchTimeout 是单次网页搜索的上限。
	//
	// ★ 它比 HTTP 宽裕：搜索要翻页，页间还有 3s 的固定间隔（框架侧）。
	defaultWebSearchTimeout = 60 * time.Second
	// defaultWikipediaTimeout 是单次维基检索的上限。
	defaultWikipediaTimeout = 15 * time.Second
	// defaultToolOutputBytes 是工具输出的字节上限。
	//
	// ★ 20000 字节 ≈ 5000 个中文字符 / 约 5000 token。够读一篇网页的
	//   摘要，不够把整本书塞进上下文。
	defaultToolOutputBytes = 20000
)

// ── 公网限定的 HTTP client ────────────────────────────────────────────────

// newPublicOnlyClient 造一个「只连公网」的 HTTP client。
//
// ★ 为什么必须自己造：这几个工具的 URL 是**模型给的**。一个能访问
//
//	127.0.0.1 的 HTTP 工具，等于给了模型一把钥匙：读本机配置、读会话
//	状态、读云上的元数据服务（169.254.169.254），再顺着 POST 外发。
//	策略层挡不住这个 —— 它只看「能不能调这个工具」，看不见 URL。
//
//	★ 为什么在**拨号那一刻**判，而不是解析域名时判：DNS 可以在两次解析
//
//	之间换答案（rebinding）。只查一次等于给了绕过；这里解析、判完
//	**直接连那个 IP**，让「检查过的地址」与「连上的地址」是同一个。
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

// dialPublicOnly 解析目标地址，逐个检查后连到**被检查过的那个 IP**。
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
		// ★ 连这个 IP，而不是让 dialer 再解析一次 —— 否则 DNS 可以在
		//   「检查」与「连接」之间换答案（rebinding）。
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

// isPublicIP 报告这个地址能不能被外部内容指向。
//
// ★ 挡掉的是「模型能自己够到」的那一类：
//
//	回环（本机）、私有网段（内网服务）、链路本地（169.254 那些，包括
//	云上元数据服务）、未指定、以及 CGNAT（100.64/10 —— 不少云厂商的
//	内部服务落在这一段）。
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
		// 100.64.0.0/10 —— 运营商级 NAT，云厂商常用它放内部服务。
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		// 192.0.0.0/24 —— IETF 协议保留。
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return false
		}
	}
	return true
}

// ── 输出长度上限 ──────────────────────────────────────────────────────────

// cappedTool 给工具的输出加一道长度上限。
//
// ★ 为什么：输出会**原样进模型上下文**。一个 10MB 的网页（或一个
//
//	返回 10MB 的接口）会把这一轮直接撑爆 —— 表现为「模型突然开始胡说」，
//	而不是报错。
//
//	截断在**字节**上做，尾巴上写明截了多少：模型得知道它看到的是残篇。
type cappedTool struct {
	tool.InvokableTool
	maxBytes int
}

// InvokableRun 调用底层工具并截断它的输出。
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

// capTool 给一个工具套上输出上限。
func capTool(t tool.InvokableTool, maxBytes int) tool.InvokableTool {
	if t == nil {
		return nil
	}
	return cappedTool{InvokableTool: t, maxBytes: maxBytes}
}

// ── provider：通用 HTTP 请求 ──────────────────────────────────────────────

// httpRequestToolProvider 挂通用 HTTP 请求工具。
type httpRequestToolProvider struct{}

func (httpRequestToolProvider) ID() string     { return "http" }
func (httpRequestToolProvider) Optional() bool { return true } // 配错不该拖垮整个 agent

func (httpRequestToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	// ★ 「这一段配置在不在」就是开关 —— 没有 enabled 字段（见 decl.ToolsCfg）。
	cfg := env.cfg.Tools.HTTP
	if cfg == nil {
		return nil, nil
	}
	cli := newPublicOnlyClient(cfg.Timeout.Duration())
	headers := cfg.Headers

	// ★ 方法是**白名单**：默认只有 get。POST/PUT/DELETE 能改外部世界的
	//   状态，得在配置里显式点名 —— 模型不需要理由就能编出一个 POST。
	methods := map[string]bool{}
	for _, m := range cfg.Methods {
		m := strings.ToLower(strings.TrimSpace(m))
		switch m {
		case "get", "post", "put", "delete":
			methods[m] = true
		default:
			// ★ 写错的方法名不静默忽略 —— 配置说要用它、实际没挂，
			//   用户会以为「配了没用」。宁可整组不挂 + 出声。
			return nil, fmt.Errorf("unknown http method %q (supported: get, post, put, delete)", m)
		}
	}
	if len(methods) == 0 {
		methods["get"] = true
	}

	var out []tool.BaseTool
	// add 接住 NewTool 的两个返回值：它要么给出工具、要么给出原因。
	add := func(t tool.InvokableTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, capTool(t, defaultToolOutputBytes))
		return nil
	}

	// ★ 名字**由这里定**，不用上游的默认值 —— 上游那一组是
	//   request_get / requests_post / requests_put / requests_delete
	//   （get 少个 s）。模型看到的是名字，一组自相矛盾的名字会让它以为
	//   这是两类东西。
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

// ── provider：网页搜索 ────────────────────────────────────────────────────

// webSearchToolProvider 挂网页搜索工具。
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
		// ★ 未知后端不静默降级成「也是 duckduckgo」—— 那会让配置写错
		//   却看不出。只出声，不挂。
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

// ── provider：维基百科 ────────────────────────────────────────────────────

// wikipediaToolProvider 挂维基百科检索工具。
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
		ToolName: decl.ToolNameWikipedia,
		TopK:     topK,
		Language: language,
		Timeout:  timeout,
		BaseURL:  strings.TrimSpace(cfg.BaseURL),
		// ★ 用自己的 UA：维基的机器人策略要求能联系到人，框架默认的
		//   "eino (...)" 不够。
		UserAgent: "openheron (https://github.com/cloudwego/openheron)",
	})
	if err != nil {
		return nil, fmt.Errorf("build the wikipedia tool: %w", err)
	}
	return []tool.BaseTool{capTool(t, defaultToolOutputBytes)}, nil
}

// compile-time 断言：配置段的名字不能悄悄改（文档与示例都指着它）。
var _ = decl.ToolsCfg{}

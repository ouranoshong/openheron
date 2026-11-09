package spec

import (
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"strings"
	"time"
)

// The caps' defaults, for the configuration that does not name them.
const (
	DefaultMaxDepth   = 1
	DefaultMaxRetries = 5
)

func (c *Config) EnabledToolNames() []string {
	if c == nil {
		return nil
	}
	var out []string
	if t := c.Tools.HTTP; t != nil {
		for _, m := range t.Methods {
			switch strings.ToLower(strings.TrimSpace(m)) {
			case "get":
				out = append(out, decl.ToolNameHTTPGet)
			case "post":
				out = append(out, decl.ToolNameHTTPPost)
			case "put":
				out = append(out, decl.ToolNameHTTPPut)
			case "delete":
				out = append(out, decl.ToolNameHTTPDelete)
			}
		}
		if len(t.Methods) == 0 {
			out = append(out, decl.ToolNameHTTPGet)
		}
	}
	if c.Tools.WebSearch != nil {
		out = append(out, decl.ToolNameWebSearch)
	}
	if c.Tools.Wikipedia != nil {
		out = append(out, decl.ToolNameWikipedia)
	}
	return out
}

// ── tools/ ─────────────────────────────────────────────────────────────────

func defaultKB() decl.KB {
	return decl.KB{
		Index:         "default",
		BatchSize:     10,
		MaxBatchChars: 1200,
		TopK:          8,
	}
}

func defaultServer() decl.Server {
	return decl.Server{
		ListenAddr: ":8080",
		StaticDir:  "web/dist",
		DataDir:    DirState,
		LogLevel:   "info",
		Log: decl.LogCfg{
			Console:    true,
			MaxSizeMB:  20,
			MaxAgeDays: 14,
			MaxBackups: 5,
		},
		StartTimeout: decl.LenientDuration(60 * time.Second),
		LeaseTTL:     decl.LenientDuration(10 * time.Minute),
		Agent:        decl.AgentCfg{MaxDepth: 1},
		Watch:        decl.Watch{Debounce: decl.LenientDuration(decl.DefaultDebounce)},
		Retry:        decl.RetryCfg{MaxRetries: 5},
	}
}

// ── decl.Duration ─────────────────────────────────────────────────────────────

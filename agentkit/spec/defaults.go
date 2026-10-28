package spec

import (
	"openheron/agentkit/spec/decl"
	"strings"
	"time"
)

// defaultPolicy 是出厂策略。
//
// ★ 出厂策略只点**名**要审批的那几个：
//
//   - write_file / edit_file / execute —— 会改环境、会执行命令；
//   - kb_index —— 会写索引（读多写少，但写的那部分要有人点头）。
//
// ★ **兜底是 allow**（见 [decl.Policy.TierOf]）：没点名的工具一律放行。
//
//	这不是疏忽，是刻意的 —— [decl.Policy] 根本没有 allow 字段：
//	「直接放行」不需要一列来声明，留着 allow 只会让人以为
//	「没列在 allow 里就是不让用」，而事实相反。
//
//	（agent 层的 [agent.Policy.TierOf] 是同一套语义，两处必须一致。）
//
// 工具名在这里是**字面量**，与 read_file / write_file / grep 那些一致 ——
//
// 它们来自 eino 的中间件或各功能自己的包，openheron 从未把工具名收敛到一处。
// kb 自己那两份常量在 kb/tools.go，两边是否一致由 TestDefaultPolicyCoversKBTools
// 钉住 —— 跨包常量没法共用，但漂移可以变成一次红的测试。
//
// ★ 因此 kb_search **不需要**任何特殊处理：它不在 ask / deny 里，
//
// 于是它是 allow。只读工具被要求审批，会让模型每问一次都停一次，
// 而那正是它最该被用的时候。
func defaultPolicy() decl.Policy {
	return decl.Policy{
		Ask:  []string{"write_file", "edit_file", "execute", "kb_index"},
		Deny: nil,
	}
}

// EnabledToolNames 返回这份配置**显式启用**的工具名。
//
// ★ 未点名的工具本来就是放行的（策略的兜底档是 allow ✅）—— 所以这份名单
//
//	**不决定权限** ✅ ✅。它只为了让这些工具出现在工具表里（`/v2/agents` 的
//	名字全集来自策略点过的那些 ✅）。
//
//	★ 「显式启用」= **那一段配置在不在**（见 [decl.ToolsCfg]）—— 所以这里判的是
//	指针为不为 nil，不是某个 enabled 字段。
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
		// ★ **不给 `Splitter.Headers` 缺省值**（v2 起）。
		//
		//	它已经什么都不决定，而「预填一个缺省」会让 `kb status` 的
		//	「该配置项已不生效」提示对**每一个**用户都出现 —— 包括从没写过
		//	这个字段的那些。一条永远在的提醒等于没有提醒，而它本来要提示的
		//	正是「你的配置里有这个字段，它不起作用了」。
		//
		//	留空之后：写了的会看到提示，没写的看不到。这正是 §9.1 要的效果。
	}
}

func defaultServer() decl.Server {
	return decl.Server{
		ListenAddr:   ":8080",
		StaticDir:    "web/dist",
		DataDir:      DirState,
		StartTimeout: decl.LenientDuration(60 * time.Second),
		Agent:        decl.AgentCfg{MaxDepth: 1},
		Watch:        decl.Watch{Debounce: decl.LenientDuration(decl.DefaultDebounce)},
		Retry:        decl.RetryCfg{MaxRetries: 5},
	}
}

// ── decl.Duration ─────────────────────────────────────────────────────────────

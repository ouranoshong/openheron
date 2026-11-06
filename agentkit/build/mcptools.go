package build

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// The reasons a tool is not exposed, as the wire reports them.
//
// ★ The reasons are tokens and the copy is the panel's: "disabled_tools"
//
//	and "mcps" are machine values, like the code of an error body.
const (
	MCPSkipNameTooLong   = "name_too_long"
	MCPSkipNameCollision = "name_collision"
)

// mcpToolPlan is one tool of one server, as the plan sees it.
type mcpToolPlan struct {
	// Server is the server the tool belongs to.
	Server string
	// Own is the server's own name for the tool.
	Own string
	// Exposed is the name the model sees.
	Exposed string
	// Tier is the tier the gate will use for the exposed name.
	Tier Tier
	// Reason is why the tool is not exposed. Empty when it is exposed.
	Reason string
}

// mcpServerPlan is one server's mapping.
type mcpServerPlan struct {
	Server    string
	Transport string
	// Tools are the tools the server offers, in the server's own order.
	Tools []mcpToolPlan
	// Allowed is the exposed names the server's file allows.
	Allowed map[string]bool
}

// mcpPlan maps one server's own tool names onto the names the model sees.
//
// ★ It is a free function because two callers need it: the provider, which
//
//	turns the plan into eino tools, and the projection, which reports it
//	(§11.2). A second copy of the mapping would be a second truth about a
//	tool's name and a tool's tier.
//
// ★ The tier is read from the file's own vocabulary. The file says
//
//	"create_issue", the model sees "mcp__github__create_issue", and the
//	translation happens here — once.
func mcpPlan(cfg *decl.MCPServerCfg, own []string) mcpServerPlan {
	plan := mcpServerPlan{
		Server:    cfg.Name,
		Transport: cfg.Transport,
		Tools:     make([]mcpToolPlan, 0, len(own)),
		Allowed:   map[string]bool{},
	}

	allows := map[string]bool{}
	for _, a := range cfg.Allows {
		allows[a] = true
		plan.Allowed[mcpExposedName(cfg.Name, a)] = true
	}

	seen := map[string]bool{}
	for _, name := range own {
		if !mcpOffers(cfg, name) {
			continue
		}
		exposed := mcpExposedName(cfg.Name, name)
		switch {
		case len(exposed) > decl.MCPMaxExposedNameLen:
			// ★ Skip and warn, never fail the process: the name is chosen
			//   by a remote server, and a server that adds one odd tool
			//   should cost that tool, not the whole agent.
			plan.Tools = append(plan.Tools, mcpToolPlan{
				Server: cfg.Name, Own: name, Exposed: exposed,
				Tier: TierAsk, Reason: MCPSkipNameTooLong,
			})
			continue
		case seen[exposed]:
			plan.Tools = append(plan.Tools, mcpToolPlan{
				Server: cfg.Name, Own: name, Exposed: exposed,
				Tier: TierAsk, Reason: MCPSkipNameCollision,
			})
			continue
		}
		seen[exposed] = true

		tier := TierAsk
		if allows[name] {
			tier = TierAllow
		}
		plan.Tools = append(plan.Tools, mcpToolPlan{
			Server: cfg.Name, Own: name, Exposed: exposed, Tier: tier,
		})
	}
	return plan
}

// mcpOffers reports whether the file's tools: list keeps this tool.
//
// ★ The list is a filter, not a switch: an absent or empty list means all
//
//	of them, and the list never adds a tool the server does not have.
func mcpOffers(cfg *decl.MCPServerCfg, own string) bool {
	if len(cfg.Tools) == 0 {
		return true
	}
	for _, t := range cfg.Tools {
		if t == own {
			return true
		}
	}
	return false
}

// mcpExposedName composes the name the model sees.
//
// ★ It is the one place a name is composed: the provider and the
//
//	projection both go through it, so they cannot disagree about what a
//	tool is called.
func mcpExposedName(server, own string) string {
	return decl.MCPToolPrefix + server + decl.MCPSeparator + sanitizeMCPToolName(own)
}

// sanitizeMCPToolName replaces the characters the model provider's
// function-name set does not accept.
//
// ★ The case is preserved: the server's own name is the identity, and a
//
//	lowercased copy of it would be a second name to keep in sync.
func sanitizeMCPToolName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// mcpAllowedNames returns the exposed names the servers' files allow.
//
// ★ The tier of an MCP tool is assembled from the configuration, not from
//
//	the server: the entry "mcp__github__read_issue → allow" can be built
//	before the server is even up, so the gate's lookup never waits for a
//	connection.
func mcpAllowedNames(files decl.MCPFiles) map[string]bool {
	out := map[string]bool{}
	for name, cfg := range files {
		if cfg == nil {
			continue
		}
		for _, own := range cfg.Allows {
			out[mcpExposedName(name, own)] = true
		}
	}
	return out
}

// ── the provider ────────────────────────────────────────────────────────

// mcpToolProvider mounts the configured MCP servers as tools.
//
// ★ Optional: an unreachable server must not keep the agent down. The
//
//	price is that the provider's warning is the only notice — so the
//	warning names the server, the transport and the error.
type mcpToolProvider struct {
	mgr *mcpManager
}

func (mcpToolProvider) ID() string     { return "mcp" }
func (mcpToolProvider) Optional() bool { return true }

func (p mcpToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	if env.cfg == nil || len(env.cfg.MCPs) == 0 {
		return nil, nil
	}
	if p.mgr == nil {
		return nil, nil
	}

	var out []tool.BaseTool
	for _, name := range env.cfg.MCPServersInUse() {
		scfg := env.cfg.MCPs[name]
		if scfg == nil {
			continue
		}
		cli, own, err := p.mgr.Tools(ctx, scfg)
		if err != nil {
			obs.Logger().Warn("[mcp] the server is unreachable",
				"server", name, "transport", scfg.Transport, "err", err)
			continue
		}
		out = append(out, p.mount(ctx, scfg, cli, own)...)
	}
	return out, nil
}

// mount turns one server's tools into eino tools.
func (p mcpToolProvider) mount(ctx context.Context, cfg *decl.MCPServerCfg,
	cli mcpClient, own []string) []tool.BaseTool {

	plan := mcpPlan(cfg, own)

	for _, t := range plan.Tools {
		if t.Reason == "" {
			continue
		}
		obs.Logger().Warn("[mcp] tool skipped",
			"server", cfg.Name, "tool", t.Own, "exposed", t.Exposed, "reason", t.Reason)
	}
	for _, want := range cfg.Tools {
		if !containsString(own, want) {
			obs.Logger().Warn("[mcp] the file asks for a tool the server does not have",
				"server", cfg.Name, "tool", want, "offers", strings.Join(own, ","))
		}
	}

	// The component's tool list holds the server's own names, which is
	// exactly what the file's tools: list holds — and it is the list the
	// plan kept, so the two cannot disagree about what is offered.
	keep := make([]string, 0, len(plan.Tools))
	for _, t := range plan.Tools {
		if t.Reason == "" {
			keep = append(keep, t.Own)
		}
	}

	batch, err := mcpp.GetTools(ctx, &mcpp.Config{
		Cli:                   cli,
		ToolNameList:          keep,
		CustomHeaders:         cfg.Headers,
		ToolCallResultHandler: capMCPToolResult(defaultToolOutputBytes),
	})
	if err != nil {
		obs.Logger().Warn("[mcp] the server's tools could not be read",
			"server", cfg.Name, "transport", cfg.Transport, "err", err)
		return nil
	}

	byName := make(map[string]tool.BaseTool, len(batch))
	for _, t := range batch {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		byName[info.Name] = t
	}

	var out []tool.BaseTool
	for _, t := range plan.Tools {
		if t.Reason != "" {
			continue
		}
		inner, ok := byName[t.Own]
		if !ok {
			continue
		}
		inv, ok := inner.(tool.InvokableTool)
		if !ok {
			continue
		}
		var body tool.InvokableTool = inv
		if cfg.Transport == decl.MCPTransportStdio {
			// ★ The calls of one stdio server are serialized: the
			//   transport writes the request straight to the child's
			//   stdin, and a pipe write is atomic only up to PIPE_BUF.
			body = serialTool{
				InvokableTool: inv,
				mu:            p.mgr.callMutex(cfg.Name),
				timeout:       cfg.CallTimeout.Value,
				onFailure:     p.mgr.markBroken(cfg.Name),
			}
		}
		out = append(out, mcpTool{InvokableTool: body, exposed: t.Exposed})
	}
	return out
}

// mcpTool is one MCP tool, exposed under its namespaced name.
//
// ★ The component's tool keeps the server's own name — that is the name
//
//	the call goes out with — so the exposed name lives here.
type mcpTool struct {
	tool.InvokableTool
	exposed string
}

func (t mcpTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := t.InvokableTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	cp := *info
	cp.Name = t.exposed
	return &cp, nil
}

// serialTool serializes the calls of one stdio server.
//
// ★ This is a correctness decision, not a performance one: mcp-go's stdio
//
//	transport writes the request straight to the child's stdin, and POSIX
//	makes a pipe write atomic only up to PIPE_BUF — so a larger JSON-RPC
//	line can be interleaved with another call's line, and the server reads
//	a corrupted frame. The mutex waits; it does not fail the second call.
type serialTool struct {
	tool.InvokableTool
	mu        *sync.Mutex
	timeout   time.Duration
	onFailure func()
}

func (t serialTool) InvokableRun(ctx context.Context, argumentsInJSON string,
	opts ...tool.Option) (string, error) {

	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.timeout > 0 {
		// ★ A call has a deadline: a mutex without one is a way to hang
		//   a turn, and every other call of that server would queue
		//   behind it.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	out, err := t.InvokableTool.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil && t.onFailure != nil {
		t.onFailure()
	}
	return out, err
}

// capMCPToolResult truncates the tool's output.
//
// ★ A tool's output goes into the model's context verbatim, and a huge
//
//	page breaks the turn — it reads as "the model suddenly starts making
//	things up", not as an error. The cap is the same one every other tool
//	has, and the component's result handler is where it belongs: the
//	result is still a well-formed JSON-RPC result afterwards.
func capMCPToolResult(maxBytes int) func(context.Context, string, *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	return func(_ context.Context, _ string, result *mcp.CallToolResult) (*mcp.CallToolResult, error) {
		if result == nil || maxBytes <= 0 {
			return result, nil
		}
		used := 0
		for i, c := range result.Content {
			text, ok := c.(mcp.TextContent)
			if !ok {
				continue
			}
			room := maxBytes - used
			if room < 0 {
				room = 0
			}
			if len(text.Text) <= room {
				used += len(text.Text)
				continue
			}
			text.Text = text.Text[:room] +
				fmt.Sprintf("\n\n[truncated: %d more bytes]", len(text.Text)-room)
			result.Content[i] = text
			used = maxBytes
			break
		}
		return result, nil
	}
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// mcpServersOfConfig returns the configured servers' names, sorted.
func mcpServersOfConfig(cfg *spec.Config) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.MCPs))
	for name := range cfg.MCPs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

package build

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
)

type toolEnv struct {
	cfg         *spec.Config
	projectRoot string
	dataDir     string
}

type toolProvider interface {
	ID() string
	Optional() bool
	Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
}

type toolRegistry struct {
	providers []toolProvider
}

// defaultToolRegistry assembles the providers.
//
// ★ The MCP provider is registered here, and it is the only provider that
//
//	needs a manager: an MCP client outlives the batch the provider
//	returns, so the manager — not the provider — owns it.
func defaultToolRegistry(mgr *mcpManager) *toolRegistry {
	return &toolRegistry{providers: []toolProvider{
		kbToolProvider{},
		httpRequestToolProvider{},
		webSearchToolProvider{},
		wikipediaToolProvider{},
		mcpToolProvider{mgr: mgr},
		// projectToolsProvider{}, ...
	}}
}

// builtinAllowedTools are the built-in tools that run without a nod.
//
// ★ The default is ask, so the exceptions are named: the tools that only
//
//	read, and the delegation tools.
//
// ★ A tool that has a file of its own (the HTTP, search and knowledge-base
//
//	tools) names its own exceptions in that file — this list is the
//	fallback for the tools that have no file.
//
// ★ `task` is in it because an agent that cannot delegate without a nod
//
//	cannot delegate at all, and delegation is how a session gets work
//	done.
var builtinAllowedTools = func() []string {
	return []string{
		"ls", "read_file", "glob", "grep", // the filesystem's readers
		"task", "write_todos", // the delegation tools
		"skill", // the skill reader
	}
}

var reservedToolNames = map[string]string{
	"ls": "filesystem middleware", "read_file": "filesystem middleware",
	"write_file": "filesystem middleware", "edit_file": "filesystem middleware",
	"glob": "filesystem middleware", "grep": "filesystem middleware",
	"execute": "filesystem middleware",
	"task":    "DeepAgent", "write_todos": "DeepAgent",
	"skill": "skill middleware",
}

func (r *toolRegistry) Build(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	if r == nil {
		return nil, nil
	}
	seen := make(map[string]string)
	var out []tool.BaseTool

	for _, p := range r.providers {
		batch, err := p.Provide(ctx, env)
		if err != nil {
			if p.Optional() {
				obs.Logger().Warn("[agent] tool provider disabled", "provider", p.ID(), "err", err)
				continue
			}
			return nil, fmt.Errorf("tool provider %s: %w", p.ID(), err)
		}
		names, err := validateBatch(ctx, p.ID(), batch, seen)
		if err != nil {
			return nil, fmt.Errorf("tool provider %s: %w", p.ID(), err)
		}
		for _, n := range names {
			seen[n] = p.ID()
		}
		out = append(out, batch...)
	}
	return out, nil
}

func validateBatch(ctx context.Context, providerID string,
	batch []tool.BaseTool, seen map[string]string) ([]string, error) {

	names := make([]string, 0, len(batch))
	local := make(map[string]bool, len(batch))
	for _, t := range batch {
		if t == nil {
			return nil, fmt.Errorf("nil tool")
		}
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool info: %w", err)
		}
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("tool info has an empty name")
		}
		if owner, ok := reservedToolNames[info.Name]; ok {
			return nil, fmt.Errorf("tool %q is owned by %s", info.Name, owner)
		}
		switch t.(type) {
		case tool.InvokableTool, tool.StreamableTool,
			tool.EnhancedInvokableTool, tool.EnhancedStreamableTool:
		default:
			return nil, fmt.Errorf("tool %q is metadata-only; it cannot be executed", info.Name)
		}
		if prev, ok := seen[info.Name]; ok {
			return nil, fmt.Errorf("duplicate tool %q from %s and %s",
				info.Name, prev, providerID)
		}
		if local[info.Name] {
			return nil, fmt.Errorf("duplicate tool %q within %s", info.Name, providerID)
		}
		local[info.Name] = true
		names = append(names, info.Name)
	}
	return names, nil
}

type kbToolProvider struct{}

func (kbToolProvider) ID() string     { return "kb" }
func (kbToolProvider) Optional() bool { return true }

func (kbToolProvider) Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error) {
	return buildKBTools(ctx, env.cfg, env.projectRoot, env.dataDir)
}

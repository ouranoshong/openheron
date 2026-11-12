package build

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type toolEnv struct {
	cfg         *spec.Config
	projectRoot string
	dataDir     string
	log         *slog.Logger
	track       func(io.Closer)

	// svc is the configuration service itself — the only thing that knows
	// where the tree is and how to write it safely. The config_* tools
	// answer through it, so they answer the same way the editor does.
	svc *spec.Service

	// deps is the running tree's dependency map: what each agent depends
	// on, by the file that carries it.
	//
	// ★ The impact query compares it with the current one to answer "what
	//   changes next turn" — and it is the **running** tree's map, so the
	//   answer is about the tree this turn runs.
	deps map[string]agentDeps
	// mcp owns the MCP clients: the dependency walk asks it what the
	// mounted servers offer right now.
	mcp mcpTools
	// skillsDir is where the runtime reads skills from.
	skillsDir string
}

func (e toolEnv) logger() *slog.Logger {
	if e.log == nil {
		return obs.Discard()
	}
	return e.log
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
		configToolProvider{},
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

		// ★ The config tools that only read. They join this list for the
		//   same reason `task` does: they belong to no file, so no file can
		//   name them as an exception — and a question about the
		//   configuration should not cost an approval. `config_write` is
		//   **absent on purpose**: it edits the files this list comes from.
		decl.ToolNameConfigGet,
		decl.ToolNameConfigValidate,
		decl.ToolNameConfigTemplate,
		decl.ToolNameConfigNotices,
		decl.ToolNameConfigTier,
	}
}

// alwaysAsk are the tools no configuration may turn into an exception.
//
// ★ One name, one rule: `config_write` edits the configuration — including
// the file whose `allows:` list would grant it. A file that could grant it
// grants itself, so the exception is removed here, after every source has
// been merged, rather than trusted to stay absent.
var alwaysAsk = map[string]bool{
	decl.ToolNameConfigWrite: true,
}

// reservedToolNames maps a built-in tool's name to what provides it.
//
// ★ The **names** live in decl (one list, so the loader and the builder
//
//	cannot disagree about what exists); only the owner is build's business.
var reservedToolNames = func() map[string]string {
	out := make(map[string]string, len(decl.BuiltinToolNames))
	for _, n := range decl.BuiltinToolNames {
		switch n {
		case "task", "write_todos":
			out[n] = "DeepAgent"
		case "skill":
			out[n] = "skill middleware"
		case "execute":
			// ★ The execute tool is **ours**, not the framework's: openheron
			//   mounts it (see execute.go), because the model writes the
			//   command and only our description says which shell runs it.
			//   The name stays reserved — no provider may claim it.
			out[n] = "openheron's execute tool"
		default:
			out[n] = "filesystem middleware"
		}
	}
	return out
}()

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
				env.logger().Warn("[agent] tool provider disabled", "provider", p.ID(), "err", err)
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
	return buildKBTools(ctx, env.cfg, env.projectRoot, env.dataDir, env.logger(), env.track)
}

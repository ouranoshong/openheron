package build

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/obs/httplog"
	"github.com/ouranoshong/openheron/agentkit/shell"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/llm/common"
)

type snapshot struct {
	// ★ The tree's identity: the configuration it was derived from, and
	// ★ the tool surface the MCP servers offered at that moment.
	//
	// ★ A new configuration **object** is a new tree — that is what a
	//   reload publishes, and what the write path publishes. ★ A surface
	//   that moved is a new tree too: the tool set is built once, and a
	//   server that comes up offers tools the tree does not have yet.
	//
	// ★ There is no counter here any more. ★ A counter said "everything
	//   changed"; these two say "**this** changed", and the pool's key
	//   (the fingerprint) says the same thing per agent.
	cfg     *spec.Config
	surface string

	svc                  *spec.Service
	projectRoot, dataDir string
	log                  *slog.Logger
	track                func(io.Closer)
	agentSpec            agentSpec
	tables               map[string]*Table
	models               modelFactory
	tools                *toolRegistry
	// mcp owns the MCP clients. ★ It outlives the snapshot — a client owns
	//   a child process — and the dependency walk asks it what the servers
	//   offer right now.
	mcp       *mcpManager
	heavyOnce sync.Once
	heavy     heavyDeps
	heavyErr  error

	// skillsDir is where the runtime reads skills from.
	//
	// ★ It is resolved **once** per snapshot: the dependency walk hashes
	//   the skill files and the filesystem backend reads them, and two
	//   resolutions could be two answers.
	skillsDir string
	// deps is what each agent depends on, by the file that carries it.
	//
	// ★ It is the tree's **identity**: two agents with the same
	//   fingerprint are the same agent, whatever the rest of the
	//   configuration did. ★ And it is the answer to "who depends on this
	//   file" — see impact.go.
	deps map[string]agentDeps
}

type heavyDeps struct {
	backend fsapi.Backend
	shell   shell.Shell
	tools   []tool.BaseTool
}

func (s *snapshot) heavyDepsOf(ctx context.Context) (heavyDeps, error) {
	s.heavyOnce.Do(func() {
		s.heavy, s.heavyErr = s.buildHeavy(ctx)
	})
	return s.heavy, s.heavyErr
}

func (s *snapshot) buildHeavy(ctx context.Context) (heavyDeps, error) {
	var out heavyDeps

	// The session workspace (uploads) and the skills directory may live outside the
	// project root; everything else is off limits.
	roots := []string{s.dataDir}
	if s.skillsDir != "" {
		roots = append(roots, s.skillsDir)
	}

	// ★ The shell is decided **once**, here: the execute tool's description
	//   tells the model which dialect to write, and the backend runs the
	//   command — so the two have to be the same answer, and it has to be
	//   the machine's answer at startup.
	sh := shell.Detect()
	s.log.Info("[agent] the execute tool runs commands with this shell", "shell", sh.String())

	local, err := fsx.New(fsx.Config{
		Root:         s.projectRoot,
		ExtraRoots:   roots,
		Shell:        sh.Command(),
		ShellPathDir: sh.Dir,
		// ★ The writes that land inside the configuration root go through
		//   the configuration service: a configuration file is written with
		//   the checks the editor uses (see configgate.go).
		ConfigGate: newConfigGate(s.svc),
	})
	if err != nil {
		return out, fmt.Errorf("build filesystem backend: %w", err)
	}
	out.backend = local
	out.shell = sh

	// ★ The search engine is decided with the backend, and it is worth a
	//   line in the log: a machine whose grep tool runs on the built-in
	//   engine answers the same questions, but not with the same edges.
	s.log.Info("[agent] the grep tool searches with this engine", "engine", local.SearchEngine())

	tools, err := s.tools.Build(ctx, toolEnv{
		cfg:         s.cfg,
		svc:         s.svc,
		deps:        s.deps,
		mcp:         s.mcp,
		skillsDir:   s.skillsDir,
		projectRoot: s.projectRoot,
		dataDir:     s.dataDir,
		log:         s.log,
		track:       s.track,
	})
	if err != nil {
		return out, err
	}
	// ★ The execute tool is **ours** (see execute.go): the framework's
	//   description does not say which shell the command runs in, and the
	//   model is the one who writes the command.
	exec, err := newExecuteTool(local, executeToolDesc(sh))
	if err != nil {
		return out, fmt.Errorf("build the execute tool: %w", err)
	}
	out.tools = append(tools, exec)
	return out, nil
}

func newSnapshot(cfg *spec.Config, surface string, svc *spec.Service, mcp *mcpManager,
	projectRoot, dataDir string, models modelFactory,
	tools *toolRegistry, log *slog.Logger, track func(io.Closer)) (*snapshot, error) {

	sp, err := specOf(cfg, projectRoot)
	if err != nil {
		return nil, err
	}
	if sp.maxDepth <= 0 {
		sp.maxDepth = spec.DefaultMaxDepth
	}
	if models == nil {
		models = defaultModelFactory
	}
	if tools == nil {
		tools = defaultToolRegistry(nil)
	}
	tables := tablesOf(sp)
	// ★ The skills directory is resolved **once** per snapshot: the
	//   dependency walk hashes the skill files, and the filesystem backend
	//   reads them, and two resolutions could be two answers.
	skillsDir, _ := ResolveSkillsDir(log)
	return &snapshot{
		log:         log,
		track:       track,
		cfg:         cfg,
		surface:     surface,
		svc:         svc,
		projectRoot: projectRoot,
		dataDir:     dataDir,
		agentSpec:   sp,
		tables:      tables,
		models:      models,
		tools:       tools,
		mcp:         mcp,
		skillsDir:   skillsDir,
		deps:        computeDeps(cfg, svc, skillsDir, mcp),
	}, nil
}

// snapshotFor returns the tree the process runs on.
//
// ★ The tree is built from the configuration the manager holds **now**, and
//
//	from the tool surface the MCP servers offer right now. ★ Those two are
//	its identity: a reload publishes a new configuration, and a server
//	that comes up changes the surface.
//
// ★ The configuration is read **inside the lock**, so the order of the
//
//	reads is the order of the builds: a caller that arrived late with an
//	older configuration cannot put an older tree back. ★ That is what the
//	generation used to guarantee — and it guaranteed it with a counter
//	that said "everything changed", where this says "**this** changed".
//
// ★ The snapshot is heavier than the agents: it owns the filesystem
//
//	backend and the tool set. ★ The agents themselves are pooled by
//	**fingerprint**, so a snapshot that moves does not rebuild the agents
//	whose inputs did not change.
func (b *Builder) snapshotFor() (*snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg := b.mgr.Current()
	surface := b.mcp.surfaceHash(cfg)
	if b.snap != nil && b.snap.cfg == cfg && b.snap.surface == surface {
		return b.snap, nil
	}
	snap, err := newSnapshot(cfg, surface, b.svc, b.mcp, b.projectRoot, b.dataDir,
		b.models, b.tools, b.log, b.track)
	if err != nil {
		return nil, err
	}
	b.snap = snap
	return snap, nil
}

// fingerprintOf returns the agent's fingerprint, and "" when it has none.
//
// ★ An agent that is not in the dependency map has no fingerprint — and the
//
//	pool then keys it by the empty string, which is a key like any other.
func (s *snapshot) fingerprintOf(name string) string {
	if d, ok := s.deps[name]; ok {
		return d.fingerprint
	}
	return ""
}

func resolveModel(ctx context.Context, snap *snapshot, sp decl.SubAgentSpec,
	base model.BaseModel[*schema.Message]) (model.BaseModel[*schema.Message], error) {

	ref := strings.TrimSpace(sp.ModelRef)
	if ref == "" {
		if base != nil {
			return base, nil
		}
		return buildModel(ctx, snap.log, configuredRequestDump(snap.cfg))
	}
	factory := snap.models
	if factory == nil {
		factory = defaultModelFactory
	}
	return factory(ctx, snap.cfg, ref, CallIntent{
		Reasoning:   sp.Reasoning,
		Temperature: sp.Temperature,
		MaxTokens:   sp.MaxTokens,
	}, snap.log)
}

func buildModel(ctx context.Context, log *slog.Logger, requestDump bool) (model.BaseModel[*schema.Message], error) {
	mc, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	// ★ ConfigFromEnv sets no client, so common.New would build a bare one
	// and the request would never reach the log. This is the non-primary path
	// (no provider or model named in the configuration).
	if mc.HTTPClient == nil {
		mc.HTTPClient = httplog.Wrap(&http.Client{Timeout: mc.Timeout}, mc.ProviderID, log,
			httplog.WithRequestBodyDump(requestDump))
	}
	cm, err := common.New(ctx, mc)
	if err != nil {
		return nil, err
	}
	// ★ The same call guard as the configured path: the cut is the
	//   model's, not the provider file's.
	return newCallGuardModel(common.AttachRequestOptions(cm, common.RequestOptions{
		Model:  mc.Model,
		Fields: mc.ExtraFields,
	}, "", "")), nil
}

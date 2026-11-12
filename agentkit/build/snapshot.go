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
	gen uint64

	cfg                  *spec.Config
	svc                  *spec.Service
	projectRoot, dataDir string
	log                  *slog.Logger
	track                func(io.Closer)
	agentSpec            agentSpec
	tables               map[string]*Table
	models               modelFactory
	tools                *toolRegistry
	heavyOnce            sync.Once
	heavy                heavyDeps
	heavyErr             error
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
	if dir, ok := ResolveSkillsDir(s.log); ok {
		roots = append(roots, dir)
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

func newSnapshot(gen uint64, cfg *spec.Config, svc *spec.Service,
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
	return &snapshot{
		log:         log,
		track:       track,
		gen:         gen,
		cfg:         cfg,
		svc:         svc,
		projectRoot: projectRoot,
		dataDir:     dataDir,
		agentSpec:   sp,
		tables:      tables,
		models:      models,
		tools:       tools,
	}, nil
}

func (b *Builder) snapshotFor(cfg *spec.Config, gen uint64) (*snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.snap != nil && b.snap.gen >= gen {
		return b.snap, nil
	}
	snap, err := newSnapshot(gen, cfg, b.svc, b.projectRoot, b.dataDir, b.models, b.tools, b.log, b.track)
	if err != nil {
		return nil, err
	}
	b.snap = snap
	return snap, nil
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
	return common.AttachRequestOptions(cm, common.RequestOptions{
		Model:  mc.Model,
		Fields: mc.ExtraFields,
	}, "", ""), nil
}

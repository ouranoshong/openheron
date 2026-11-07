package build

import (
	"context"
	"fmt"
	"strings"
	"sync"

	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/llm/common"
)

type snapshot struct {
	gen uint64

	cfg                  *spec.Config
	projectRoot, dataDir string
	agentSpec            agentSpec
	tables               map[string]*Table
	models               modelFactory
	tools                *toolRegistry
	heavyOnce            sync.Once
	heavy                heavyDeps
	heavyErr             error
}

type heavyDeps struct {
	backend        fsapi.Backend
	streamingShell fsapi.StreamingShell
	tools          []tool.BaseTool
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
	if dir, ok := resolveSkillsDir(); ok {
		roots = append(roots, dir)
	}
	local, err := fsx.New(fsx.Config{Root: s.projectRoot, ExtraRoots: roots})
	if err != nil {
		return out, fmt.Errorf("build filesystem backend: %w", err)
	}
	out.backend = local
	out.streamingShell = local

	tools, err := s.tools.Build(ctx, toolEnv{
		cfg:         s.cfg,
		projectRoot: s.projectRoot,
		dataDir:     s.dataDir,
	})
	if err != nil {
		return out, err
	}
	out.tools = tools
	return out, nil
}

func newSnapshot(gen uint64, cfg *spec.Config, projectRoot, dataDir string,
	models modelFactory, tools *toolRegistry) (*snapshot, error) {

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
		gen:         gen,
		cfg:         cfg,
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
	snap, err := newSnapshot(gen, cfg, b.projectRoot, b.dataDir, b.models, b.tools)
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
		return buildModel(ctx)
	}
	factory := snap.models
	if factory == nil {
		factory = defaultModelFactory
	}
	return factory(ctx, snap.cfg, ref, CallIntent{
		Reasoning:   sp.Reasoning,
		Temperature: sp.Temperature,
		MaxTokens:   sp.MaxTokens,
	})
}

func buildModel(ctx context.Context) (model.BaseModel[*schema.Message], error) {
	mc, err := ConfigFromEnv()
	if err != nil {
		return nil, err
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

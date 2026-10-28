package build

import (
	"context"
	"openheron/agentkit/obs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	localbk "github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"
)

func skillChain(ctx context.Context, backend fsapi.Backend, hubs *skillHubs) (
	adk.TypedChatModelAgentMiddleware[*schema.Message], error) {

	dir, ok := resolveSkillsDir()
	if !ok {
		return nil, nil
	}
	skillBackend, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
		Backend: backend,
		BaseDir: dir,
	})
	if err != nil {
		return nil, err
	}
	cfg := &skill.TypedConfig[*schema.Message]{Backend: skillBackend}
	if hubs != nil {
		cfg.AgentHub = hubs.agent
		cfg.ModelHub = hubs.model
	}
	return skill.NewTyped[*schema.Message](ctx, cfg)
}

func resolveSkillsDir() (string, bool) {
	dir := strings.TrimSpace(os.Getenv("EINO_EXT_SKILLS_DIR"))
	if dir == "" {
		if found, ok := probeProjectSkills(); ok {
			dir = found
		}
	}
	if dir == "" {
		return "", false
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		if fromEnv := strings.TrimSpace(os.Getenv("EINO_EXT_SKILLS_DIR")); fromEnv != "" {
			obs.Logger().Warn("[agent] EINO_EXT_SKILLS_DIR points at a missing dir, skills disabled",
				"dir", fromEnv)
		}
		return "", false
	}
	return dir, true
}

func probeProjectSkills() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		cand := filepath.Join(dir, ".openheron", "skills")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Context     string `json:"context"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
}

func Skills(ctx context.Context) []SkillInfo {
	dir, ok := resolveSkillsDir()
	if !ok {
		return nil
	}
	backend, err := localbk.NewBackend(ctx, &localbk.Config{})
	if err != nil {
		obs.Logger().Warn("[agent] failed to build the filesystem backend for the skill list", "err", err)
		return nil
	}
	skillBackend, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
		Backend: backend,
		BaseDir: dir,
	})
	if err != nil {
		obs.Logger().Warn("[agent] failed to build the skill backend", "err", err)
		return nil
	}
	list, err := skillBackend.List(ctx)
	if err != nil {
		obs.Logger().Warn("[agent] failed to list the skills", "dir", dir, "err", err)
		return nil
	}
	out := make([]SkillInfo, 0, len(list))
	for _, fm := range list {
		mode := string(fm.Context)
		if mode == "" {
			mode = "inline"
		}
		out = append(out, SkillInfo{
			Name:        fm.Name,
			Description: fm.Description,
			Context:     mode,
			Agent:       fm.Agent,
			Model:       fm.Model,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

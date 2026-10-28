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

// 本文件是 skill 侧的**接线**：skill 中间件（让 agent 拥有 `skill` 工具）与
// skills 目录的探测。
//
// ★ eino 官方 quickstart 第九章的「在 DeepAgent 中启用 Skill」三步：
//
//	① 用本地 filesystem backend 提供文件读取/Glob 能力   → [Builder.doSetup]
//	② 用 skill.NewBackendFromFilesystem 把目录变成一个 Skill Backend → [skillChain]
//	③ 用 skill.NewTyped[M] 生成中间件，塞进 Handlers     → [skillChain]
//
// ★ 两个 hub 的**实现**在 [hub.go]（eino 只给接口）。
// skillChain 造 **skill 侧**的中间件（"怎么做"）。
//
// ★ 官方启用 skill 的第 ②③ 步（quickstart 第九章）：
//
//	② skill.NewBackendFromFilesystem 把 skills 目录变成一个 Skill Backend
//	③ skill.NewTyped[M] 生成 Skill middleware
//
// ★ 没有 skills 目录就不注册 —— 不是错误，是「这个 agent 不带说明书」。
//
//	官方 quickstart 也这么做（"没配置 skills 也能跑"）。而目录显式配了却不
//	存在时必须**说出来**（见 [resolveSkillsDir]）—— 那是「配了没用」。
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
		// ★ 两个 hub 必须都挂：在 inline 模式下缺 ModelHub 会让 `model:` 被
		//   静默忽略，在 fork 模式下缺 AgentHub 会让整个 skill 调用失败。
		cfg.AgentHub = hubs.agent
		cfg.ModelHub = hubs.model
	}
	return skill.NewTyped[*schema.Message](ctx, cfg)
}

// resolveSkillsDir 找到本轮要挂的 skills 目录。
//
// ★ 找不到不是错误 —— 「这个 agent 不带说明书」是合法状态。
//
//	但**显式配了却不存在**要说出来（见函数体），那是「配了没用」。
func resolveSkillsDir() (string, bool) {
	// ★ 环境变量是**覆盖项**；未设时探测项目级 .openheron/skills ——
	//	//   「skills 属于这个项目的行为定义」与它同根才自然。
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
		// ★ 显式配了却不存在 ⇒ **说出来**。静默返回 false 会让「我配了 skills
		//   怎么没生效」变成一个查不出原因的问题（属「配了没用」）。
		if fromEnv := strings.TrimSpace(os.Getenv("EINO_EXT_SKILLS_DIR")); fromEnv != "" {
			obs.Logger().Warn("[agent] EINO_EXT_SKILLS_DIR points at a missing dir, skills disabled",
				"dir", fromEnv)
		}
		return "", false
	}
	return dir, true
}

// probeProjectSkills 从当前目录向上找 `.openheron/skills`。
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

// SkillInfo 是一个 skill 的清单项。
//
// ★ description 是**模型选不选它**的唯一依据 —— 也是用户「查看」时最想
//
//	看的那一行。frontmatter 的其余字段（context / agent / model）是执行
//	细节：列出来是为了让人知道「这个 skill 会换 agent / 换模型」。
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Context 是执行模式：inline / fork / fork_with_context。
	Context string `json:"context"`
	// Agent 是 fork 模式要用的 agent 名（空 = 当前 agent）。
	Agent string `json:"agent,omitempty"`
	// Model 是这个 skill 覆盖的模型（空 = 不覆盖）。
	Model string `json:"model,omitempty"`
}

// Skills 返回这一代挂着的 skill 清单。
//
// ★ 用 eino 的 backend 解析（[skill.NewBackendFromFilesystem]）—— 与装配时
//
//	**同一份** frontmatter 解析器：自己再解一遍 yaml，两边的 name /
//	description 迟早不一致，而模型看到的与用户看到的不一样是最难查的一类。
//
// ★ 没有 skills 目录时返回空 —— 「这个 agent 不带说明书」是合法状态。
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
			// ★ 空 = inline（eino 的默认）。写出来是为了让人看得懂 ——
			//   界面上一个空的「模式」列没人知道意味着什么。
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

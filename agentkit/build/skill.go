package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/obs"
)

func skillChain(ctx context.Context, backend fsapi.Backend, hubs *skillHubs, scope skillScope) (
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
	scoped, err := newScopedSkillBackend(ctx, skillBackend, scope)
	if err != nil {
		return nil, err
	}
	cfg := &skill.TypedConfig[*schema.Message]{Backend: scoped}
	if hubs != nil {
		cfg.AgentHub = hubs.agent
		cfg.ModelHub = hubs.model
	}
	return skill.NewTyped[*schema.Message](ctx, cfg)
}

// skillScope is one agent's allow-list of skills.
//
// ★ A nil scope means "every skill in the skills directory" — that is what an
//
//	agent got before the `skills` field existed. An empty scope means none.
//	The distinction is the whole point of the field, so it is carried as a
//	pointer all the way from the frontmatter.
type skillScope struct {
	all   bool
	names map[string]bool
}

// skillScopeOf reads the allow-list of an agent's frontmatter.
//
// A nil list is "not declared" and means no restriction; an empty list is
// "declared, and empty" and means the agent gets no skill at all.
func skillScopeOf(list *[]string) skillScope {
	if list == nil {
		return skillScope{all: true}
	}
	s := skillScope{names: map[string]bool{}}
	for _, n := range *list {
		n = strings.TrimSpace(n)
		if n != "" {
			s.names[n] = true
		}
	}
	return s
}

// scopedSkillBackend restricts a skill backend to one agent's allow-list.
//
// ★ Both halves matter. List is what the model **sees** — the skill tool's
//
//	description is rendered from it — and Get is what the model can actually
//	**load**. Filtering only List would hide a skill from the description and
//	still let a model that remembers the name load it.
type scopedSkillBackend struct {
	inner skill.Backend
	scope skillScope
	// folders maps a skill's frontmatter name to the folder it lives in.
	folders map[string]string
	// known holds every name this backend answers to: the folders and the
	// skills' own names.
	known map[string]bool
}

func newScopedSkillBackend(ctx context.Context, inner skill.Backend, scope skillScope) (
	*scopedSkillBackend, error) {

	b := &scopedSkillBackend{inner: inner, scope: scope}
	if scope.all {
		return b, nil
	}
	matters, err := inner.List(ctx)
	if err != nil {
		return nil, err
	}
	b.folders = make(map[string]string, len(matters))
	b.known = make(map[string]bool, len(matters)*2)
	for _, m := range matters {
		b.known[m.Name] = true
		// ★ The upstream backend re-reads the whole directory on every Get,
		//   so this loop is quadratic in the number of skills. That is the
		//   price of the folder name: it is the only place a skill says
		//   which folder it came from, and the folder is what the user
		//   writes in the allow-list. A skills directory holds a handful of
		//   files, and the mapping is built once per agent build.
		loaded, err := inner.Get(ctx, m.Name)
		if err != nil {
			continue
		}
		folder := filepath.Base(loaded.BaseDirectory)
		b.folders[m.Name] = folder
		b.known[folder] = true
	}
	// ★ A name that is not there is a typo, and a typo that silently narrows
	//   an agent to nothing is the worst kind: the agent looks configured and
	//   behaves as if it had no skill at all.
	for _, name := range b.missingSkills() {
		obs.Logger().Warn("[agent] the skill is not in the skills directory", "skill", name)
	}
	return b, nil
}

// missingSkills lists the names of the allow-list that the skills directory
// does not have.
func (b *scopedSkillBackend) missingSkills() []string {
	var out []string
	for name := range b.scope.names {
		if !b.known[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// allows reports whether this agent may use the named skill.
//
// ★ The folder name is accepted next to the skill's own name: the user names
//
//	a skill by the folder they see in the directory tree, the model names it
//	by the frontmatter.
func (b *scopedSkillBackend) allows(name string) bool {
	if b.scope.all {
		return true
	}
	if b.scope.names[name] {
		return true
	}
	return b.scope.names[b.folders[name]]
}

func (b *scopedSkillBackend) List(ctx context.Context) ([]skill.FrontMatter, error) {
	matters, err := b.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	if b.scope.all {
		return matters, nil
	}
	out := make([]skill.FrontMatter, 0, len(matters))
	for _, m := range matters {
		if b.allows(m.Name) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (b *scopedSkillBackend) Get(ctx context.Context, name string) (skill.Skill, error) {
	loaded, err := b.inner.Get(ctx, name)
	if err != nil {
		return skill.Skill{}, err
	}
	if !b.allows(name) {
		return skill.Skill{}, fmt.Errorf("skill %q is not available to this agent", name)
	}
	return loaded, nil
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
	backend, err := fsx.New(fsx.Config{Root: dir})
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

package spec

import (
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

func TestCategoryTemplatesDecodeStrictly(t *testing.T) {
	t.Run("llm", func(t *testing.T) {
		var pr decl.Provider
		if err := decodeStrict([]byte(Skeleton(DirLLM, nil)), &pr); err != nil {
			t.Fatalf("the llm template must decode with the loader's own decoder: %v", err)
		}
	})

	t.Run("agent", func(t *testing.T) {
		fm, _, _, err := splitFrontmatter([]byte(Skeleton(DirAgent, nil)))
		if err != nil {
			t.Fatalf("the agent template must have a frontmatter: %v", err)
		}
		var a decl.Agent
		if err := decodeStrict(fm, &a); err != nil {
			t.Fatalf("the agent template must decode with the loader's own decoder: %v", err)
		}
	})
}

// ★ The scaffold a user gets when they create an agent in the settings editor
//
//	has to name the skill key: a field nobody can find is a field nobody
//	uses.
func TestTheAgentTemplateNamesTheSkillKey(t *testing.T) {
	fm, _, _, err := splitFrontmatter([]byte(Skeleton(DirAgent, nil)))
	if err != nil {
		t.Fatalf("the agent template must have a frontmatter: %v", err)
	}
	if !strings.Contains(string(fm), "skills:") {
		t.Fatalf("the agent template must name the skills key:\n%s", fm)
	}
}

func TestDeclarationsDecodeWithTheirOwnTags(t *testing.T) {
	var (
		provider decl.Provider
		model    decl.Model
		agent    decl.Agent
		server   decl.Server
		kb       decl.KB
	)
	for _, tc := range []struct {
		name string
		v    any
	}{
		{"provider", &provider},
		{"model", &model},
		{"agent", &agent},
		{"server", &server},
		{"kb", &kb},
	} {
		if err := decodeStrict([]byte("{}\n"), tc.v); err != nil {
			t.Errorf("%s must decode from an empty document: %v", tc.name, err)
		}
	}
}

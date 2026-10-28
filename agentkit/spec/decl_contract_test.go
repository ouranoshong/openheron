package spec

import (
	"testing"

	"openheron/agentkit/spec/decl"
)

func TestCategoryTemplatesDecodeStrictly(t *testing.T) {
	t.Run("policy", func(t *testing.T) {
		var p decl.Policy
		if err := decodeStrict([]byte(Template(DirPolicy, nil)), &p); err != nil {
			t.Fatalf("the policy template must decode with the loader's own decoder: %v", err)
		}
	})

	t.Run("llm", func(t *testing.T) {
		var pr decl.Provider
		if err := decodeStrict([]byte(Template(DirLLM, nil)), &pr); err != nil {
			t.Fatalf("the llm template must decode with the loader's own decoder: %v", err)
		}
	})

	t.Run("agent", func(t *testing.T) {
		fm, _, err := splitFrontmatter([]byte(Template(DirAgent, nil)))
		if err != nil {
			t.Fatalf("the agent template must have a frontmatter: %v", err)
		}
		var a decl.Agent
		if err := decodeStrict(fm, &a); err != nil {
			t.Fatalf("the agent template must decode with the loader's own decoder: %v", err)
		}
	})
}

func TestDeclarationsDecodeWithTheirOwnTags(t *testing.T) {
	var (
		provider decl.Provider
		model    decl.Model
		agent    decl.Agent
		policy   decl.Policy
		limits   decl.Limits
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
		{"policy", &policy},
		{"limits", &limits},
		{"server", &server},
		{"kb", &kb},
	} {
		if err := decodeStrict([]byte("{}\n"), tc.v); err != nil {
			t.Errorf("%s must decode from an empty document: %v", tc.name, err)
		}
	}
}
